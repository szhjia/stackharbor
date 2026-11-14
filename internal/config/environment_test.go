package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectEnvironmentComesFromCaller(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "stackharbor.yaml")
	_ = os.WriteFile(path, []byte("version: 2\nproject: {id: isolated, name: Isolated}\ncontext: {cwd: ., env: {OVERRIDE: yaml}}\nservices:\n  api: {run: {command: [tool]}, env: {SERVICE: local}}\n"), 0600)
	project, ds := LoadProjectWithEnvironment(path, root, []string{"PATH=/custom/bin", "OVERRIDE=caller", "ONLY_CALLER=yes"})
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	spec := project.Services[0]
	if !spec.EnvFrozen || spec.Env["PATH"] != "/custom/bin" || spec.Env["ONLY_CALLER"] != "yes" || spec.Env["OVERRIDE"] != "yaml" || spec.Env["SERVICE"] != "local" {
		t.Fatal("caller/YAML environment precedence wrong")
	}
}
