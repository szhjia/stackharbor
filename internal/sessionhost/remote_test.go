package sessionhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/tui"
)

type remoteFixture struct {
	mu        sync.Mutex
	snapshot  control.Snapshot
	store     *logs.Store
	plan      control.Plan
	plans     []control.PlanRequest
	submits   []control.SubmitRequest
	operation control.Operation
	closed    int
}

func (f *remoteFixture) Snapshot(ctx context.Context) (control.Snapshot, error) {
	return f.snapshot, ctx.Err()
}
func (f *remoteFixture) Logs(ctx context.Context, target string, after uint64, limit int) (control.LogPage, error) {
	return control.ReadLogs(f.store, model.ServiceID(target), after, limit), ctx.Err()
}
func (f *remoteFixture) Plan(ctx context.Context, req control.PlanRequest) (control.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return control.Plan{}, err
	}
	f.plans = append(f.plans, req)
	return f.plan, nil
}
func (f *remoteFixture) Submit(ctx context.Context, req control.SubmitRequest) (control.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return control.Operation{}, err
	}
	f.submits = append(f.submits, req)
	return f.operation, nil
}
func (f *remoteFixture) Operation(ctx context.Context, id string) (control.Operation, error) {
	return f.operation, ctx.Err()
}
func (f *remoteFixture) Close() error { f.mu.Lock(); defer f.mu.Unlock(); f.closed++; return nil }

func TestRemoteProjectsAuthoritativeNodesAndPhysicalMetrics(t *testing.T) {
	created := int64(123)
	rss := uint64(456)
	uptime := int64(789)
	cpu := 1.5
	at := time.Now().UTC()
	w := model.Workspace{Root: "/workspace", Projects: []model.Project{{ID: "app", Name: "Application", Services: []model.Service{
		{ID: "app/api", Key: "api", Name: "Old name", DependsOn: []model.ServiceID{"local/old"}, Env: map[string]string{"TOKEN": "secret"}},
		{ID: "app/new-on-disk", Key: "new"},
	}}}}
	snapshot := control.Snapshot{ObservedAt: at, Nodes: []control.Node{{ID: "app/api", ProjectID: "app", Name: "Engine name", Kind: "service", State: "ready", Reason: "listening", Ownership: "session", DependsOn: []string{"app/db"}, ResourceRefs: []string{"proc:known", "proc:unknown"}, Metric: control.Metric{Known: true, RSSBytes: &rss, CPUPercent: &cpu, UptimeMillis: &uptime, SampledAt: &at}, Ports: []control.Port{{Port: 8123, Status: "owned", ResourceRefs: []string{"proc:known"}}}}, {ID: "app/db", ProjectID: "app", Kind: "resource", Name: "Database", State: "available", Ownership: "external"}}, Processes: []control.Process{{ID: "proc:known", PID: 42, CreatedUnixMillis: &created, Ownership: "session", Metric: control.Metric{Known: true, RSSBytes: &rss}}, {ID: "proc:unknown", PID: 43, Ownership: "session"}}, Containers: []control.Container{{ID: "container", EndpointIdentity: "daemon", ServiceID: "app/db", Name: "Database", State: "running", PublishedEndpoints: []control.PublishedEndpoint{{Host: "127.0.0.1", Port: 5432}}}}}
	out := remoteSnapshot(w, snapshot)
	if out.Root != w.Root || !out.ObservedAt.Equal(at) || len(out.Services) != 2 || len(out.Projects) != 1 || len(out.Projects[0].Services) != 2 {
		t.Fatalf("projection: %+v", out)
	}
	api := out.Services[0]
	if api.Spec.Name != "Engine name" || api.Spec.Key != "api" || len(api.Spec.DependsOn) != 1 || api.Spec.DependsOn[0] != "app/db" || len(api.Owned) != 1 || api.Owned[0].PID != 42 {
		t.Fatalf("node mismatch: %+v", api)
	}
	if api.Metric.RSS != rss || api.Metric.Uptime != 789*time.Millisecond || api.Metric.CPUPercent == nil || *api.Metric.CPUPercent != cpu || len(api.Ports) != 1 || api.Ports[0].Listeners[0].PID != 42 {
		t.Fatalf("metrics/ports: %+v", api)
	}
	if out.Services[1].Spec.Control != "observe" || out.Services[1].Spec.Kind != "resource" || out.DockerFile == "" || len(out.Docker) != 1 || out.Docker[0].Ports != "127.0.0.1:5432" {
		t.Fatalf("external/container state: %+v", out)
	}
}

