package config

import (
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func validateV2(path, root string, f projectFile) (model.Project, []model.Diagnostic) {
	p := model.Project{ID: f.Project.ID, Name: f.Project.Name, SourceFile: path}
	ds := []model.Diagnostic{}
	bad := func(field, msg string) { ds = append(ds, model.Error(path, field, msg)) }
	inputFiles := []string{path}
	env := map[string]string{}
	for _, pair := range os.Environ() {
		k, v, ok := strings.Cut(pair, "=")
		if ok {
			env[k] = v
		}
	}
	for _, name := range f.Context.EnvFiles {
		file := name
		if !filepath.IsAbs(file) {
			file = filepath.Join(filepath.Dir(path), file)
		}
		real, e := filepath.EvalSymlinks(file)
		if e != nil || !Within(root, real) {
			bad("context.env_files", "missing file or path outside root")
			continue
		}
		b, e := os.ReadFile(real)
		if e != nil || len(b) > 1048576 {
			bad("context.env_files", "cannot read bounded env file")
			continue
		}
		inputFiles = append(inputFiles, real)
		for i, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")
			k, v, ok := strings.Cut(line, "=")
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if !ok || !envKey(k) {
				bad("context.env_files", fmt.Sprintf("invalid assignment at line %d", i+1))
				continue
			}
			if len(v) > 0 && (v[0] == '\'' || v[0] == '"') {
				q := v[0]
				if len(v) < 2 || v[len(v)-1] != q {
					bad("context.env_files", fmt.Sprintf("unclosed quote at line %d", i+1))
					continue
				}
				v = v[1 : len(v)-1]
			} else if j := strings.Index(v, " #"); j >= 0 {
				v = strings.TrimSpace(v[:j])
			}
			if strings.Contains(v, "${") || strings.Contains(v, "$(") || strings.ContainsRune(v, 0) {
				bad("context.env_files", fmt.Sprintf("interpolation unsupported at line %d", i+1))
				continue
			}
			env[k] = v
		}
	}
	for k, v := range f.Context.Env {
		env[k] = v
	}
	add := func(k, kind string, v serviceFile, task *model.TaskSpec) {
		if len(v.DependsOn) > 0 || len(v.DockerDependsOn) > 0 {
			bad(kind+"."+k, "v2 uses explicit requires")
			return
		}
		if v.Cwd == "" {
			v.Cwd = f.Context.Cwd
		}
		v.Env = mergeEnv(env, v.Env)
		original := append([]string{}, v.Run.Command...)
		if v.Control == "observe" {
			if kind != "service" || len(original) > 0 || v.Ready == nil || len(v.Ports) == 0 || v.Stop.Signal != "" || v.Stop.TimeoutSeconds != 0 || v.Identity == nil || !validArgs(v.Identity.Command) {
				bad("services."+k, "observe requires identity ports and readiness, forbids run/stop")
				return
			}
			v.Run.Command = []string{"observe"}
		}
		if v.Control != "observe" && v.Identity != nil {
			bad("services."+k, "identity only supported for observe services")
			return
		}
		one := projectFile{Version: 1, Services: map[string]serviceFile{k: v}}
		one.Project = f.Project
		n, errs := validateProject(path, root, one)
		ds = append(ds, errs...)
		if len(errs) > 0 {
			return
		}
		node := n.Services[0]
		node.ID = model.ServiceID(f.Project.ID + "/" + kind + "/" + k)
		node.Kind = kind
		node.Version = 2
		node.Requires = v.Requires
		node.Control = v.Control
		node.EnvFrozen = true
		node.Task = task
		node.Identity = v.Identity
		node.InputFiles = append([]string{}, inputFiles...)
		if v.Control == "observe" {
			node.Command = nil
		}
		for _, dep := range v.Requires {
			node.DependsOn = append(node.DependsOn, dep.Node)
		}
		if task != nil {
			for _, name := range task.Inputs {
				file := name
				if !filepath.IsAbs(file) {
					file = filepath.Join(node.Cwd, file)
				}
				real, e := filepath.EvalSymlinks(file)
				if e != nil || !Within(root, real) {
					bad("tasks."+k+".inputs", "missing input or path outside root")
					continue
				}
				node.InputFiles = append(node.InputFiles, real)
			}
		}
		p.Services = append(p.Services, node)
	}
	keys := []string{}
	for k := range f.Tasks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t := f.Tasks[k]
		if t.Policy == "" {
			t.Policy = "always"
		}
		if t.TimeoutSeconds == 0 {
			t.TimeoutSeconds = 120
		}
		if t.TimeoutSeconds < 1 || t.TimeoutSeconds > 600 {
			bad("tasks."+k, "timeout_seconds must be 1–600")
		}
		switch t.Effect {
		case "read-only", "schema-write", "environment-write", "data-write":
		default:
			bad("tasks."+k, "explicit effect required")
		}
		switch t.Policy {
		case "always":
			if t.Effect != "read-only" {
				bad("tasks."+k, "always allowed only for read-only tasks")
			}
		case "when-needed":
			if t.Check == nil || t.Verify == nil || !validArgs(t.Check.Command) || !validArgs(t.Verify.Command) {
				bad("tasks."+k, "when-needed requires check and verify commands")
			}
			if t.Check != nil && (t.Check.SatisfiedExit != 0 || t.Check.NeededExit != 10 || t.Check.DriftExit != 20) {
				bad("tasks."+k, "check exits must be 0/10/20")
			}
			if t.Verify != nil && t.Verify.SuccessExit != 0 {
				bad("tasks."+k, "verify success_exit must be 0")
			}
		case "manual":
		default:
			bad("tasks."+k, "unknown policy")
		}
		if t.Effect == "schema-write" && (t.Policy != "when-needed" || t.Lock == nil || t.Lock.Scope != "database" || t.Lock.Target.Env == "" || mergeEnv(env, t.Env)[t.Lock.Target.Env] == "") {
			bad("tasks."+k, "schema-write requires when-needed and explicit database target lock")
		}
		if t.Lock != nil && t.Effect != "schema-write" {
			bad("tasks."+k, "database lock only supported for schema-write")
		}
		if t.Lock != nil {
			if t.Lock.SharedID != "" {
				bad("tasks."+k, "shared_id unsupported; PostgreSQL target lock already unifies host aliases")
			}
			u, e := url.Parse(mergeEnv(env, t.Env)[t.Lock.Target.Env])
			if e != nil || u.Hostname() == "" || u.Path == "" || (u.Scheme != "postgresql" && u.Scheme != "postgres" && u.Scheme != "postgresql+psycopg2") {
				bad("tasks."+k, "database lock requires an explicit PostgreSQL URL")
			}
		}
		v := serviceFile{Name: t.Name, Cwd: t.Cwd, Env: t.Env, Requires: t.Requires}
		v.Run.Command = t.Run.Command
		add(k, "task", v, &t.TaskSpec)
	}
	keys = nil
	for k := range f.Services {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := f.Services[k]
		if v.Control != "" && v.Control != "managed" && v.Control != "observe" {
			bad("services."+k, "unknown control")
		}
		add(k, "service", v, nil)
	}
	if len(p.Services) == 0 {
		bad("nodes", "requires at least one task or service")
	}
	return p, ds
}
func mergeEnv(base, override map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}
func envKey(k string) bool {
	if k == "" {
		return false
	}
	for i, c := range k {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}
func validArgs(args []string) bool {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return false
	}
	for _, s := range args {
		if strings.ContainsRune(s, 0) {
			return false
		}
	}
	return true
}
