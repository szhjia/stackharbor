package discovery

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/pelletier/go-toml/v2"
	"github.com/szhjia/stackharbor/internal/model"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func boundedRead(path string) ([]byte, error) {
	st, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > 256*1024 {
		return nil, fmt.Errorf("manifest must be regular and at most 256 KiB")
	}
	return os.ReadFile(path)
}
func candidate(root, dir string) (*model.Candidate, []model.Diagnostic) {
	ds := []model.Diagnostic{}
	rel, _ := filepath.Rel(root, dir)
	slug := regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(strings.ToLower(filepath.ToSlash(rel)), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" || slug[0] < 'a' || slug[0] > 'z' {
		slug = "app-" + slug
	}
	hash := sha256.Sum256([]byte(rel))
	id := fmt.Sprintf("%s-%x", slug, hash[:3])
	p := filepath.Join(dir, "package.json")
	if b, e := boundedRead(p); e == nil {
		var pkg struct {
			Name           string
			Scripts        map[string]string
			PackageManager string
		}
		if e = json.Unmarshal(b, &pkg); e != nil {
			return nil, []model.Diagnostic{model.Error(p, "manifest", e.Error())}
		}
		script := ""
		if pkg.Scripts["dev"] != "" {
			script = "dev"
		} else if pkg.Scripts["start"] != "" {
			script = "start"
		}
		if script != "" {
			ok, e := memberOfWorkspace(root, dir)
			if e != nil {
				return nil, []model.Diagnostic{model.Error(p, "workspace", e.Error())}
			}
			if !ok {
				return nil, nil
			}
			name := pkg.Name
			if name == "" {
				name = filepath.Base(dir)
			}
			c := &model.Candidate{ID: id, Name: name, Cwd: dir, Kind: "javascript"}
			manager := managerFor(root, dir)
			if manager == "" {
				c.NeedsInput = []string{"package manager"}
			} else {
				c.SuggestedCommand = []string{manager, script}
				if manager == "npm" {
					c.SuggestedCommand = []string{"npm", "run", script}
				}
			}
			return c, ds
		}
	} else if !os.IsNotExist(e) {
		ds = append(ds, model.Error(p, "manifest", e.Error()))
	}
	p = filepath.Join(dir, "pyproject.toml")
	if b, e := boundedRead(p); e == nil {
		var doc map[string]any
		if e = toml.Unmarshal(b, &doc); e != nil {
			return nil, []model.Diagnostic{model.Error(p, "manifest", e.Error())}
		}
		return &model.Candidate{ID: id, Name: filepath.Base(dir), Cwd: dir, Kind: "python", NeedsInput: []string{"startup command"}}, ds
	} else if !os.IsNotExist(e) {
		ds = append(ds, model.Error(p, "manifest", e.Error()))
	}
	return nil, ds
}
func managerFor(root, dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if b, e := boundedRead(filepath.Join(d, "package.json")); e == nil {
			var p struct{ PackageManager string }
			if json.Unmarshal(b, &p) == nil {
				m := strings.Split(p.PackageManager, "@")[0]
				if m == "pnpm" || m == "npm" || m == "yarn" || m == "bun" {
					return m
				}
			}
		}
		found := []string{}
		for file, m := range map[string]string{"pnpm-lock.yaml": "pnpm", "package-lock.json": "npm", "yarn.lock": "yarn", "bun.lock": "bun", "bun.lockb": "bun"} {
			if _, e := os.Stat(filepath.Join(d, file)); e == nil {
				found = append(found, m)
			}
		}
		if len(found) == 1 {
			return found[0]
		}
		if len(found) > 1 {
			return ""
		}
		if d == root || d == filepath.Dir(d) {
			break
		}
	}
	return ""
}
func memberOfWorkspace(root, dir string) (bool, error) {
	for d := dir; ; d = filepath.Dir(d) {
		p := filepath.Join(d, "pnpm-workspace.yaml")
		if b, e := boundedRead(p); e == nil {
			var w struct {
				Packages []string `yaml:"packages"`
			}
			if e = yaml.Unmarshal(b, &w); e != nil {
				return false, e
			}
			if dir == d || len(w.Packages) == 0 {
				return true, nil
			}
			rel, _ := filepath.Rel(d, dir)
			rel = filepath.ToSlash(rel)
			match := false
			for _, pattern := range w.Packages {
				neg := strings.HasPrefix(pattern, "!")
				if neg {
					pattern = pattern[1:]
				}
				yes, e := doublestar.Match(pattern, rel)
				if e != nil {
					return false, e
				}
				if yes {
					if neg {
						return false, nil
					}
					match = true
				}
			}
			return match, nil
		} else if !os.IsNotExist(e) {
			return false, e
		}
		if d == root || d == filepath.Dir(d) {
			break
		}
	}
	return true, nil
}
