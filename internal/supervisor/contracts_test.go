package supervisor

import (
	"context"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/runner"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestV2StartedAndRecheckRefusesChangedInputs(t *testing.T) {
	n := service("app/service/api")
	n.Kind = "service"
	n.Version = 2
	n.SourceFile = filepath.Join(t.TempDir(), "app.yaml")
	os.WriteFile(n.SourceFile, []byte("first"), 0600)
	s, f := setup(t, n)
	if e := s.Start(context.Background(), []model.ServiceID{n.ID}); e != nil {
		t.Fatal(e)
	}
	if s.Snapshot().Services[0].State != "started" {
		t.Fatal("unprobed process claimed readiness")
	}
	os.WriteFile(n.SourceFile, []byte("changed"), 0600)
	if e := s.Start(context.Background(), []model.ServiceID{n.ID}); e == nil {
		t.Fatal("changed registration executed")
	}
	if f.starts[n.ID] != 1 {
		t.Fatal("duplicate spawn")
	}
}
func TestTaskCancellationCanRetryAndIndependentBranchSurvives(t *testing.T) {
	n := service("app/task/check")
	n.Kind = "task"
	n.Task = &model.TaskSpec{Effect: "read-only", Policy: "always", TimeoutSeconds: 5}
	api := service("app/service/api")
	api.Version = 2
	api.Kind = "service"
	s, f := setup(t, n, api)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Start(ctx, []model.ServiceID{n.ID}) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		h := f.handles[n.ID]
		f.mu.Unlock()
		if h != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if <-done == nil {
		t.Fatal("cancellation hidden")
	}
	state := func(id model.ServiceID) string {
		for _, v := range s.Snapshot().Services {
			if v.Spec.ID == id {
				return v.State
			}
		}
		return ""
	}
	for time.Now().Before(deadline) {
		if state(n.ID) == "failed" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	deadline = time.Now().Add(time.Second)
	go func() { done <- s.Start(context.Background(), []model.ServiceID{n.ID, api.ID}) }()
	for time.Now().Before(deadline) {
		f.mu.Lock()
		h := f.handles[n.ID]
		count := f.starts[n.ID]
		f.mu.Unlock()
		if count == 2 {
			h.done <- runner.ExitResult{Code: 4}
			break
		}
		time.Sleep(time.Millisecond)
	}
	if <-done == nil {
		t.Fatal("task failure hidden")
	}
	snap := s.Snapshot()
	if state(api.ID) != "started" {
		t.Fatal("independent branch stopped", snap.Services)
	}
	history, e := ReadHistory(snap.Root)
	if e != nil {
		t.Fatal(e)
	}
	attempts := map[string]bool{}
	for _, v := range history {
		if v.ServiceID == n.ID && v.AttemptID != "" {
			attempts[v.AttemptID] = true
		}
	}
	if len(attempts) != 2 {
		t.Fatal("attempt history lost", attempts)
	}
}
func TestV2RestartAllRestoresOnlyRunningServices(t *testing.T) {
	a, b := service("app/service/a"), service("app/service/b")
	a.Kind = "service"
	b.Kind = "service"
	a.Version = 2
	b.Version = 2
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	f := &fakeRunner{starts: map[model.ServiceID]int{}, handles: map[model.ServiceID]*fakeHandle{}}
	s, e := New(model.Workspace{Version: 2, Root: t.TempDir(), Projects: []model.Project{{ID: "app", Services: []model.Service{a, b}}}}, f, observe.NewPortProbe(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Shutdown(context.Background())
	if e = s.Start(context.Background(), []model.ServiceID{a.ID}); e != nil {
		t.Fatal(e)
	}
	if e = s.AllAction(context.Background(), "restart"); e != nil {
		t.Fatal(e)
	}
	if f.starts[a.ID] != 2 || f.starts[b.ID] != 0 {
		t.Fatal("restart expanded scope", f.starts)
	}
}

func TestConditionsAndResourceHealthAreDistinct(t *testing.T) {
	e := &entry{state: "started", spec: model.Service{Ready: &model.ReadyProbe{TCP: "localhost:9000"}}}
	if dependencySatisfied(e, "ready") || !dependencySatisfied(e, "started") {
		t.Fatal("started promoted to ready")
	}
	r := &model.ResourceSpec{Service: "db", Available: "healthy"}
	if ok, _ := resourceAvailable(r, []model.DockerSnapshot{{Service: "db", State: "running", Health: "starting"}}); ok {
		t.Fatal("unhealthy resource promoted")
	}
	if ok, id := resourceAvailable(r, []model.DockerSnapshot{{ID: "generation", Service: "db", State: "running", Health: "healthy"}}); !ok || id != "generation" {
		t.Fatal("identity lost")
	}
}

func TestObservedUnreadyServiceIsRecheckedOnExplicitStart(t *testing.T) {
	n := service("app/service/external")
	n.Kind, n.Version, n.Control = "service", 2, "observe"
	n.Ready = &model.ReadyProbe{TCP: "127.0.0.1:1"}
	s, f := setup(t, n)
	s.mu.Lock()
	e := s.entries[n.ID]
	e.state = "unready"
	e.reason = "External process identity changed; verification required"
	e.ready = make(chan struct{})
	close(e.ready)
	e.readyClosed = true
	previous := e.gen
	s.mu.Unlock()
	if err := s.Start(context.Background(), []model.ServiceID{n.ID}); err == nil {
		t.Fatal("unready observation reused as successful readiness")
	}
	s.mu.Lock()
	if e.gen <= previous {
		t.Fatal("explicit start did not create a new observation attempt")
	}
	s.mu.Unlock()
	if f.starts[n.ID] != 0 {
		t.Fatal("read-only observer spawned a process")
	}
}
