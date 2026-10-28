package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
)

func TestAllRestartRestoresActiveExcludedNativeDependents(t *testing.T) {
	for _, version := range []int{1, 2} {
		for _, backend := range []bool{false, true} {
			t.Run(fmt.Sprintf("v%d/backend=%t", version, backend), func(t *testing.T) {
				no := false
				root := service("app/root")
				middle := service("app/middle", root.ID)
				leaf := service("app/leaf", middle.ID)
				idle := service("app/idle", root.ID)
				unrelated := service("app/unrelated")
				observer := service("app/observer", root.ID)
				observer.Control = "observe"
				nodes := []model.Service{root, middle, leaf, idle, unrelated, observer}
				for i := range nodes {
					nodes[i].Kind, nodes[i].Version = "service", version
					if nodes[i].ID != root.ID {
						nodes[i].AllActions = &no
					}
				}
				t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
				f := &fakeRunner{starts: map[model.ServiceID]int{}, handles: map[model.ServiceID]*fakeHandle{}}
				s, err := newManuallySampledSession(model.Workspace{Root: t.TempDir(), Version: version, Projects: []model.Project{{ID: "app", Services: nodes}}}, f, &countingPorts{}, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
				if err := s.Start(context.Background(), []model.ServiceID{leaf.ID, unrelated.ID}); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				s.mu.Lock()
				s.entries[observer.ID].state = "waiting"
				s.entries[observer.ID].cancel = cancel
				unrelatedGen := s.entries[unrelated.ID].gen
				s.mu.Unlock()
				b := NewControlBackend(s, control.Identity{})
				req := control.PlanRequest{Action: "all-restart"}
				plan, err := b.PlanState(context.Background(), req)
				want := []string{"app/leaf", "app/middle", "app/root"}
				if err != nil || !slices.Equal(plan.Affected, want) {
					t.Fatalf("restart plan: affected=%v err=%v", plan.Affected, err)
				}
				f.mu.Lock()
				plannedWrites := len(f.stops)
				for _, count := range f.starts {
					if count != 1 {
						t.Error("planning started a service", f.starts)
					}
				}
				f.mu.Unlock()
				if plannedWrites != 0 {
					t.Fatal("planning stopped a service")
				}
				if backend {
					results, err := b.Execute(context.Background(), req, plan)
					if err != nil || len(results) != len(want) {
						t.Fatalf("restart execution: %+v %v", results, err)
					}
					for i, result := range results {
						if result.Target != want[i] || result.State != "succeeded" || result.Error != nil {
							t.Fatalf("restart results differ from plan: %+v", results)
						}
					}
				} else if err := s.AllAction(context.Background(), "restart"); err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if !slices.Equal(f.stops, []string{"app/leaf", "app/middle", "app/root"}) {
					t.Errorf("wrong stop order or unrelated node stopped: %v", f.stops)
				}
				for _, id := range []model.ServiceID{root.ID, middle.ID, leaf.ID} {
					if f.starts[id] != 2 {
						t.Errorf("active downstream %s not restored: starts=%v", id, f.starts)
					}
				}
				if f.starts[idle.ID] != 0 || f.starts[unrelated.ID] != 1 || f.starts[observer.ID] != 0 {
					t.Errorf("restart expanded scope: %v", f.starts)
				}
				s.mu.Lock()
				defer s.mu.Unlock()
				for _, id := range []model.ServiceID{root.ID, middle.ID, leaf.ID} {
					if !targetSucceeded("all-restart", s.entries[id].state) {
						t.Errorf("restart succeeded with %s left %s", id, s.entries[id].state)
					}
				}
				if s.entries[unrelated.ID].gen != unrelatedGen || s.entries[observer.ID].gen != 0 || s.entries[observer.ID].state != "waiting" || ctx.Err() != nil {
					t.Error("unrelated service or observe-only lifecycle changed")
				}
			})
		}
	}
}

func TestControlAllRestartMixedResourceActions(t *testing.T) {
	cases := []struct {
		name                           string
		rootAll, childAll, childActive bool
		failRestore                    bool
	}{
		{"root-only/active-child", true, false, true, false},
		{"root-only/idle-child", true, false, false, false},
		{"child-only/active-child", false, true, true, false},
		{"child-only/idle-child", false, true, false, false},
		{"both/active-child", true, true, true, false},
		{"both/idle-child", true, true, false, false},
		{"neither/active-child", false, false, true, false},
		{"neither/idle-child", false, false, false, false},
		{"root-only/restore-fails", true, false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, calls := readonlyResourceSession(t, "existing-container")
			root := filepath.Dir(calls)
			s.mu.Lock()
			s.entries["resource/db"].spec.Resource.AllActions = tc.rootAll
			s.entries["resource/managed"].spec.Resource.AllActions = tc.childAll
			if !tc.childActive {
				s.entries["resource/managed"].state = "stopped"
				s.entries["resource/managed"].resourceRunning = false
			}
			observerGen := s.entries["resource/observer"].gen
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.entries["resource/observer"].cancel = cancel
			s.mu.Unlock()
			if !tc.childActive {
				if err := os.WriteFile(filepath.Join(root, "managed.state"), []byte("exited"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.failRestore {
				path := filepath.Join(root, "docker")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				script := strings.Replace(string(raw), " case \"$1\" in stop)", " if [ \"$1\" = start ] && [ \"$2\" = managed ]; then exit 1; fi\n case \"$1\" in stop)", 1)
				if script == string(raw) {
					t.Fatal("restore failure injection missing")
				}
				if err := os.WriteFile(path, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
			}
			childAffected := tc.childActive && (tc.rootAll || tc.childAll)
			want := []string{}
			if tc.rootAll || childAffected {
				want = append(want, "resource/db")
			}
			if childAffected {
				want = append(want, "resource/managed")
			}
			b := NewControlBackend(s, control.Identity{})
			req := control.PlanRequest{Action: "all-restart"}
			plan, err := b.PlanState(context.Background(), req)
			if err != nil || !slices.Equal(plan.Affected, want) {
				t.Fatalf("mixed all_actions plan: affected=%v want=%v err=%v", plan.Affected, want, err)
			}
			if raw, err := os.ReadFile(calls); err != nil || len(raw) != 0 {
				t.Fatalf("planning mutated a resource: %q %v", raw, err)
			}
			results, err := b.Execute(context.Background(), req, plan)
			if tc.failRestore {
				var api *control.APIError
				if !errors.As(err, &api) || api.Code != "execution_failed" {
					t.Fatalf("restoration failure reported as success: %+v %v", results, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(results) != len(want) {
				t.Fatalf("results differ from plan: %+v", results)
			}
			for i, result := range results {
				state := "succeeded"
				if tc.failRestore && result.Target == "resource/managed" {
					state = "failed"
				}
				if result.Target != want[i] || result.State != state || (result.Error != nil) != (state == "failed") {
					t.Errorf("incorrect result: %+v; plan=%v", result, want)
				}
			}
			commands := ""
			if childAffected {
				commands += "stop managed\n"
			}
			if tc.rootAll {
				commands += "stop db\nstart db\n"
			}
			if childAffected {
				commands += "start managed\n"
			}
			if raw, err := os.ReadFile(calls); err != nil || string(raw) != commands {
				t.Errorf("wrong physical actions: %q want=%q err=%v", raw, commands, err)
			}
			physicalState := "exited"
			if tc.childActive && !tc.failRestore {
				physicalState = "running"
			}
			if raw, err := os.ReadFile(filepath.Join(root, "managed.state")); err != nil || string(raw) != physicalState {
				t.Errorf("wrong downstream container state: %q %v", raw, err)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			state := "stopped"
			if tc.failRestore {
				state = "failed"
			} else if tc.childActive {
				state = "available"
			}
			if s.entries["resource/db"].state != "available" || s.entries["resource/managed"].state != state {
				t.Errorf("reported outcome differs from session state: db=%s managed=%s", s.entries["resource/db"].state, s.entries["resource/managed"].state)
			}
			observer := s.entries["resource/observer"]
			if observer.gen != observerGen || observer.operationID != "" || observer.state != "available" || ctx.Err() != nil {
				t.Error("observe-only resource was controlled")
			}
		})
	}
}
