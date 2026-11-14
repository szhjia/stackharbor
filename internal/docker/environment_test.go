package docker

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEnvironmentRefreshUsesNewWorkspaceDockerPath(t *testing.T) {
	root := t.TempDir()
	dirs := []string{filepath.Join(root, "one"), filepath.Join(root, "two")}
	for i, dir := range dirs {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		name := []string{"one", "two"}[i]
		script := "#!/bin/sh\nprintf '\"" + name + "-%s\"' \"$MARKER\"\n"
		if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	manager := NewScoped(root, "", "fixture")
	manager.SetEnvironment([]string{"PATH=" + dirs[0], "MARKER=old"})
	before, err := manager.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next := []string{"PATH=" + dirs[1], "MARKER=new"}
	if err := manager.ValidateEnvironment(next, false); err != nil {
		t.Fatal(err)
	}
	manager.SetEnvironment(next)
	after, err := manager.Identity(context.Background())
	if err != nil || before.Endpoint == after.Endpoint {
		t.Fatalf("new PATH/environment were not used: before=%+v after=%+v err=%v", before, after, err)
	}
	if os.Getenv("MARKER") == "old" || os.Getenv("MARKER") == "new" {
		t.Fatal("manager changed the process environment")
	}
}

func TestOwnedEnvironmentRejectsTargetAndSecurityChangesWithoutLosingPins(t *testing.T) {
	for _, key := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "HOME", "SSH_AUTH_SOCK"} {
		t.Run(key, func(t *testing.T) {
			manager := NewAdopted(t.TempDir(), "fixture", []string{"db"})
			manager.SetEnvironment([]string{"PATH=/old/bin", key + "=previous-secret"})
			manager.endpoint = "pinned-endpoint"
			manager.pinned = map[string]string{"db": strings.Repeat("a", 64)}
			previous := append([]string{}, manager.env...)
			pins := map[string]string{"db": manager.pinned["db"]}
			err := manager.ValidateEnvironment([]string{"PATH=/new/bin", key + "=replacement-secret"}, false)
			if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("target change not refused safely: %v", err)
			}
			if !reflect.DeepEqual(previous, manager.env) || !reflect.DeepEqual(pins, manager.pinned) || manager.endpoint != "pinned-endpoint" {
				t.Fatal("validation changed the frozen environment or pinned identities")
			}
			if err := manager.ValidateEnvironment([]string{"PATH=/new/bin", key + "=previous-secret"}, true); err != nil {
				t.Fatalf("toolchain-only refresh refused: %v", err)
			}
		})
	}
	manager := NewScoped(t.TempDir(), "", "fixture")
	manager.SetEnvironment([]string{"DOCKER_HOST=unix:///old.sock"})
	if err := manager.ValidateEnvironment([]string{"DOCKER_HOST=unix:///new.sock"}, true); err == nil {
		t.Fatal("owned, unpinned manager accepted a different daemon")
	}
	if err := manager.ValidateEnvironment([]string{"DOCKER_HOST=unix:///new.sock"}, false); err != nil {
		t.Fatalf("unused manager cannot refresh its target: %v", err)
	}
}
