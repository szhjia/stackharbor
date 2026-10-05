package config

import (
	"bytes"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
	"strings"
)

type WorkspaceConfig struct {
	Resources     map[string]model.ResourceSpec `yaml:"resources"`
	Root          string                        `yaml:"root"`
	Discover      bool                          `yaml:"discover"`
	Exclude       []string                      `yaml:"exclude"`
	Registrations []string                      `yaml:"registrations"`
	RootExplicit  bool                          `yaml:"-"`
	Version       int                           `yaml:"version"`
}
type projectFile struct {
	Context contextFile         `yaml:"context"`
	Tasks   map[string]taskFile `yaml:"tasks"`
	Version int                 `yaml:"version"`
	Project struct {
		ID   string `yaml:"id"`
		Name string `yaml:"name"`
	} `yaml:"project"`
	Services map[string]serviceFile `yaml:"services"`
}
type serviceFile struct {
	Identity *model.ObservedIdentity `yaml:"identity"`
	Requires []model.Requirement     `yaml:"requires"`
	Control  string                  `yaml:"control"`
	Name     string                  `yaml:"name"`
	Cwd      string                  `yaml:"cwd"`
	Run      struct {
		Command []string `yaml:"command"`
	} `yaml:"run"`
	Env             map[string]string `yaml:"env"`
	Ports           []model.Port      `yaml:"ports"`
	Ready           *model.ReadyProbe `yaml:"ready"`
	DependsOn       []model.ServiceID `yaml:"depends_on"`
	DockerDependsOn []string          `yaml:"docker_depends_on"`
	Stop            model.StopPolicy  `yaml:"stop"`
	Open            string            `yaml:"open"`
}
type contextFile struct {
	Cwd      string            `yaml:"cwd"`
	EnvFiles []string          `yaml:"env_files"`
	Env      map[string]string `yaml:"env"`
}
type taskFile struct {
	Name string            `yaml:"name"`
	Cwd  string            `yaml:"cwd"`
	Env  map[string]string `yaml:"env"`
	Run  struct {
		Command []string `yaml:"command"`
	} `yaml:"run"`
	Requires       []model.Requirement `yaml:"requires"`
	model.TaskSpec `yaml:",inline"`
}

func decode(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 256*1024+1))
	if err != nil {
		return err
	}
	if len(b) > 256*1024 {
		return fmt.Errorf("file exceeds 256 KiB")
	}
	var node yaml.Node
	d := yaml.NewDecoder(bytes.NewReader(b))
	if err = d.Decode(&node); err != nil {
		return err
	}
	if err = checkNode(&node); err != nil {
		return err
	}
	var extra yaml.Node
	if err = d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("exactly one YAML document required")
	}
	d = yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	return d.Decode(out)
}
func checkNode(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode || n.Tag == "!!merge" {
		return fmt.Errorf("aliases and merges are unsupported at line %d", n.Line)
	}
	if n.Tag != "" && !strings.HasPrefix(n.Tag, "!!") {
		return fmt.Errorf("custom tag at line %d", n.Line)
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if seen[k.Value] {
				return fmt.Errorf("duplicate key %s at line %d", k.Value, k.Line)
			}
			seen[k.Value] = true
			v := n.Content[i+1]
			if k.Value == "command" && v.Kind == yaml.SequenceNode {
				for _, arg := range v.Content {
					if arg.Tag != "!!str" {
						return fmt.Errorf("command arguments must be strings")
					}
				}
			}
			if k.Value == "env" && v.Kind == yaml.MappingNode {
				for j := 1; j < len(v.Content); j += 2 {
					if v.Content[j].Tag != "!!str" {
						return fmt.Errorf("env values must be strings")
					}
				}
			}
		}
	}
	for _, c := range n.Content {
		if err := checkNode(c); err != nil {
			return err
		}
	}
	return nil
}
func LoadProject(path, root string) (model.Project, []model.Diagnostic) {
	var f projectFile
	if err := decode(path, &f); err != nil {
		return model.Project{}, []model.Diagnostic{model.Error(path, "document", err.Error())}
	}
	if f.Version == 2 {
		return validateV2(path, root, f)
	}
	if len(f.Tasks) > 0 || f.Context.Cwd != "" || len(f.Context.EnvFiles) > 0 || len(f.Context.Env) > 0 {
		return model.Project{}, []model.Diagnostic{model.Error(path, "version", "v2 fields require version 2")}
	}
	for _, v := range f.Services {
		if len(v.Requires) > 0 || v.Control != "" || v.Identity != nil {
			return model.Project{}, []model.Diagnostic{model.Error(path, "version", "v2 fields require version 2")}
		}
	}
	return validateProject(path, root, f)
}
func LoadWorkspace(path string) (WorkspaceConfig, []model.Diagnostic) {
	w := WorkspaceConfig{Discover: true}
	if err := decode(path, &w); err != nil {
		return w, []model.Diagnostic{model.Error(path, "workspace", err.Error())}
	}
	w.RootExplicit = w.Root != ""
	if w.Version != 1 && w.Version != 2 {
		return w, []model.Diagnostic{model.Error(path, "version", "version must be 1 or 2")}
	}
	if w.Version == 1 && len(w.Resources) > 0 {
		return w, []model.Diagnostic{model.Error(path, "resources", "resources require version 2")}
	}
	for _, p := range w.Exclude {
		if p == "" || !relativeSafe(p) {
			return w, []model.Diagnostic{model.Error(path, "exclude", "exclude must be relative and cannot contain ..")}
		}
	}
	return w, nil
}
