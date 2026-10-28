package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Config-only integration uses the real Compose parser without contacting the
// daemon or starting containers.
func TestRealComposeProjectNameRegistration(t *testing.T) {
	if os.Getenv("STACKHARBOR_COMPOSE_INTEGRATION") != "1" {
		t.Skip("set STACKHARBOR_COMPOSE_INTEGRATION=1 for config-only integration")
	}
	for _, name := range []string{"20years", "123", "demo-project_2"} {
		t.Run(name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			put(t, root, "compose.yaml", "services:\n  db:\n    image: postgres:17\n")
			path := put(t, root, "stackharbor.workspace.yaml", fmt.Sprintf("version: 2\nresources:\n  db:\n    adapter: compose\n    file: compose.yaml\n    project: %q\n    service: db\n    available: running\n    control: managed\n    lifetime: persistent\n", name))
			workspace, ds := LoadWorkspace(path)
			if len(ds) != 0 {
				t.Fatal(ds)
			}
			project, ds := ResourceProject(path, root, workspace)
			if len(ds) != 0 || len(project.Services) != 1 {
				t.Fatalf("real Compose registration failed: %+v %v", project, ds)
			}
			resource := project.Services[0]
			digest, _ := resource.Resource.ComposeEvidence()
			if resource.ID != "resource/db" || resource.Resource.Project != name || digest == "" {
				t.Fatalf("registration lost identity or resolved evidence: %+v", resource)
			}
		})
	}
}

func TestRealComposeProfilesKeepDefaultRegistration(t *testing.T) {
	if os.Getenv("STACKHARBOR_COMPOSE_INTEGRATION") != "1" {
		t.Skip("set STACKHARBOR_COMPOSE_INTEGRATION=1 for config-only integration")
	}
	t.Setenv("COMPOSE_PROFILES", "")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "compose.yaml", "services:\n  db: {image: redis:7-alpine}\n  seed: {image: redis:7-alpine, profiles: [seed]}\n")
	for _, service := range []string{"db", "seed"} {
		t.Run(service, func(t *testing.T) {
			path := put(t, root, "stackharbor.workspace.yaml", fmt.Sprintf("version: 2\nresources:\n  database:\n    adapter: compose\n    file: compose.yaml\n    project: profile-test\n    service: %s\n    available: running\n    control: managed\n    lifetime: persistent\n", service))
			workspace, ds := LoadWorkspace(path)
			if len(ds) != 0 {
				t.Fatal(ds)
			}
			project, ds := ResourceProject(path, root, workspace)
			if service == "db" {
				if len(ds) != 0 || len(project.Services) != 1 || project.Services[0].Resource.Service != "db" {
					t.Fatalf("unrelated profile blocks default resource: %+v %v", project, ds)
				}
			} else if len(ds) == 0 || len(project.Services) != 0 {
				t.Fatalf("inactive profile resource silently registered: %+v %v", project, ds)
			}
		})
	}
}
