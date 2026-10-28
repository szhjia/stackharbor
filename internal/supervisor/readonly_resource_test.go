package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/graph"
	"github.com/szhjia/stackharbor/internal/model"
)

func TestAffectedPreservesObserveOnlyResources(t *testing.T) {
	for _, adapter := range []string{"compose", "existing-container"} {
		for _, state := range []string{"available", "unavailable", "waiting", "starting"} {
			t.Run(adapter+"/"+state, func(t *testing.T) {
				db := service("resource/db")
				managed := service("resource/managed", db.ID)
				managed.Kind = "resource"
				managed.Resource = &model.ResourceSpec{Adapter: adapter, Control: "managed"}
				observer := service("resource/observer", db.ID)
				observer.Kind = "resource"
				observer.Resource = &model.ResourceSpec{Adapter: adapter, Control: "observe"}
				localObserver := service("app/observer", db.ID)
				localObserver.Control = "observe"
				g, ds := graph.Build([]model.Service{db, managed, observer, localObserver})
				if len(ds) != 0 {
					t.Fatal(ds)
				}
				s := &Session{graph: g, entries: map[model.ServiceID]*entry{
					db.ID: {spec: db}, managed.ID: {spec: managed, state: "available", resourceRunning: true},
					observer.ID: {spec: observer, state: state, resourceRunning: true}, localObserver.ID: {spec: localObserver, state: "waiting"},
				}}
				if affected := s.Affected([]model.ServiceID{db.ID}); !slices.Equal(affected, []model.ServiceID{managed.ID}) {
					t.Fatalf("managed dependent lost or observe-only dependent included: %v", affected)
				}
			})
		}
	}
}

