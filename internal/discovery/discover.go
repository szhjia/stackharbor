package discovery

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/config"
	"github.com/szhjia/stackharbor/internal/docker"
	"github.com/szhjia/stackharbor/internal/model"
	"os"
	"path/filepath"
	"sort"
)

type Options struct {
	Root, WorkspaceFile string
	RootExplicit        bool
}

func Discover(ctx context.Context, opts Options) model.Workspace {
	w := model.Workspace{}
	root := opts.Root
	if root == "" {
		root, _ = os.Getwd()
	}
	root, err := filepath.Abs(root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		w.Diagnostics = append(w.Diagnostics, model.Error(root, "root", err.Error()))
		return w
	}
	wf := opts.WorkspaceFile
	if wf == "" {
		p := filepath.Join(root, "stackharbor.workspace.yaml")
		if _, e := os.Stat(p); e == nil {
			wf = p
		}
	}
	cfg := config.WorkspaceConfig{Discover: true}
	if wf != "" {
		wf, err = filepath.Abs(wf)
		if err != nil {
			w.Diagnostics = append(w.Diagnostics, model.Error(wf, "workspace", err.Error()))
			return w
		}
		var ds []model.Diagnostic
		cfg, ds = config.LoadWorkspace(wf)
		w.Diagnostics = append(w.Diagnostics, ds...)
		if len(ds) > 0 {
			return w
		}
		if cfg.RootExplicit {
			target := cfg.Root
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(wf), target)
			}
			target, err = filepath.EvalSymlinks(target)
			if err != nil {
				w.Diagnostics = append(w.Diagnostics, model.Error(wf, "root", err.Error()))
				return w
			}
			if opts.RootExplicit && root != target {
				w.Diagnostics = append(w.Diagnostics, model.Error(wf, "root", "--root conflicts with workspace root"))
				return w
			}
			root = target
		}
	}
	w.Root = root
	if wf != "" {
		w.InputFiles = append(w.InputFiles, wf)
	}
	w.Version = cfg.Version
	if len(cfg.Resources) > 0 {
		p, ds := config.ResourceProject(wf, root, cfg)
		w.Diagnostics = append(w.Diagnostics, ds...)
		w.Projects = append(w.Projects, p)
	}
	paths := []string{}
	for _, p := range cfg.Registrations {
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(wf), p)
		}
		paths = append(paths, p)
	}
	dirs := []string{}
	if cfg.Discover {
		var ds []model.Diagnostic
		paths, dirs, ds = scan(ctx, root, paths, cfg.Exclude)
		w.Diagnostics = append(w.Diagnostics, ds...)
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	registered := map[string]bool{}
	count := len(w.Services())
	sort.Strings(paths)
	for _, p := range paths {
		real, e := filepath.EvalSymlinks(p)
		if e != nil {
			w.Diagnostics = append(w.Diagnostics, model.Error(p, "registrations", e.Error()))
			continue
		}
		if seen[real] {
			continue
		}
		seen[real] = true
		project, ds := config.LoadProject(real, root)
		for _, n := range project.Services {
			if n.Version == 2 {
				w.Version = 2
			}
		}
		w.Diagnostics = append(w.Diagnostics, ds...)
		if len(ds) > 0 {
			continue
		}
		if !config.Within(root, filepath.Dir(real)) {
			for _, s := range project.Services {
				if s.Cwd == filepath.Dir(real) {
					w.Diagnostics = append(w.Diagnostics, model.Error(real, "cwd", "external registration requires explicit cwd"))
				}
			}
		}
		if ids[project.ID] {
			w.Diagnostics = append(w.Diagnostics, model.Error(real, "project.id", "duplicate project ID"))
		}
		ids[project.ID] = true
		count += len(project.Services)
		if len(w.Projects) >= 1000 || count > 1000 {
			w.Diagnostics = append(w.Diagnostics, model.Error(real, "services", "workspace exceeds 1000 projects/services"))
			break
		}
		w.Projects = append(w.Projects, project)
		for _, s := range project.Services {
			registered[s.Cwd] = true
		}
	}
	for _, d := range dirs {
		if registered[d] {
			continue
		}
		c, ds := candidate(root, d)
		w.Diagnostics = append(w.Diagnostics, ds...)
		if c != nil {
			w.Candidates = append(w.Candidates, *c)
		}
	}
	compose := docker.New(root)
	for _, spec := range w.Services() {
		if _, err := compose.DependencyOrder(spec.DockerDependsOn); err != nil {
			w.Diagnostics = append(w.Diagnostics, model.Error(spec.SourceFile, "docker_depends_on", err.Error()))
		}
	}
	sort.Slice(w.Projects, func(i, j int) bool { return w.Projects[i].ID < w.Projects[j].ID })
	sort.Slice(w.Candidates, func(i, j int) bool { return w.Candidates[i].Cwd < w.Candidates[j].Cwd })
	return w
}

var _ = fmt.Sprintf