func TestRemoteObservationMetadataSurvivesChangedOrMissingLocalFiles(t *testing.T) {
	include, exclude := true, false
	w := model.Workspace{Projects: []model.Project{{ID: "app", Services: []model.Service{{ID: "app/api", Version: 1, AllActions: &include, Ports: []model.Port{{Name: "HTTP", Number: 8123}}}}}}}
	snapshot := control.Snapshot{ComposeConfigured: true, Nodes: []control.Node{{ID: "app/api", ProjectID: "app", Version: 2, AllActions: &exclude, Kind: "service", State: "stopped", ObservedState: "running", ObservedReason: "external instance", ReadinessChecked: true, Ports: []control.Port{{Port: 8123, Status: "external"}}}, {ID: "app/db", ProjectID: "app", Kind: "resource", AllActions: &include}}}
	out := remoteSnapshot(w, snapshot)
	if out.DockerFile == "" || out.Services[0].Spec.Version != 2 || out.Services[0].Spec.InAllActions() || out.Services[0].ObservedState != "running" || out.Services[0].ObservedReason != "external instance" || !out.Services[0].ReadinessChecked || out.Services[0].Spec.Ports[0].Name != "HTTP" {
		t.Fatal(out)
	}
	if !out.Services[1].Spec.InAllActions() {
		t.Fatal("engine-included resource became unselectable without local metadata")
	}
	if !w.Services()[0].InAllActions() {
		t.Fatal("projection mutated local workspace")
	}
}

func TestRemoteLoadsRetainedBacklogAndDetachesIdempotently(t *testing.T) {
	f := &remoteFixture{store: logs.NewStore()}
	for i := 0; i < 1100; i++ {
		f.store.Append(logs.Entry{ServiceID: "app/api", Text: fmt.Sprint(i)})
	}
	r, err := newRemote(context.Background(), model.Workspace{Root: "/workspace"}, f)
	if err != nil {
		t.Fatal(err)
	}
	if entries := r.Logs().Entries(nil); len(entries) != 1100 || entries[0].Text != "0" || entries[1099].Text != "1099" {
		t.Fatalf("backlog incomplete: %d", len(entries))
	}
	if err = r.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.Logs().Entries(nil)) != 1100 {
		t.Fatal("duplicate logs on refresh")
	}
	_ = r.Shutdown(context.Background())
	_ = r.Shutdown(context.Background())
	select {
	case <-r.Done():
	default:
		t.Fatal("detach did not end client")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed != 1 || len(f.submits) != 0 || len(f.plans) != 0 {
		t.Fatalf("detach mutated host: %+v", f)
	}
}

func TestRemoteRetainsPlanAndRequiresWarningConfirmation(t *testing.T) {
	f := &remoteFixture{store: logs.NewStore(), plan: control.Plan{ID: "retained", Affected: []string{"app/api", "app/db"}, Warnings: []string{"Port 8123 owned by external PID 42"}}, operation: control.Operation{ID: "operation", State: "succeeded"}}
	r, err := newRemote(context.Background(), model.Workspace{}, f)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Shutdown(context.Background())
	p, err := r.PlanAction(context.Background(), "start", []model.ServiceID{"app/api"}, nil)
	if err != nil || !p.RequiresConfirmation || len(p.Affected) != 2 || p.ID != "retained" || p.Key == "" {
		t.Fatal(p, err)
	}
	if err = r.Start(context.Background(), []model.ServiceID{"app/api"}); err == nil {
		t.Fatal("warnings bypassed confirmation")
	}
	if err = r.ExecuteAction(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(f.submits) != 1 || f.submits[0].PlanID != p.ID || f.submits[0].IdempotencyKey != p.Key || f.plans[0].Action != "release-start" {
		t.Fatal(f.plans, f.submits)
	}
}

func TestRemoteDetachPreservesInflightOperationAndRefusesLaterWrites(t *testing.T) {
	f := &remoteFixture{store: logs.NewStore(), operation: control.Operation{ID: "operation-continues", State: "running"}}
	r, err := newRemote(context.Background(), model.Workspace{}, f)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.ExecuteAction(context.Background(), tui.PlannedAction{ID: "retained", Key: "key"}) }()
	deadline := time.Now().Add(time.Second)
	for {
		f.mu.Lock()
		submitted := len(f.submits) == 1
		f.mu.Unlock()
		if submitted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("submit never reached host")
		}
		time.Sleep(time.Millisecond)
	}
	_ = r.Shutdown(context.Background())
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "operation-continues") {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("detachment left operation waiter running")
	}
	if _, err = r.PlanAction(context.Background(), "stop", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("write after detach", err)
	}
	if f.operation.State != "running" {
		t.Fatal("detach canceled host operation")
	}
}