// The fixture exercises real manager commands, identity binding and physical
// locks, but its docker executable only reads and writes this test's temp files.
func readonlyResourceSession(t *testing.T, adapter string) (*Session, string) {
	t.Helper()
	root := t.TempDir()
	put := func(name, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	put("compose.yaml", "services:\n  db: {image: app}\n  managed: {image: app}\n  observer: {image: app}\n", 0600)
	for _, name := range []string{"db", "managed", "observer"} {
		put(name+".state", "running", 0600)
	}
	put("calls", "", 0600)
	put("docker", `#!/bin/sh
set -eu
id_for() {
 case "$1" in
 db) printf '%064d' 1;;
 managed) printf '%064d' 2;;
 observer) printf '%064d' 3;;
 *) exit 1;;
 esac
}
name_for() {
 for name in db managed observer; do
  if [ "$(id_for "$name")" = "$1" ]; then printf '%s' "$name"; return; fi
 done
 exit 1
}
mutate() {
 printf '%s %s\n' "$1" "$2" >> "$SH_READONLY_FIXTURE/calls"
 case "$1" in stop) state=exited;; start) state=running;; *) exit 1;; esac
 printf '%s' "$state" > "$SH_READONLY_FIXTURE/$2.state"
}
case "$1" in
 info) printf '"readonly-fixture-daemon"';;
 stats) exit 0;;
 ps)
  for name in db managed observer; do printf '%s %s\n' "$(id_for "$name")" "$name"; done;;
 inspect)
  shift; printf '['; separator=''
  for id in "$@"; do
   name=$(name_for "$id"); state=$(cat "$SH_READONLY_FIXTURE/$name.state")
   printf '%s{"Id":"%s","Name":"/%s","Config":{"Image":"app","Labels":{"com.docker.compose.project":"readonly-fixture","com.docker.compose.service":"%s"}},"State":{"Status":"%s"}}' "$separator" "$id" "$name" "$name" "$state"
   separator=','
  done
  printf ']';;
 container)
  action=$2; for last do :; done
  mutate "$action" "$(name_for "$last")";;
 compose)
  case " $* " in
   *' config '*) printf '{"name":"readonly-fixture","services":{"db":{"image":"app"},"managed":{"image":"app"},"observer":{"image":"app"}}}';;
   *' ps '*)
    printf '['; separator=''
    for name in db managed observer; do
     printf '%s{"ID":"%s","Service":"%s","State":"%s"}' "$separator" "$(id_for "$name")" "$name" "$(cat "$SH_READONLY_FIXTURE/$name.state")"
     separator=','
    done
    printf ']';;
   *' stop '*) for last do :; done; mutate stop "$last";;
   *' up '*) for last do :; done; mutate start "$last";;
   *) exit 1;;
  esac;;
 *) exit 1;;
esac
`, 0700)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SH_READONLY_FIXTURE", root)
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	nodes := []model.Service{}
	for _, name := range []string{"db", "managed", "observer"} {
		r := &model.ResourceSpec{Adapter: adapter, Project: "readonly-fixture", Service: name, Available: "running", Control: "managed", Lifetime: "persistent", AllActions: adapter == "existing-container" && name != "observer"}
		if adapter == "compose" {
			r.File = filepath.Join(root, "compose.yaml")
		}
		n := model.Service{ID: model.ServiceID("resource/" + name), ProjectID: "infra", Kind: "resource", Version: 2, Resource: r}
		if name != "db" {
			n.DependsOn = []model.ServiceID{"resource/db"}
			n.Requires = []model.Requirement{{Node: "resource/db", Condition: "available"}}
		}
		if name == "observer" {
			r.Control = "observe"
		}
		nodes = append(nodes, n)
	}
	s, err := newManuallySampledSession(model.Workspace{Root: root, Version: 2, Projects: []model.Project{{ID: "infra", Services: nodes}}}, &fakeRunner{}, &countingPorts{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	s.sample(context.Background())
	return s, filepath.Join(root, "calls")
}

func TestControlStopRestartPreservesObserveOnlyResources(t *testing.T) {
	for _, adapter := range []string{"compose", "existing-container"} {
		actions := []string{"stop", "restart", "docker-stop", "docker-restart"}
		if adapter == "existing-container" {
			actions = append(actions, "all-stop", "all-restart")
		}
		for _, action := range actions {
			t.Run(adapter+"/"+action, func(t *testing.T) {
				s, calls := readonlyResourceSession(t, adapter)
				b := NewControlBackend(s, control.Identity{SessionID: "readonly-fixture"})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				s.entries["resource/observer"].cancel = cancel
				generation := s.entries["resource/observer"].gen
				readCalls := func() string {
					t.Helper()
					raw, err := os.ReadFile(calls)
					if err != nil {
						t.Fatal(err)
					}
					return string(raw)
				}
				for _, directAction := range []string{"stop", "restart"} {
					direct := control.PlanRequest{Action: directAction, Targets: []string{"resource/observer"}}
					_, err := b.PlanState(context.Background(), direct)
					var api *control.APIError
					if !errors.As(err, &api) || api.Code != "forbidden" {
						t.Fatalf("explicit observe-only target accepted: %v", err)
					}
					if _, err := b.Execute(context.Background(), direct, control.PlanState{}); !errors.As(err, &api) || api.Code != "forbidden" {
						t.Fatalf("explicit observe-only target executed: %v", err)
					}
				}
				req := control.PlanRequest{Action: action, Targets: []string{"resource/db"}}
				if strings.HasPrefix(action, "all-") {
					req.Targets = nil
				}
				plan, err := b.PlanState(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"resource/db", "resource/managed"}
				if !slices.Equal(plan.Affected, want) {
					t.Fatalf("incorrect stop/restart plan: %v", plan.Affected)
				}
				if raw := readCalls(); raw != "" {
					t.Fatalf("planning mutated containers: %s", raw)
				}
				results, err := b.Execute(context.Background(), req, plan)
				if err != nil {
					t.Fatal(err)
				}
				if len(results) != len(want) {
					t.Fatalf("incorrect execution results: %+v", results)
				}
				for i, result := range results {
					if result.Target != want[i] || result.State != "succeeded" {
						t.Fatalf("managed target failed or observer was reported: %+v", results)
					}
				}
				commands := "stop managed\nstop db\n"
				if strings.HasSuffix(action, "restart") {
					commands += "start db\nstart managed\n"
				}
				if raw := readCalls(); raw != commands {
					t.Fatalf("wrong managed action order or observer mutated: %q", raw)
				}
				observer := s.entries["resource/observer"]
				if observer.gen != generation || observer.operationID != "" || observer.state != "available" || ctx.Err() != nil {
					t.Fatalf("observe-only lifecycle changed: %+v, cancellation=%v", observer, ctx.Err())
				}
			})
		}
	}
}

func TestControlCancellationDuringResourceEvidence(t *testing.T) {
	for _, execute := range []bool{false, true} {
		name := "plan"
		if execute {
			name = "execute"
		}
		t.Run(name, func(t *testing.T) {
			s, calls := readonlyResourceSession(t, "compose")
			marker := filepath.Join(filepath.Dir(calls), "binding-entered")
			dockerPath := filepath.Join(filepath.Dir(calls), "docker")
			script, err := os.ReadFile(dockerPath)
			if err != nil {
				t.Fatal(err)
			}
			blocked := strings.Replace(string(script), "*' ps '*)", "*' ps '*) : > \"$SH_READONLY_FIXTURE/binding-entered\"; sleep 0.2; exit 1;", 1)
			if err := os.WriteFile(dockerPath, []byte(blocked), 0700); err != nil {
				t.Fatal(err)
			}
			b := NewControlBackend(s, control.Identity{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				req := control.PlanRequest{Action: "stop", Targets: []string{"resource/db"}}
				var err error
				if execute {
					_, err = b.Execute(ctx, req, control.PlanState{})
				} else {
					_, err = b.PlanState(ctx, req)
				}
				done <- err
			}()
			deadline := time.Now().Add(2 * time.Second)
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					cancel()
					<-done
					t.Fatal("container binding read never began")
				}
				time.Sleep(time.Millisecond)
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation replaced by identity error: %v", err)
			}
			if raw, err := os.ReadFile(calls); err != nil || len(raw) != 0 {
				t.Fatalf("canceled plan mutated a resource: %q %v", raw, err)
			}
		})
	}
}
