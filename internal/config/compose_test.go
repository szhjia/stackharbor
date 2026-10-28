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

func TestExistingContainerRegistrationUsesExplicitDependenciesWithoutCompose(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "stackharbor.workspace.yaml")
	db := model.ResourceSpec{Adapter: "existing-container", Project: "wordverse", Service: "db", Available: "healthy", Control: "managed", Lifetime: "persistent"}
	api := model.ResourceSpec{Adapter: "existing-container", Project: "wordverse", Service: "api", Available: "healthy", Control: "managed", Lifetime: "persistent", AllActions: true, Requires: []model.Requirement{{Node: "resource/database", Condition: "available"}}}
	c := WorkspaceConfig{Resources: map[string]model.ResourceSpec{"database": db, "api": api}}
	p, ds := ResourceProject(path, root, c)
	if len(ds) != 0 || len(p.Services) != 2 {
		t.Fatalf("valid adopted resources rejected: %v", ds)
	}
	for _, n := range p.Services {
		if n.ID == "resource/api" && (len(n.DependsOn) != 1 || n.DependsOn[0] != "resource/database" || !n.Resource.AllActions) {
			t.Fatalf("adopted dependency or all_actions lost: %+v", n)
		}
	}
	api.File = "docker-compose.yml"
	c.Resources["api"] = api
	if _, ds := ResourceProject(path, root, c); len(ds) == 0 {
		t.Fatal("adopted resource accepted Compose file")
	}
	api.File = ""
	api.Requires[0].Node = "resource/missing"
	c.Resources["api"] = api
	if _, ds := ResourceProject(path, root, c); len(ds) == 0 {
		t.Fatal("missing adopted dependency accepted")
	}
}

func TestResourceProjectComposeProjectNames(t *testing.T) {
	for _, adapter := range []string{"compose", "existing-container"} {
		for _, tc := range []struct {
			name  string
			valid bool
		}{
			{"20years", true}, {"0", true}, {"123", true}, {"demo", true}, {"demo-project_2", true},
			{"", false}, {"-demo", false}, {"_demo", false}, {"Demo", false},
			{"demo.project", false}, {"demo project", false}, {"demo/name", false}, {"demo\n", false}, {"项目", false},
		} {
			t.Run(adapter+"/"+tc.name, func(t *testing.T) {
				root, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				put(t, root, "base.yml", "services: {db: {image: postgres:17}}")
				put(t, root, "docker", "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SH_PROJECT_ARGS_LOG\"\nprintf '%s\\n' '{\"services\":{\"db\":{\"image\":\"postgres:17\"}}}'\n")
				if err := os.Chmod(filepath.Join(root, "docker"), 0700); err != nil {
					t.Fatal(err)
				}
				argsPath := filepath.Join(root, "docker-args")
				t.Setenv("SH_PROJECT_ARGS_LOG", argsPath)
				t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
				r := model.ResourceSpec{Adapter: adapter, Project: tc.name, Service: "db", Available: "running", Control: "managed", Lifetime: "persistent"}
				if adapter == "compose" {
					r.File = "base.yml"
				}
				project, ds := ResourceProject(filepath.Join(root, "workspace.yaml"), root, WorkspaceConfig{Resources: map[string]model.ResourceSpec{"db": r}})
				if !tc.valid {
					if len(ds) == 0 || len(project.Services) != 0 {
						t.Fatalf("invalid project %q accepted: %+v %v", tc.name, project, ds)
					}
				} else if len(ds) != 0 || len(project.Services) != 1 || project.Services[0].Resource.Project != tc.name {
					t.Fatalf("project name %q rejected or rewritten: %+v %v", tc.name, project, ds)
				}
				args, err := os.ReadFile(argsPath)
				if tc.valid && adapter == "compose" {
					if err != nil || !strings.Contains(string(args), "-p\n"+tc.name+"\n") {
						t.Fatalf("project name not passed intact to Compose: %q %v", args, err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("unexpected Docker invocation: %q %v", args, err)
				}
			})
		}
	}
}
