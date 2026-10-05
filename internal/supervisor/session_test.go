package supervisor

import (
	"context"
	"errors"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/process"
	"github.com/szhjia/stackharbor/internal/runner"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeHandle struct {
	mu       sync.Mutex
	done     chan runner.ExitResult
	complete bool
	stops    *[]string
	id       string
}

func (h *fakeHandle) Identities() []model.ProcessIdentity { return nil }
func (h *fakeHandle) Done() <-chan runner.ExitResult      { return h.done }
func (h *fakeHandle) Stop(context.Context, model.StopPolicy) runner.StopResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.stops = append(*h.stops, h.id)
	return runner.StopResult{Complete: h.complete}
}

type fakeRunner struct {
	mu           sync.Mutex
	starts       map[model.ServiceID]int
	handles      map[model.ServiceID]*fakeHandle
	stops        []string
	active, peak int
	delay        time.Duration
}

func (f *fakeRunner) Start(ctx context.Context, s model.Service, _ func(string, string)) (runner.Handle, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.peak {
		f.peak = f.active
	}
	f.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(f.delay):
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active--
	f.starts[s.ID]++
	h := &fakeHandle{done: make(chan runner.ExitResult, 1), complete: true, stops: &f.stops, id: string(s.ID)}
	f.handles[s.ID] = h
	return h, nil
}
func setup(t *testing.T, ss ...model.Service) (*Session, *fakeRunner) {
	t.Helper()
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	f := &fakeRunner{starts: map[model.ServiceID]int{}, handles: map[model.ServiceID]*fakeHandle{}}
	w := model.Workspace{Root: t.TempDir(), Projects: []model.Project{{ID: "app", Services: ss}}}
	s, e := New(w, f, observe.NewPortProbe(), observe.NewSampler(process.NewReader()))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return s, f
}
func service(id string, deps ...model.ServiceID) model.Service {
	return model.Service{ID: model.ServiceID(id), ProjectID: "app", DependsOn: deps, Stop: model.StopPolicy{Signal: "TERM", Timeout: time.Millisecond}}
}
func TestSessionDependencyReadyBeforeStart(t *testing.T) {
	s, f := setup(t, service("app/db"), service("app/web", "app/db"))
	if e := s.Start(context.Background(), []model.ServiceID{"app/web"}); e != nil {
		t.Fatal(e)
	}
	if f.starts["app/db"] != 1 || f.starts["app/web"] != 1 {
		t.Fatal("dependencies not started", f.starts)
	}
}
func TestStartMaxFourAndDuplicateAction(t *testing.T) {
	ss := []model.Service{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		ss = append(ss, service("app/"+id))
	}
	s, f := setup(t, ss...)
	f.delay = 20 * time.Millisecond
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Start(context.Background(), []model.ServiceID{"app/a", "app/b", "app/c", "app/d", "app/e", "app/f"})
		}()
	}
	wg.Wait()
	if f.peak > 4 || f.peak == 0 {
		t.Fatal("invalid concurrent startup", f.peak)
	}
	for _, n := range f.starts {
		if n != 1 {
			t.Fatal("duplicate instance")
		}
	}
}
func TestStoppingRefusesRestartUntilComplete(t *testing.T) {
	s, f := setup(t, service("app/web"))
	_ = s.Start(context.Background(), []model.ServiceID{"app/web"})
	f.handles["app/web"].complete = false
	if e := s.Restart(context.Background(), []model.ServiceID{"app/web"}); e == nil {
		t.Fatal("restarted with incomplete cleanup")
	}
	if f.starts["app/web"] != 1 {
		t.Fatal("duplicate instance")
	}
	f.handles["app/web"].complete = true
}
func TestStopReverseDependencyAndRestartOriginalSet(t *testing.T) {
	s, f := setup(t, service("app/db"), service("app/web", "app/db"))
	_ = s.Start(context.Background(), []model.ServiceID{"app/web"})
	if e := s.Restart(context.Background(), []model.ServiceID{"app/db"}); e != nil {
		t.Fatal(e)
	}
	if len(f.stops) < 2 || f.stops[0] != "app/web" || f.stops[1] != "app/db" {
		t.Fatal("not reverse order", f.stops)
	}
	if f.starts["app/web"] != 2 {
		t.Fatal("downstream set lost")
	}
}
func TestSecondSessionLockAndRootExitFailure(t *testing.T) {
	s, f := setup(t, service("app/web"))
	w := model.Workspace{Root: s.Snapshot().Root}
	if other, e := New(w, f, observe.NewPortProbe(), nil); e == nil {
		_ = other.Shutdown(context.Background())
		t.Fatal("second session acquired lock")
	}
	_ = s.Start(context.Background(), []model.ServiceID{"app/web"})
	f.handles["app/web"].done <- runner.ExitResult{Code: 3, Err: errors.New("exit")}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s.Snapshot().Services[0].State == "failed" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("root exit not reflected")
}
func TestShutdownBoundedAndProbeCancellation(t *testing.T) {
	s, _ := setup(t, service("app/web"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	if e := s.Start(ctx, []model.ServiceID{"app/web"}); e == nil {
		t.Fatal("start accepted after shutdown")
	}
}

func TestReadinessTimeoutRecoveryAndSnapshotIsolation(t *testing.T) {
	var healthy atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	db := service("app/db")
	db.Ready = &model.ReadyProbe{HTTP: server.URL, Timeout: 20 * time.Millisecond}
	s, f := setup(t, db, service("app/web", "app/db"))
	done := make(chan error, 1)
	go func() { done <- s.Start(context.Background(), []model.ServiceID{"app/web"}) }()
	time.Sleep(320 * time.Millisecond)
	snap := s.Snapshot()
	if snap.Services[0].State != "unready" {
		t.Fatal("timeout did not retain unready", snap.Services)
	}
	f.mu.Lock()
	n := f.starts["app/web"]
	f.mu.Unlock()
	if n != 0 {
		t.Fatal("downstream started before readiness")
	}
	snap.Services[0].Spec.DependsOn = append(snap.Services[0].Spec.DependsOn, "mutated")
	if len(s.Snapshot().Services[0].Spec.DependsOn) > 0 {
		t.Fatal("snapshot leaked mutation")
	}
	healthy.Store(true)
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recovery did not release dependency")
	}
}
func TestLockInDifferentProcess(t *testing.T) {
	if os.Getenv("SH_LOCK_FIXTURE") == "1" {
		l, e := Acquire(os.Getenv("SH_LOCK_ROOT"))
		if e == nil {
			l.Close()
			os.Exit(2)
		}
		os.Exit(0)
	}
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	root := t.TempDir()
	l, e := Acquire(root)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockInDifferentProcess$")
	cmd.Env = append(os.Environ(), "SH_LOCK_FIXTURE=1", "SH_LOCK_ROOT="+root)
	if e := cmd.Run(); e != nil {
		t.Fatal("child acquired occupied lock", e)
	}
}
func TestStopCancelsProbeAndRejectsOldGeneration(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	v := service("app/web")
	v.Ready = &model.ReadyProbe{HTTP: server.URL, Timeout: time.Second}
	s, _ := setup(t, v)
	done := make(chan error, 1)
	go func() { done <- s.Start(context.Background(), []model.ServiceID{v.ID}) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := s.Stop(ctx, []model.ServiceID{v.ID}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("probe cancellation blocked")
	}
	if s.Snapshot().Services[0].State != "stopped" {
		t.Fatal("old probe changed state")
	}
}
