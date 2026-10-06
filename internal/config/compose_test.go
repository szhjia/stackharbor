package config

import (
	"github.com/szhjia/stackharbor/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeScopeNormalization(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	base := filepath.Join(root, "base.yml")
	os.WriteFile(base, []byte("services: {}"), 0600)
	path := filepath.Join(root, "workspace.yaml")
	a, ds := normalizeComposeScope(path, root, model.ResourceSpec{File: "base.yml", Project: "demo"})
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	b, ds := normalizeComposeScope(path, root, model.ResourceSpec{Files: []string{"base.yml"}, Project: "demo"})
	if len(ds) > 0 || a.Key() != b.Key() || b.ProjectDirectory != root {
		t.Fatal("normalization mismatch")
	}
	outside := filepath.Join(t.TempDir(), "outside.yml")
	os.WriteFile(outside, []byte("services: {}"), 0600)
	os.Symlink(outside, filepath.Join(root, "link.yml"))
	for _, r := range []model.ResourceSpec{{}, {File: "base.yml", Files: []string{}}, {Files: []string{"base.yml", "base.yml"}}, {Files: []string{"missing"}}, {File: "."}, {File: "link.yml"}, {File: "base.yml", ProjectDirectory: "missing"}, {File: "base.yml", EnvFiles: []string{"missing"}}} {
		if _, ds := normalizeComposeScope(path, root, r); len(ds) == 0 {
			t.Errorf("accepted invalid scope %#v", r)
		}
	}
}

func TestComposeNormalizedDependencies(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	path := filepath.Join(root, "workspace.yaml")
	base := filepath.Join(root, "base.yml")
	override := filepath.Join(root, "override.yml")
	os.WriteFile(base, []byte("services: {db: {image: postgres}, api: {image: app}}"), 0600)
	os.WriteFile(override, []byte("services: {api: {depends_on: [db]}}"), 0600)
	result := filepath.Join(root, "result")
	os.WriteFile(filepath.Join(root, "docker"), []byte("#!/bin/sh\ncat '"+result+"'\n"), 0700)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	db := model.ResourceSpec{Adapter: "compose", Files: []string{"base.yml", "override.yml"}, Project: "demo", Service: "db", Available: "healthy", Lifetime: "persistent", Control: "managed"}
	api := db
	api.Service = "api"
	api.Available = "running"
	c := WorkspaceConfig{Resources: map[string]model.ResourceSpec{"db": db, "api": api}}
	good := `{"services":{"db":{"image":"postgres","healthcheck":{"test":["CMD","true"]}},"api":{"image":"app","depends_on":{"db":{"condition":"service_healthy","required":true,"restart":false}}}}}`
	os.WriteFile(result, []byte(good), 0600)
	p, ds := ResourceProject(path, root, c)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if len(p.Services[0].DependsOn) != 1 || p.Services[0].DependsOn[0] != "resource/db" {
		t.Fatal("merged dependency missing")
	}
	delete(c.Resources, "db")
	if _, ds = ResourceProject(path, root, c); len(ds) == 0 {
		t.Fatal("unregistered dependency accepted")
	}
	c.Resources["db"] = db
	for _, bad := range []string{strings.Replace(good, `"required":true`, `"required":false`, 1), strings.Replace(good, `"restart":false`, `"restart":true`, 1), strings.Replace(good, `"service_healthy"`, `"service_completed_successfully"`, 1), strings.Replace(good, `"test":["CMD","true"]`, `"disable":true`, 1)} {
		os.WriteFile(result, []byte(bad), 0600)
		if _, ds = ResourceProject(path, root, c); len(ds) == 0 {
			t.Fatal("invalid normalized contract accepted")
		}
	}
}

func TestComposeScopeRejectsBothYAMLFields(t *testing.T) {
	for _, file := range []string{`""`, `null`} {
		root := t.TempDir()
		p := filepath.Join(root, "workspace.yaml")
		os.WriteFile(p, []byte("version: 2\nresources:\n  db:\n    adapter: compose\n    file: "+file+"\n    files: [base.yml]\n"), 0600)
		if _, ds := LoadWorkspace(p); len(ds) == 0 {
			t.Fatal("both file keys accepted")
		}
	}
}
