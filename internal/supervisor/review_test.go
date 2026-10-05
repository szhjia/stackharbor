package supervisor

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewFailedDependencyCannotRestart(t *testing.T) {
	s, f := setup(t, service("app/db"), service("app/web", "app/db"))
	if e := s.Start(context.Background(), []model.ServiceID{"app/web"}); e != nil {
		t.Fatal(e)
	}
	f.handles["app/db"].complete = false
	if e := s.Stop(context.Background(), []model.ServiceID{"app/db"}); e == nil {
		t.Fatal("expected incomplete cleanup")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	e := s.Start(ctx, []model.ServiceID{"app/web"})
	f.mu.Lock()
	n := f.starts["app/db"]
	f.mu.Unlock()
	f.handles["app/db"].complete = true
	if n != 1 {
		t.Fatalf("incomplete dependency relaunched: starts=%d err=%v", n, e)
	}
}
func TestReviewHealthRecheckedAfterSuccess(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !healthy.Load() {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	a := service("app/web")
	a.Ready = &model.ReadyProbe{HTTP: server.URL, Timeout: time.Second}
	s, _ := setup(t, a)
	if e := s.Start(context.Background(), []model.ServiceID{a.ID}); e != nil {
		t.Fatal(e)
	}
	healthy.Store(false)
	time.Sleep(2500 * time.Millisecond)
	if got := s.Snapshot().Services[0].State; got != "unready" {
		t.Fatalf("health changed to 503 but state=%s requests=%d", got, calls.Load())
	}
}

type countingPorts struct{ calls int }

func (p *countingPorts) Observe(context.Context, []model.Port, []model.ProcessIdentity) []model.PortObservation {
	p.calls++
	return nil
}
func (*countingPorts) CheckStart(context.Context, []model.Port) error { return nil }
func TestObservationUsesOneWorkspaceProbe(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	ports := &countingPorts{}
	specs := []model.Service{}
	for i := range 100 {
		specs = append(specs, service(fmt.Sprintf("app/service%d", i)))
	}
	w := model.Workspace{Root: t.TempDir(), Projects: []model.Project{{ID: "app", Services: specs}}}
	s, e := New(w, &fakeRunner{}, ports, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Shutdown(context.Background())
	s.sample(context.Background())
	if ports.calls != 1 {
		t.Fatal("per-service lsof multiplies scan cost", ports.calls)
	}
}

func TestHealthRecoveryGatesNewDownstream(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	db := service("app/db")
	db.Ready = &model.ReadyProbe{HTTP: server.URL, Timeout: time.Second}
	s, f := setup(t, db, service("app/web", "app/db"))
	if e := s.Start(context.Background(), []model.ServiceID{db.ID}); e != nil {
		t.Fatal(e)
	}
	healthy.Store(false)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && s.Snapshot().Services[0].State != "unready" {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Snapshot().Services[0].State != "unready" {
		t.Fatal("lost unhealthy transition")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Start(ctx, []model.ServiceID{"app/web"}) }()
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	n := f.starts["app/web"]
	f.mu.Unlock()
	if n != 0 {
		t.Fatal("unhealthy dependency admitted downstream")
	}
	healthy.Store(true)
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("dependency recovery did not release downstream")
	}
}
