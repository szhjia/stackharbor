package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type testBackend struct {
	mu                 sync.Mutex
	fingerprint        string
	calls              int
	active             int
	peak               int
	started            chan string
	gate               chan struct{}
	ignoreCancellation bool
}

func (b *testBackend) Snapshot(context.Context) (Snapshot, error) { return Snapshot{}, nil }
func (b *testBackend) Logs(context.Context, string, uint64, int) (LogPage, error) {
	return LogPage{}, nil
}
func (b *testBackend) PlanState(_ context.Context, r PlanRequest) (PlanState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return PlanState{Fingerprint: b.fingerprint, Affected: append([]string{}, r.Targets...)}, nil
}
func (b *testBackend) Execute(ctx context.Context, r PlanRequest, _ PlanState) ([]TargetResult, error) {
	b.mu.Lock()
	b.calls++
	b.active++
	if b.active > b.peak {
		b.peak = b.active
	}
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.active--; b.mu.Unlock() }()
	if b.started != nil {
		b.started <- r.Action
	}
	if b.gate != nil && r.Action != "close" {
		if b.ignoreCancellation {
			<-b.gate
		} else {
			select {
			case <-b.gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	if r.Action == "close" && b.ignoreCancellation && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return []TargetResult{{Target: "a", State: "succeeded"}}, nil
}
func code(err error) string {
	var e *APIError
	if errors.As(err, &e) {
		return e.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}
func plan(t *testing.T, c *Coordinator, action string) Plan {
	t.Helper()
	targets := []string{"a"}
	if action == "close" {
		targets = nil
	}
	p, e := c.Plan(context.Background(), PlanRequest{Action: action, Targets: targets})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func submit(t *testing.T, c *Coordinator, p Plan, key string) Operation {
	t.Helper()
	o, e := c.Submit(context.Background(), SubmitRequest{PlanID: p.ID, IdempotencyKey: p.ID + ":" + key})
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func terminal(t *testing.T, c *Coordinator, id string) Operation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		o, e := c.Operation(id)
		if e != nil {
			t.Fatal(e)
		}
		if o.State != "queued" && o.State != "running" {
			return o
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return Operation{}
}
func coordinator(b *testBackend, opts Options) *Coordinator {
	return New(Identity{SessionID: "s"}, b, opts)
}

func TestPlanExpiryAndRelevantRevision(t *testing.T) {
	now := time.Now()
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	b := &testBackend{fingerprint: "config/generation/process"}
	c := coordinator(b, Options{Now: clock})
	defer c.Close(context.Background())
	p := plan(t, c, "start")
	p.Targets[0] = "mutated" // returned values cannot mutate retained approval.
	o := submit(t, c, p, "metrics-do-not-matter")
	if terminal(t, c, o.ID).State != "succeeded" {
		t.Fatal("stable relevant state invalidated")
	}
	p = plan(t, c, "start")
	b.mu.Lock()
	b.fingerprint = "changed-generation"
	b.mu.Unlock()
	if _, e := c.Submit(context.Background(), SubmitRequest{PlanID: p.ID, IdempotencyKey: p.ID + ":stale"}); code(e) != "plan_conflict" {
		t.Fatalf("got %v", e)
	}
	p = plan(t, c, "start")
	mu.Lock()
	now = now.Add(60 * time.Second)
	mu.Unlock()
	if _, e := c.Submit(context.Background(), SubmitRequest{PlanID: p.ID, IdempotencyKey: p.ID + ":expired"}); code(e) != "plan_expired" {
		t.Fatalf("got %v", e)
	}
}
func TestSubmitIdempotency(t *testing.T) {
	b := &testBackend{fingerprint: "x"}
	c := coordinator(b, Options{})
	defer c.Close(context.Background())
	p := plan(t, c, "start")
	a := submit(t, c, p, "key")
	z := submit(t, c, p, "key")
	if a.ID != z.ID {
		t.Fatal("duplicate operation")
	}
	terminal(t, c, a.ID)
	p2 := plan(t, c, "stop")
	if _, e := c.Submit(context.Background(), SubmitRequest{PlanID: p2.ID, IdempotencyKey: p.ID + ":key"}); code(e) != "idempotency_conflict" {
		t.Fatalf("got %v", e)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls != 1 {
		t.Fatalf("calls=%d", b.calls)
	}
}
func TestClientCancellationDoesNotCancelAcceptedOperation(t *testing.T) {
	b := &testBackend{fingerprint: "x", started: make(chan string, 4), gate: make(chan struct{})}
	c := coordinator(b, Options{})
	defer c.Close(context.Background())
	p := plan(t, c, "start")
	ctx, cancel := context.WithCancel(context.Background())
	o, e := c.Submit(ctx, SubmitRequest{PlanID: p.ID, IdempotencyKey: p.ID + ":k"})
	if e != nil {
		t.Fatal(e)
	}
	<-b.started
	cancel()
	close(b.gate)
	if terminal(t, c, o.ID).State != "succeeded" {
		t.Fatal("request cancellation killed accepted work")
	}
}
func TestCoordinatorSerializesWritesAndClosePreempts(t *testing.T) {
	b := &testBackend{fingerprint: "x", started: make(chan string, 8), gate: make(chan struct{})}
	notified := make(chan Operation, 1)
	var c *Coordinator
	c = coordinator(b, Options{OnClosed: func(o Operation) {
		if _, e := c.Operation(o.ID); e != nil {
			t.Error(e)
		}
		notified <- o
	}})
	p := plan(t, c, "start")
	a := submit(t, c, p, "a")
	<-b.started
	p2 := plan(t, c, "start")
	z := submit(t, c, p2, "z")
	select {
	case <-b.started:
		t.Fatal("ordinary writes overlapped")
	case <-time.After(20 * time.Millisecond):
	}
	closed := plan(t, c, "close")
	o := submit(t, c, closed, "close")
	if terminal(t, c, o.ID).State != "succeeded" {
		t.Fatal("close failed")
	}
	if terminal(t, c, a.ID).State != "canceled" || terminal(t, c, z.ID).State != "canceled" {
		t.Fatal("close did not cancel writes")
	}
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("close notification missing")
	}
	if _, e := c.Submit(context.Background(), SubmitRequest{PlanID: p.ID, IdempotencyKey: p.ID + ":new"}); code(e) != "session_closed" {
		t.Fatalf("got %v", e)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.peak != 1 {
		t.Fatalf("cleanup overlapped normal execution: %d", b.peak)
	}
}
func TestDequeueRevalidation(t *testing.T) {
	b := &testBackend{fingerprint: "x", started: make(chan string, 8), gate: make(chan struct{})}
	c := coordinator(b, Options{})
	defer c.Close(context.Background())
	p := plan(t, c, "start")
	a := submit(t, c, p, "a")
	<-b.started
	p2 := plan(t, c, "start")
	z := submit(t, c, p2, "z")
	b.mu.Lock()
	b.fingerprint = "y"
	b.mu.Unlock()
	close(b.gate)
	terminal(t, c, a.ID)
	o := terminal(t, c, z.ID)
	if o.State != "failed" || o.Error == nil || o.Error.Code != "plan_conflict" {
		t.Fatalf("got %+v", o)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls != 1 {
		t.Fatal("stale queued plan executed")
	}
}
func TestQueueAndRetentionBounds(t *testing.T) {
	b := &testBackend{fingerprint: "x", started: make(chan string, 200), gate: make(chan struct{})}
	c := coordinator(b, Options{})
	p := plan(t, c, "start")
	submit(t, c, p, "running")
	<-b.started
	for i := 0; i < 100; i++ {
		submit(t, c, plan(t, c, "start"), fmt.Sprint(i))
	}
	fullPlan := plan(t, c, "start")
	if _, e := c.Submit(context.Background(), SubmitRequest{PlanID: fullPlan.ID, IdempotencyKey: fullPlan.ID + ":full"}); code(e) != "queue_full" {
		t.Fatalf("got %v", e)
	}
	if e := c.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	b = &testBackend{fingerprint: "x"}
	c = coordinator(b, Options{Now: clock})
	defer c.Close(context.Background())
	p = plan(t, c, "start")
	first := submit(t, c, p, "old")
	terminal(t, c, first.ID)
	for i := 0; i < 1000; i++ {
		mu.Lock()
		now = now.Add(time.Second)
		mu.Unlock()
		o := submit(t, c, plan(t, c, "start"), fmt.Sprint(i))
		terminal(t, c, o.ID)
	}
	if _, e := c.Operation(first.ID); code(e) != "not_found" {
		t.Fatalf("got %v", e)
	}
	if len(c.Operations()) != 1000 {
		t.Fatalf("retention=%d", len(c.Operations()))
	}
	if _, e := c.Submit(context.Background(), SubmitRequest{PlanID: p.ID, IdempotencyKey: p.ID + ":old"}); code(e) != "plan_expired" {
		t.Fatalf("got %v", e)
	}
	mu.Lock()
	now = now.Add(24 * time.Hour)
	mu.Unlock()
	if len(c.Operations()) != 0 {
		t.Fatal("aged records retained")
	}
}

func TestCloseFinalizationRemainsRunningAndReportsFailure(t *testing.T) {
	b := &testBackend{fingerprint: "x"}
	entered := make(chan Operation, 1)
	release := make(chan struct{})
	notified := make(chan Operation, 1)
	c := coordinator(b, Options{FinalizeClose: func(_ context.Context, o Operation) error {
		entered <- o
		<-release
		return errors.New("completion journal unavailable")
	}, OnClosed: func(o Operation) { notified <- o }})
	p := plan(t, c, "close")
	o := submit(t, c, p, "close")
	proposed := <-entered
	if proposed.State != "succeeded" {
		t.Fatalf("proposed=%+v", proposed)
	}
	current, e := c.Operation(o.ID)
	if e != nil || current.State != "running" {
		t.Fatalf("published before finalization: %+v %v", current, e)
	}
	close(release)
	final := terminal(t, c, o.ID)
	if final.State == "succeeded" || final.Error == nil || final.Error.Code != "close_finalization_failed" {
		t.Fatalf("failure missing: %+v", final)
	}
	if e := c.Close(context.Background()); code(e) != "close_finalization_failed" {
		t.Fatalf("close returned %v", e)
	}
	select {
	case reported := <-notified:
		if reported.Error == nil {
			t.Fatal("notification discarded failure")
		}
	case <-time.After(time.Second):
		t.Fatal("notification absent")
	}
}
func TestQueuedCancellationAndStalePlanHaveTargetResults(t *testing.T) {
	b := &testBackend{fingerprint: "x", started: make(chan string, 8), gate: make(chan struct{})}
	c := coordinator(b, Options{})
	defer c.Close(context.Background())
	p := plan(t, c, "start")
	a := submit(t, c, p, "a")
	<-b.started
	p2 := plan(t, c, "start")
	z := submit(t, c, p2, "z")
	if e := c.Cancel(z.ID); e != nil {
		t.Fatal(e)
	}
	o := terminal(t, c, z.ID)
	if len(o.Results) != 1 || o.Results[0].State != "canceled" {
		t.Fatalf("canceled results: %+v", o)
	}
	p3 := plan(t, c, "start")
	z = submit(t, c, p3, "stale")
	b.mu.Lock()
	b.fingerprint = "y"
	b.mu.Unlock()
	close(b.gate)
	terminal(t, c, a.ID)
	o = terminal(t, c, z.ID)
	if len(o.Results) != 1 || o.Results[0].Error == nil || o.Results[0].Error.Code != "plan_conflict" {
		t.Fatalf("stale results: %+v", o)
	}
}
func TestRejectInvalidScopes(t *testing.T) {
	b := &testBackend{fingerprint: "x"}
	c := coordinator(b, Options{})
	defer c.Close(context.Background())
	for _, r := range []PlanRequest{{Action: "close", Targets: []string{"a"}}, {Action: "release", Port: 8080}, {Action: "start", Targets: []string{"a"}, Port: 8080}, {Action: "start", Targets: []string{"a", "a"}}} {
		if _, e := c.Plan(context.Background(), r); code(e) != "invalid_request" {
			t.Fatalf("accepted %+v: %v", r, e)
		}
	}
}

func TestPlanBoundSingleUseKeysHaveNoSessionTombstones(t *testing.T) {
	now := time.Now()
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	b := &testBackend{fingerprint: "x"}
	c := coordinator(b, Options{Now: clock})
	defer c.Close(context.Background())
	p := plan(t, c, "start")
	key := NewIdempotencyKey(p.ID)
	first, e := c.Submit(context.Background(), SubmitRequest{PlanID: p.ID, IdempotencyKey: key})
	if e != nil {
		t.Fatal(e)
	}
	terminal(t, c, first.ID)
	again, e := c.Submit(context.Background(), SubmitRequest{PlanID: p.ID, IdempotencyKey: key})
	if e != nil || again.ID != first.ID {
		t.Fatalf("retry: %+v %v", again, e)
	}
	if _, e = c.Submit(context.Background(), SubmitRequest{PlanID: p.ID, IdempotencyKey: NewIdempotencyKey(p.ID)}); code(e) != "plan_conflict" {
		t.Fatalf("consumed plan: %v", e)
	}
	mu.Lock()
	now = now.Add(60 * time.Second)
	mu.Unlock()
	fresh := plan(t, c, "start")
	if _, e = c.Submit(context.Background(), SubmitRequest{PlanID: p.ID, IdempotencyKey: key}); code(e) != "plan_expired" {
		t.Fatalf("old replay: %v", e)
	}
	if _, e = c.Submit(context.Background(), SubmitRequest{PlanID: fresh.ID, IdempotencyKey: key}); code(e) != "idempotency_conflict" {
		t.Fatalf("rebound replay: %v", e)
	}
	operation := submit(t, c, fresh, "fresh")
	if terminal(t, c, operation.ID).State != "succeeded" {
		t.Fatal("fresh session operation rejected")
	}
}

func TestCloseBudgetIncludesWaitingForInFlightCancellation(t *testing.T) {
	b := &testBackend{fingerprint: "x", started: make(chan string, 8), gate: make(chan struct{}), ignoreCancellation: true}
	c := coordinator(b, Options{})
	c.mu.Lock()
	c.closeBudget = 20 * time.Millisecond
	c.mu.Unlock()
	p := plan(t, c, "start")
	submit(t, c, p, "run")
	<-b.started
	done := make(chan error, 1)
	go func() { done <- c.Close(context.Background()) }()
	select {
	case e := <-done:
		if !errors.Is(e, context.DeadlineExceeded) {
			t.Fatalf("timeout=%v", e)
		}
	case <-time.After(time.Second):
		close(b.gate)
		t.Fatal("shared close budget did not cover pending execution")
	}
	select {
	case action := <-b.started:
		t.Fatalf("cleanup overlapped pending %s", action)
	default:
	}
	c.mu.Lock()
	closeID := c.closeID
	c.mu.Unlock()
	current, e := c.Operation(closeID)
	if e != nil || current.State == "succeeded" {
		t.Fatalf("false completion: %+v %v", current, e)
	}
	close(b.gate)
	final := terminal(t, c, closeID)
	if final.State == "succeeded" {
		t.Fatal("expired cleanup budget claimed success")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.peak != 1 {
		t.Fatalf("overlap=%d", b.peak)
	}
}
