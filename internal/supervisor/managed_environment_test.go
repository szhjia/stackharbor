package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/szhjia/stackharbor/internal/docker"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/runner"
)

type countedHandle struct{ identities []model.ProcessIdentity }

func (h countedHandle) Identities() []model.ProcessIdentity { return h.identities }
func (h countedHandle) Done() <-chan runner.ExitResult      { return nil }
func (h countedHandle) Stop(context.Context, model.StopPolicy) runner.StopResult {
	return runner.StopResult{Complete: true}
}

func TestOwnershipCountsKeepsUnhealthyAndUncertainResourcesBusy(t *testing.T) {
	for _, tc := range []struct {
		name, state, lifetime, control string
		running, mutation              bool
		instances                      []string
		want                           int
	}{
		{name: "unhealthy running", state: "unavailable", lifetime: "session", running: true, want: 1},
		{name: "failed mutation", state: "failed", lifetime: "session", mutation: true, want: 1},
		{name: "identity retained but observation unknown", state: "unknown", lifetime: "session", instances: []string{"daemon:id"}, want: 1},
		{name: "explicitly stopped", state: "stopped", lifetime: "session", instances: []string{"daemon:id"}},
		{name: "not started", state: "stopped", lifetime: "session"},
		{name: "persistent running", state: "available", lifetime: "persistent", running: true},
		{name: "observe running", state: "available", lifetime: "session", control: "observe", running: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := model.ProcessIdentity{PID: 100, CreatedMillis: 200}
			session := &Session{entries: map[model.ServiceID]*entry{
				"resource/db": {spec: model.Service{Resource: &model.ResourceSpec{Lifetime: tc.lifetime, Control: tc.control}}, state: tc.state, resourceRunning: tc.running, resourceMutation: tc.mutation, resourceInstances: tc.instances},
				"app/api":     {handle: countedHandle{[]model.ProcessIdentity{id}}, state: "failed"},
				"app/alias":   {handle: countedHandle{[]model.ProcessIdentity{id}}, state: "failed"},
			}}
			processes, resources := session.OwnershipCounts()
			if processes != 1 || resources != tc.want {
				t.Fatalf("physical ownership: processes=%d resources=%d, want 1/%d", processes, resources, tc.want)
			}
		})
	}
}

func reloadDockerFixture(t *testing.T) (*Session, model.Workspace, []string, []string, string) {
	t.Helper()
	root := t.TempDir()
	calls := filepath.Join(root, "calls")
	paths := []string{filepath.Join(root, "one"), filepath.Join(root, "two")}
	for i, path := range paths {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		script := `#!/bin/sh
printf '%s|%s|%s\n' '` + []string{"one", "two"}[i] + `' "$MARKER" "$DOCKER_HOST" >> "$CALLS"
case "$1" in
 info) printf '"fixture-daemon"';;
 ps) printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa db\n';;
 inspect) printf '[{"Id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","Config":{"Labels":{"com.docker.compose.project":"fixture","com.docker.compose.service":"db"}},"State":{"Status":"running"}}]';;
 *) exit 1;;
esac
`
		if err := os.WriteFile(filepath.Join(path, "docker"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	env := func(path, marker string) []string {
		return []string{"PATH=" + path, "MARKER=" + marker, "CALLS=" + calls, "DOCKER_HOST=unix:///same.sock"}
	}
	previous, next := env(paths[0], "old"), env(paths[1], "new")
	legacy := docker.NewScoped(root, "", "fixture")
	legacy.SetEnvironment(previous)
	adopted := docker.NewAdopted(root, "fixture", []string{"db"})
	adopted.SetEnvironment(previous)
	if err := adopted.Pin(context.Background()); err != nil {
		t.Fatal(err)
	}
	compose := docker.NewResolved(docker.ComposeScope{Project: "fixture", ProjectDirectory: root}, previous, &docker.ResolvedCompose{Services: map[string]docker.ComposeService{"db": {}}})
	resource := model.ResourceSpec{Adapter: "compose", Project: "fixture", Service: "db", Lifetime: "session", Control: "managed"}
	resource.SetComposeEvidence("fixture-digest", next)
	nodes := []model.Service{
		{ID: "app/api", ProjectID: "app", Cwd: root, EnvFrozen: true, Env: map[string]string{"MARKER": "new"}},
		{ID: "resource/adopted", ProjectID: "app", Cwd: root, Kind: "resource", Resource: &model.ResourceSpec{Adapter: "existing-container", Project: "fixture", Service: "db", Lifetime: "persistent", Control: "managed"}},
		{ID: "resource/compose", ProjectID: "app", Cwd: root, Kind: "resource", Resource: &resource},
	}
	w := model.Workspace{Root: root, Projects: []model.Project{{ID: "app", Services: nodes}}}
	s := &Session{workspace: w, docker: legacy, resources: map[model.ServiceID]*docker.Manager{"resource/adopted": adopted, "resource/compose": compose}, entries: map[model.ServiceID]*entry{}}
	for _, node := range nodes {
		old := node
		old.Env = map[string]string{"MARKER": "old"}
		s.entries[node.ID] = &entry{spec: old, state: "stopped"}
	}
	if err := os.WriteFile(calls, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return s, w, previous, next, calls
}

func assertDockerEnvironmentCalls(t *testing.T, s *Session, calls, want string) {
	t.Helper()
	if err := os.WriteFile(calls, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, manager := range []*docker.Manager{s.docker, s.resources["resource/adopted"], s.resources["resource/compose"]} {
		if _, err := manager.Identity(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(calls)
	if err != nil || string(raw) != strings.Repeat(want+"\n", 3) {
		t.Fatalf("Docker environment calls: %q err=%v", raw, err)
	}
}

func TestReloadEnvironmentRefreshesEveryDockerManagerWithoutReplacingPins(t *testing.T) {
	s, w, _, next, calls := reloadDockerFixture(t)
	if err := s.ReloadEnvironment(w, next); err != nil {
		t.Fatal(err)
	}
	assertDockerEnvironmentCalls(t, s, calls, "two|new|unix:///same.sock")
	if s.entries["app/api"].spec.Env["MARKER"] != "new" {
		t.Fatal("process environment did not refresh")
	}
	if _, err := s.resources["resource/adopted"].Binding(context.Background(), []string{"db"}); err != nil {
		t.Fatalf("reload lost adopted identity pins: %v", err)
	}
}

func TestReloadEnvironmentRefusesOwnedTargetChangeAtomically(t *testing.T) {
	s, w, previous, next, calls := reloadDockerFixture(t)
	unsafe := append([]string{}, next...)
	unsafe[len(unsafe)-1] = "DOCKER_HOST=unix:///other.sock"
	before := s.entries["app/api"].spec.Env
	err := s.ReloadEnvironment(w, unsafe)
	if err == nil || !strings.Contains(err.Error(), "DOCKER_HOST") {
		t.Fatalf("pinned daemon target change accepted: %v", err)
	}
	if !reflect.DeepEqual(before, s.entries["app/api"].spec.Env) {
		t.Fatal("rejected reload changed process environments")
	}
	assertDockerEnvironmentCalls(t, s, calls, "one|old|unix:///same.sock")
	if !strings.Contains(strings.Join(previous, "\n"), "MARKER=old") {
		t.Fatal("caller environment was mutated")
	}
}
