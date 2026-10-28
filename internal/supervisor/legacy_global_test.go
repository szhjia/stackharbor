package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/runner"
)

type failedRestoreRunner struct{ delegate *fakeRunner }

func (r failedRestoreRunner) Start(ctx context.Context, spec model.Service, out func(string, string)) (runner.Handle, error) {
	r.delegate.mu.Lock()
	fail := spec.ID == "app/side" && r.delegate.starts[spec.ID] > 0
	r.delegate.mu.Unlock()
	if fail {
		return nil, errors.New("fixture restore failed")
	}
	return r.delegate.Start(ctx, spec, out)
}

func TestLegacyGlobalActionsIncludeSharedDockerConsumers(t *testing.T) {
	for _, action := range []string{"all-stop", "all-restart", "restore-fails", "direct-restart"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			script := `#!/bin/sh
case "$1" in info) printf '"fixture-daemon"'; exit;; esac
case "$*" in *' config '*) printf '{"name":"fixture-project"}';exit;; esac
printf '%s\n' "$*" >> "$SH_LEGACY_CALLS"
case "$*" in *' ps '*) printf '[]';; esac
`
			for name, body := range map[string]string{"docker": script, "compose.yaml": "services:\n  db: {image: redis}\n  cache: {image: redis}\n"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SH_LEGACY_CALLS", filepath.Join(root, "calls"))
			t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
			no := false
			api, side, idle, observer := service("app/api"), service("app/side"), service("app/idle"), service("app/observer")
			api.DockerDependsOn = []string{"db"}
			side.DockerDependsOn = []string{"db", "cache"}
			idle.DockerDependsOn, observer.DockerDependsOn = []string{"db"}, []string{"db"}
			leaf := service("app/leaf", side.ID)
			observer.Control = "observe"
			nodes := []model.Service{api, side, leaf, idle, observer}
			for i := range nodes {
				if nodes[i].ID != api.ID {
					nodes[i].AllActions = &no
				}
			}
			f := &fakeRunner{starts: map[model.ServiceID]int{}, handles: map[model.ServiceID]*fakeHandle{}}
			s, err := newManuallySampledSession(model.Workspace{Root: root, Version: 1, Projects: []model.Project{{ID: "app", Services: nodes}}}, f, &countingPorts{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
			if err := s.Start(context.Background(), []model.ServiceID{api.ID, leaf.ID}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "calls"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.mu.Lock()
			s.entries[observer.ID].state, s.entries[observer.ID].cancel = "waiting", cancel
			s.mu.Unlock()
			if action == "restore-fails" {
				s.runner = failedRestoreRunner{f}
			}
			req := control.PlanRequest{Action: action}
			if action == "restore-fails" {
				req.Action = "all-restart"
			}
			b := NewControlBackend(s, control.Identity{})
			plan, err := b.PlanState(context.Background(), req)
			if action == "direct-restart" {
				req = control.PlanRequest{Action: "docker-restart", Targets: []string{"db"}}
				plan, err = b.PlanState(context.Background(), req)
			}
			want := []string{"app/api", "app/leaf", "app/side"}
			if action == "direct-restart" {
				want = append(want, "db")
			}
			if err != nil || !slices.Equal(plan.Affected, want) {
				t.Fatalf("missing shared consumers: %v want=%v err=%v", plan.Affected, want, err)
			}
			planned, err := os.ReadFile(filepath.Join(root, "calls"))
			if err != nil || strings.Contains(string(planned), " stop ") || strings.Contains(string(planned), " up ") {
				t.Fatalf("planning mutated Docker: %s %v", planned, err)
			}
			results, err := b.Execute(context.Background(), req, plan)
			if (err != nil) != (action == "restore-fails") {
				t.Fatalf("wrong overall result: %+v %v", results, err)
			}
			if len(results) != len(want) {
				t.Fatalf("results differ from plan: %+v", results)
			}
			for i, result := range results {
				state := "succeeded"
				if action == "restore-fails" && result.Target != "app/api" {
					state = "failed"
				}
				if result.Target != want[i] || result.State != state {
					t.Errorf("unexpected target result: %+v", result)
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if !slices.Contains(f.stops, "app/side") || !slices.Contains(f.stops, "app/leaf") || f.starts[idle.ID] != 0 {
				t.Errorf("incorrect stop/start scope: %v %v", f.stops, f.starts)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, id := range []model.ServiceID{api.ID, side.ID, leaf.ID} {
				wantState := "running"
				if action == "all-stop" {
					wantState = "stopped"
				} else if action == "restore-fails" && id != api.ID {
					wantState = "failed"
					if id == leaf.ID {
						wantState = "blocked"
					}
				}
				if s.entries[id].state != wantState {
					t.Errorf("%s left %s; want %s", id, s.entries[id].state, wantState)
				}
			}
			if ctx.Err() != nil || s.entries[observer.ID].gen != 0 || s.entries[observer.ID].state != "waiting" {
				t.Error("observe-only consumer changed")
			}
		})
	}
}
