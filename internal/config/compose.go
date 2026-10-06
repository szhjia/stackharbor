package config

import (
	"context"
	"github.com/szhjia/stackharbor/internal/docker"
	"github.com/szhjia/stackharbor/internal/model"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func normalizeComposeScope(path, root string, r model.ResourceSpec) (docker.ComposeScope, []model.Diagnostic) {
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	s := docker.ComposeScope{Project: r.Project}
	var ds []model.Diagnostic
	bad := func(field, msg string) { ds = append(ds, model.Error(path, field, msg)) }
	if r.File != "" && r.Files != nil {
		bad("files", "file and files are mutually exclusive")
		return s, ds
	}
	files := r.Files
	if r.File != "" {
		files = []string{r.File}
	}
	if len(files) == 0 {
		bad("files", "at least one Compose file required")
		return s, ds
	}
	resolve := func(raw, field string, dir bool) string {
		if raw == "" {
			bad(field, "empty path")
			return ""
		}
		p := raw
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(path), p)
		}
		real, e := filepath.EvalSymlinks(p)
		if e != nil || !Within(root, real) {
			bad(field, "path missing or outside root")
			return ""
		}
		st, e := os.Stat(real)
		if e != nil || dir && !st.IsDir() || !dir && !st.Mode().IsRegular() {
			bad(field, "unexpected path type")
			return ""
		}
		return real
	}
	for _, group := range []struct {
		values []string
		field  string
		out    *[]string
	}{{files, "files", &s.Files}, {r.EnvFiles, "env_files", &s.EnvFiles}} {
		seen := map[string]bool{}
		for _, v := range group.values {
			p := resolve(v, group.field, false)
			if p == "" {
				continue
			}
			if seen[p] {
				bad(group.field, "duplicate path")
				continue
			}
			seen[p] = true
			*group.out = append(*group.out, p)
		}
	}
	if r.ProjectDirectory != "" {
		s.ProjectDirectory = resolve(r.ProjectDirectory, "project_directory", true)
	} else if len(s.Files) > 0 {
		s.ProjectDirectory = filepath.Dir(s.Files[0])
	}
	return s, ds
}

func ResourceProject(path, root string, c WorkspaceConfig) (model.Project, []model.Diagnostic) {
	p := model.Project{ID: "_resources", Name: "Infrastructure", SourceFile: path}
	var ds []model.Diagnostic
	keys := []string{}
	for k := range c.Resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	resolved := map[string]*docker.ResolvedCompose{}
	env := os.Environ()
	for _, k := range keys {
		r := c.Resources[k]
		bad := func(msg string) { ds = append(ds, model.Error(path, "resources."+k, msg)) }
		if !identifier.MatchString(k) || !identifier.MatchString(r.Service) || r.Adapter != "compose" || r.Project == "" || strings.HasPrefix(r.Project, "-") {
			bad("explicit compose file/project/service required")
			continue
		}
		scope, problems := normalizeComposeScope(path, root, r)
		if len(problems) > 0 {
			for _, d := range problems {
				d.Field = "resources." + k + "." + d.Field
				ds = append(ds, d)
			}
			continue
		}
		r.Files = scope.Files
		r.File = scope.Files[0]
		r.ProjectDirectory = scope.ProjectDirectory
		r.EnvFiles = scope.EnvFiles
		if r.Control != "managed" && r.Control != "observe" {
			bad("control must be managed or observe")
		}
		if r.Lifetime != "persistent" && r.Lifetime != "session" {
			bad("lifetime must be persistent or session")
		}
		if r.Available != "healthy" && r.Available != "running" {
			bad("available must be healthy or running")
		}
		key := scope.Key()
		if resolved[key] == nil {
			v, e := docker.Resolve(context.Background(), scope, env)
			if e != nil {
				bad(e.Error())
				continue
			}
			resolved[key] = v
		}
		v := resolved[key]
		decl, ok := v.Services[r.Service]
		if !ok {
			bad("invalid Compose resource: unknown service")
			continue
		}
		if r.Available == "healthy" && !decl.HasHealthcheck {
			bad("healthy resource requires a Compose healthcheck")
		}
		identity := r.Project + "\x00" + r.Service
		if seen[identity] {
			bad("duplicate resource identity")
		}
		seen[identity] = true
		r.SetComposeEvidence(v.Digest, env)
		name := r.Name
		if name == "" {
			name = k
		}
		inputs := append([]string{path}, scope.Files...)
		inputs = append(inputs, scope.EnvFiles...)
		p.Services = append(p.Services, model.Service{ID: model.ServiceID("resource/" + k), ProjectID: p.ID, Key: k, Name: name, Kind: "resource", Version: 2, SourceFile: path, Cwd: root, Resource: &r, InputFiles: inputs})
	}
	for i := range p.Services {
		n := &p.Services[i]
		r := n.Resource
		scope := docker.ScopeFor(*r)
		decl := resolved[scope.Key()].Services[r.Service]
		deps := []string{}
		for dep := range decl.Dependencies {
			deps = append(deps, dep)
		}
		sort.Strings(deps)
		for _, dep := range deps {
			edge := decl.Dependencies[dep]
			bad := func(msg string) { ds = append(ds, model.Error(path, "resources."+n.Key, msg)) }
			if !edge.Required || edge.Restart {
				bad("optional/automatic-restart Compose edges require an explicit supported contract")
			}
			found := false
			for _, other := range p.Services {
				o := other.Resource
				if docker.ScopeFor(*o).Key() == scope.Key() && o.Service == dep {
					found = true
					if edge.Condition != "service_started" && edge.Condition != "service_healthy" || edge.Condition == "service_healthy" && o.Available != "healthy" {
						bad("Compose dependency condition not represented; register initialization as an explicit task")
					}
					n.DependsOn = append(n.DependsOn, other.ID)
					n.Requires = append(n.Requires, model.Requirement{Node: other.ID, Condition: "available"})
				}
			}
			if !found {
				bad("Compose dependency must be explicitly registered: " + dep)
			}
		}
	}
	return p, ds
}
