package cli

import (
	"crypto/rand"
	"fmt"
	"github.com/szhjia/stackharbor/internal/config"
	"github.com/szhjia/stackharbor/internal/model"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
)

type Draft struct {
	Path       string
	Content    []byte
	NeedsInput []string
}

func PlanInit(w model.Workspace) []Draft {
	drafts := []Draft{}
	seen := map[string]bool{}
	for _, p := range w.Projects {
		seen[p.ID] = true
	}
	for _, c := range w.Candidates {
		id := c.ID
		for n := 2; seen[id]; n++ {
			id = fmt.Sprintf("%s-%d", c.ID, n)
		}
		seen[id] = true
		needs := append([]string{}, c.NeedsInput...)
		if len(c.SuggestedCommand) == 0 && len(needs) == 0 {
			needs = append(needs, "Foreground command required")
		}
		doc := map[string]any{"version": 1, "project": map[string]string{"id": id, "name": c.Name}, "services": map[string]any{"dev": map[string]any{"run": map[string]any{"command": c.SuggestedCommand}}}}
		b, _ := yaml.Marshal(doc)
		b = append([]byte("# Registration draft: review command, ports and readiness before running.\n"), b...)
		drafts = append(drafts, Draft{Path: filepath.Join(c.Cwd, "stackharbor.yaml"), Content: b, NeedsInput: needs})
	}
	return drafts
}
func WriteDrafts(root string, drafts []Draft) []model.Diagnostic {
	ds := []model.Diagnostic{}
	real, e := filepath.EvalSymlinks(root)
	if e != nil {
		return []model.Diagnostic{model.Error(root, "init", e.Error())}
	}
	r, e := os.OpenRoot(real)
	if e != nil {
		return []model.Diagnostic{model.Error(root, "init", e.Error())}
	}
	defer r.Close()
	for _, d := range drafts {
		if len(d.NeedsInput) > 0 {
			ds = append(ds, model.Diagnostic{Severity: "warning", Code: "needs_input", File: d.Path, Message: fmt.Sprint(d.NeedsInput)})
			continue
		}
		absolute, e := filepath.Abs(d.Path)
		if e != nil {
			ds = append(ds, model.Error(d.Path, "init", "draft must remain within root"))
			continue
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(absolute))
		if e != nil || !config.Within(real, parent) {
			ds = append(ds, model.Error(d.Path, "init", "draft parent escapes root"))
			continue
		}
		absolute = filepath.Join(parent, filepath.Base(absolute))
		rel, _ := filepath.Rel(real, absolute)
		if _, e = r.Lstat(rel); e == nil || !os.IsNotExist(e) {
			ds = append(ds, model.Error(d.Path, "init", "target exists or inaccessible; refusing overwrite"))
			continue
		}
		b := make([]byte, 16)
		if _, e = rand.Read(b); e != nil {
			ds = append(ds, model.Error(d.Path, "init", e.Error()))
			continue
		}
		tmp := filepath.Join(filepath.Dir(rel), fmt.Sprintf(".stackharbor-%x.tmp", b))
		f, e := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			ds = append(ds, model.Error(d.Path, "init", e.Error()))
			continue
		}
		_, writeErr := f.Write(d.Content)
		closeErr := f.Close()
		if writeErr == nil {
			writeErr = closeErr
		}
		if writeErr == nil {
			writeErr = r.Link(tmp, rel)
		}
		_ = r.Remove(tmp)
		if writeErr != nil {
			ds = append(ds, model.Error(d.Path, "init", writeErr.Error()))
		} else {
			ds = append(ds, model.Diagnostic{Severity: "info", Code: "written", File: d.Path, Message: "registration written"})
		}
	}
	return ds
}
