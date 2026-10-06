package docker

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"path/filepath"
)

// ComposeScope captures the ordered inputs and execution directory of a project.
type ComposeScope struct {
	Files            []string
	ProjectDirectory string
	EnvFiles         []string
	Project          string
}

func (s ComposeScope) Key() string {
	b, _ := json.Marshal(s.Args())
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func (s ComposeScope) Args() []string {
	a := []string{"compose"}
	for _, f := range s.Files {
		a = append(a, "-f", f)
	}
	if s.ProjectDirectory != "" {
		a = append(a, "--project-directory", s.ProjectDirectory)
	}
	for _, f := range s.EnvFiles {
		a = append(a, "--env-file", f)
	}
	if s.Project != "" {
		a = append(a, "-p", s.Project)
	}
	return a
}
func ScopeFor(r model.ResourceSpec) ComposeScope {
	files := append([]string{}, r.Files...)
	if len(files) == 0 && r.File != "" {
		files = []string{r.File}
	}
	dir := r.ProjectDirectory
	if dir == "" && len(files) > 0 {
		dir = filepath.Dir(files[0])
	}
	return ComposeScope{Files: files, ProjectDirectory: dir, EnvFiles: append([]string{}, r.EnvFiles...), Project: r.Project}
}
