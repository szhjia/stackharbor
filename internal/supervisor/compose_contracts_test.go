package supervisor

import (
	"context"
	"encoding/json"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/docker"
	"github.com/szhjia/stackharbor/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func multiFileSession(t *testing.T) (*Session, string, string) {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, "base.yml")
	over := filepath.Join(root, "local.yml")
	result := filepath.Join(root, "result")
	calls := filepath.Join(root, "calls")
	os.WriteFile(base, []byte("services: {db: {image: postgres}}"), 0600)
	os.WriteFile(over, []byte("services: {db: {image: postgres}}"), 0600)
	os.WriteFile(result, []byte(`{"services":{"db":{"image":"postgres","environment":{"PASSWORD":"secret-sentinel"}}}}`), 0600)
	script := `#!/bin/sh
case "$1" in info) printf '"test-daemon"';exit;;stats) exit;;esac
case "$*" in *' config '*) cat "` + result + `";exit;; *' ps '*) printf '[{"ID":"db-id","Service":"db","State":"running"}]';exit;; esac
printf '%s\n' "$*" >> "` + calls + `"
`
	os.WriteFile(filepath.Join(root, "docker"), []byte(script), 0700)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	r := &model.ResourceSpec{Files: []string{base, over}, ProjectDirectory: root, Project: "fixture", Service: "db", Available: "running", Control: "managed", Lifetime: "session"}
	resolved, e := docker.Resolve(context.Background(), docker.ScopeFor(*r), os.Environ())
	if e != nil {
		t.Fatal(e)
	}
	r.SetComposeEvidence(resolved.Digest, os.Environ())
	n := model.Service{ID: "resource/db", ProjectID: "infra", Kind: "resource", Resource: r, InputFiles: []string{base, over}}
	s, e := newManuallySampledSession(model.Workspace{Root: root, Version: 2, Projects: []model.Project{{ID: "infra", Services: []model.Service{n, service("app/local")}}}}, &fakeRunner{starts: map[model.ServiceID]int{}, handles: map[model.ServiceID]*fakeHandle{}}, &countingPorts{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	return s, result, calls
}
func TestMultiFileRuntimeScope(t *testing.T) {
	s, _, calls := multiFileSession(t)
	m := s.resources["resource/db"]
	if err := m.Action(context.Background(), "start", []string{"db"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	if strings.Count(string(b), "-f ") != 2 || !strings.Contains(string(b), "--project-directory") || !strings.Contains(string(b), "up -d --no-deps --no-build --wait") {
		t.Fatal("scope or bounded start lost")
	}
}
func TestComposeDriftBlocksMutation(t *testing.T) {
	s, result, calls := multiFileSession(t)
	b := NewControlBackend(s, control.Identity{})
	req := control.PlanRequest{Action: "start", Targets: []string{"resource/db"}}
	plan, e := b.PlanState(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(result, []byte(`{"services":{"db":{"image":"postgres:changed"}}}`), 0600)
	if _, e = b.Execute(context.Background(), req, plan); e == nil {
		t.Fatal("changed model accepted")
	}
	for _, action := range []string{"start", "stop", "restart"} {
		if e = s.resources["resource/db"].Action(context.Background(), action, []string{"db"}); e == nil {
			t.Fatalf("%s accepted drift", action)
		}
	}
	raw, _ := os.ReadFile(calls)
	if len(raw) > 0 {
		t.Fatal("drift mutated resource")
	}
}
func TestComposeSecretNotSerialized(t *testing.T) {
	s, _, _ := multiFileSession(t)
	b, e := json.Marshal(s.Snapshot())
	if e != nil || strings.Contains(string(b), "secret-sentinel") {
		t.Fatal("model leaked")
	}
}

func TestComposeDriftCleanup(t *testing.T) {
	s, result, calls := multiFileSession(t)
	if err := s.Start(context.Background(), []model.ServiceID{"app/local"}); err != nil {
		t.Fatal(err)
	}
	s.sample(context.Background())
	os.WriteFile(result, []byte(`{"services":{"db":{"image":"changed"}}}`), 0600)
	if e := s.Cleanup(context.Background()); e == nil {
		t.Fatal("cleanup silently accepted changed resource")
	}
	if s.entries["app/local"].state != "stopped" {
		t.Fatal("local cleanup blocked by Compose drift")
	}
	raw, _ := os.ReadFile(calls)
	if len(raw) > 0 {
		t.Fatal("cleanup mutated changed resource")
	}
}
