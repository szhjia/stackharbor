package supervisor

import (
	"context"
	"errors"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/runner"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestControlPlanIgnoresMetricsAndBindsGeneration(t *testing.T) {
	s, _ := setup(t, service("app/a"), service("app/b", "app/a"))
	b := NewControlBackend(s, control.Identity{SessionID: "s"})
	req := control.PlanRequest{Action: "start", Targets: []string{"app/b"}}
	p, e := b.PlanState(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Affected) != 2 {
		t.Fatalf("missing dependency: %+v", p)
	}
	s.mu.Lock()
	s.entries["app/a"].metric.RSS = 123
	s.event("app/a", "log", "metric")
	s.mu.Unlock()
	q, e := b.PlanState(context.Background(), req)
	if e != nil || p.Fingerprint != q.Fingerprint {
		t.Fatalf("metric invalidated plan: %v", e)
	}
	s.mu.Lock()
	s.entries["app/a"].gen++
	s.mu.Unlock()
	q, _ = b.PlanState(context.Background(), req)
	if p.Fingerprint == q.Fingerprint {
		t.Fatal("generation not bound")
	}
	before := q.Fingerprint
	s.mu.Lock()
	s.entries["app/a"].observed = []model.ProcessIdentity{{PID: 100, CreatedMillis: 1000}}
	s.mu.Unlock()
	q, _ = b.PlanState(context.Background(), req)
	if before == q.Fingerprint {
		t.Fatal("process identity not bound")
	}
	before = q.Fingerprint
	s.mu.Lock()
	s.entries["app/a"].resourceIdentity = "container-v2"
	s.mu.Unlock()
	q, _ = b.PlanState(context.Background(), req)
	if before == q.Fingerprint {
		t.Fatal("container identity not bound")
	}
}
func TestControlValidatesAllTargetsBeforeWrites(t *testing.T) {
	s, f := setup(t, service("app/a"))
	b := NewControlBackend(s, control.Identity{})
	r := control.PlanRequest{Action: "start", Targets: []string{"app/a", "missing"}}
	if _, e := b.PlanState(context.Background(), r); e == nil {
		t.Fatal("unknown target accepted")
	}
	if _, e := b.Execute(context.Background(), r, control.PlanState{}); e == nil {
		t.Fatal("unknown target executed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.starts["app/a"] != 0 {
		t.Fatal("valid prefix was started")
	}
}
func TestControlStandaloneReleaseDoesNotStart(t *testing.T) {
	n := service("app/a")
	n.Ports = []model.Port{{Number: 18456}}
	s, f := setup(t, n)
	b := NewControlBackend(s, control.Identity{})
	r := control.PlanRequest{Action: "release", Targets: []string{"app/a"}, Port: 18456}
	p, e := b.PlanState(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Execute(context.Background(), r, p); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.starts["app/a"] != 0 {
		t.Fatal("release started the service")
	}
	if _, e = b.PlanState(context.Background(), control.PlanRequest{Action: "release", Targets: []string{"app/a"}, Port: 18457}); e == nil {
		t.Fatal("undeclared port accepted")
	}
}
func TestControlConfigMismatchStillAllowsRecovery(t *testing.T) {
	s, _ := setup(t, service("app/a"))
	file := filepath.Join(t.TempDir(), "config.yaml")
	if e := os.WriteFile(file, []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	digest, e := inputDigest(file)
	if e != nil {
		t.Fatal(e)
	}
	s.inputDigests[file] = digest
	if e = os.WriteFile(file, []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	b := NewControlBackend(s, control.Identity{})
	for _, action := range []string{"start", "restart", "release"} {
		_, e = b.PlanState(context.Background(), control.PlanRequest{Action: action, Targets: []string{"app/a"}})
		var api *control.APIError
		if !errors.As(e, &api) || api.Code != "plan_conflict" {
			t.Fatalf("%s: %v", action, e)
		}
	}
	for _, action := range []string{"stop", "close"} {
		targets := []string{"app/a"}
		if action == "close" {
			targets = nil
		}
		if _, e = b.PlanState(context.Background(), control.PlanRequest{Action: action, Targets: targets}); e != nil {
			t.Fatalf("%s: %v", action, e)
		}
	}
}
func TestControlCleanupRetainsLockUntilFinalize(t *testing.T) {
	s, _ := setup(t, service("app/a"))
	b := NewControlBackend(s, control.Identity{})
	if _, e := b.Execute(context.Background(), control.PlanRequest{Action: "close"}, control.PlanState{}); e != nil {
		t.Fatal(e)
	}
	if lock, e := Acquire(s.workspace.Root); e == nil {
		lock.Close()
		t.Fatal("cleanup prematurely released session lock")
	}
	if e := b.Finalize(); e != nil {
		t.Fatal(e)
	}
	lock, e := Acquire(s.workspace.Root)
	if e != nil {
		t.Fatal(e)
	}
	lock.Close()
}

type gatedControlRunner struct {
	delegate *fakeRunner
	entered  chan struct{}
	target   model.ServiceID
}

func (r gatedControlRunner) Start(ctx context.Context, spec model.Service, out func(string, string)) (runner.Handle, error) {
	if spec.ID == r.target {
		close(r.entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return r.delegate.Start(ctx, spec, out)
}
func TestControlCancellationPreservesCompletedTargetSuccess(t *testing.T) {
	s, f := setup(t, service("app/a"), service("app/b", "app/a"))
	entered := make(chan struct{})
	s.runner = gatedControlRunner{delegate: f, entered: entered, target: "app/b"}
	b := NewControlBackend(s, control.Identity{})
	req := control.PlanRequest{Action: "start", Targets: []string{"app/a", "app/b"}}
	p, e := b.PlanState(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		rows []control.TargetResult
		err  error
	}
	done := make(chan result, 1)
	go func() { rows, e := b.Execute(ctx, req, p); done <- result{rows, e} }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("second launch absent")
	}
	cancel()
	r := <-done
	if !errors.Is(r.err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", r.err)
	}
	states := map[string]string{}
	for _, row := range r.rows {
		states[row.Target] = row.State
	}
	if states["app/a"] != "succeeded" || states["app/b"] != "canceled" {
		t.Fatalf("lost completed success: %+v", r.rows)
	}
}
func TestControlLegacyDockerPlanIncludesActiveTransitiveDependents(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if e := os.Mkdir(bin, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\ncase \"$1\" in info) printf '\"fixture-daemon\"'; exit;; esac\ncase \"$*\" in *' config '*) printf '{\"name\":\"fixture-project\"}';exit;; esac\ncase \"$*\" in *' ps '*) printf '[]';; esac\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	if e := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services:\n  db:\n    image: postgres\n"), 0600); e != nil {
		t.Fatal(e)
	}
	api := service("app/api")
	api.DockerDependsOn = []string{"db"}
	web := service("app/web", "app/api")
	idle := service("app/idle")
	idle.DockerDependsOn = []string{"db"}
	f := &fakeRunner{starts: map[model.ServiceID]int{}, handles: map[model.ServiceID]*fakeHandle{}}
	s, e := New(model.Workspace{Root: root, Projects: []model.Project{{ID: "app", Services: []model.Service{api, web, idle}}}}, f, observe.NewPortProbe(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Shutdown(context.Background())
	if e = s.Start(context.Background(), []model.ServiceID{web.ID}); e != nil {
		t.Fatal(e)
	}
	b := NewControlBackend(s, control.Identity{})
	req := control.PlanRequest{Action: "docker-stop", Targets: []string{"db"}}
	p, e := b.PlanState(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	for _, target := range []string{"db", "app/api", "app/web"} {
		if !slices.Contains(p.Affected, target) {
			t.Fatalf("missing effect %s: %+v", target, p.Affected)
		}
	}
	if slices.Contains(p.Affected, "app/idle") {
		t.Fatal("inactive dependent included")
	}
	rows, e := b.Execute(context.Background(), req, p)
	if e != nil {
		t.Fatal(e)
	}
	for _, target := range p.Affected {
		found := false
		for _, row := range rows {
			if row.Target == target && row.State == "succeeded" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing target result %s: %+v", target, rows)
		}
	}
}

func TestControlCompositeReleaseRetainsScopeAndRejectsChangedEvidence(t *testing.T) {
	s, f := setup(t, service("app/a"), service("app/b", "app/a"))
	b := NewControlBackend(s, control.Identity{})
	req := control.PlanRequest{Action: "release-start", Targets: []string{"app/b"}}
	p, err := b.PlanState(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Affected, "app/a") || !slices.Contains(p.Affected, "app/b") {
		t.Fatal(p.Affected)
	}
	s.mu.Lock()
	s.entries["app/a"].gen++
	s.mu.Unlock()
	if _, err = b.Execute(context.Background(), req, p); err == nil {
		t.Fatal("changed scope executed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.starts["app/a"]+f.starts["app/b"] != 0 {
		t.Fatal("changed plan launched")
	}
}

func TestFinalReviewClosePlanDisclosesScope(t *testing.T) {
	s, _ := setup(t, service("app/a"), service("app/b", "app/a"))
	if err := s.Start(context.Background(), []model.ServiceID{"app/b"}); err != nil {
		t.Fatal(err)
	}
	b := NewControlBackend(s, control.Identity{})
	p, err := b.PlanState(context.Background(), control.PlanRequest{Action: "close"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Affected, "app/a") || !slices.Contains(p.Affected, "app/b") {
		t.Errorf("running owned service scope missing: affected=%v warnings=%v", p.Affected, p.Warnings)
	}
}

func TestControlClosePlanPreservesResourcesAndWholeSessionAuthority(t *testing.T) {
	s, f := setup(t, service("app/a"), service("app/later"), service("app/db"), service("app/observe"), service("app/external"), service("app/ephemeral"))
	s.mu.Lock()
	s.entries["app/db"].spec.Kind = "resource"
	s.entries["app/db"].spec.Resource = &model.ResourceSpec{Lifetime: "persistent", Control: "manage"}
	s.entries["app/observe"].spec.Control = "observe"
	s.entries["app/external"].observed = []model.ProcessIdentity{{PID: 100, CreatedMillis: 1}}
	s.entries["app/ephemeral"].spec.Kind = "resource"
	s.entries["app/ephemeral"].spec.Resource = &model.ResourceSpec{Lifetime: "ephemeral", Control: "manage"}
	s.mu.Unlock()
	if err := s.Start(context.Background(), []model.ServiceID{"app/a"}); err != nil {
		t.Fatal(err)
	}
	b := NewControlBackend(s, control.Identity{})
	p, err := b.PlanState(context.Background(), control.PlanRequest{Action: "close"})
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{"app/db", "app/observe", "app/ephemeral"} {
		if slices.Contains(p.Affected, preserved) {
			t.Fatalf("preserved or inactive resource in stop scope: %v", p.Affected)
		}
	}
	warnings := strings.Join(p.Warnings, "\n")
	for _, disclosure := range []string{"whole session", "Persistent resource app/db", "Observe-only node app/observe", "External observed processes for app/external"} {
		if !strings.Contains(warnings, disclosure) {
			t.Fatalf("missing %q: %v", disclosure, p.Warnings)
		}
	}
	// A pending ephemeral resource is cleanup scope even before a container exists.
	s.mu.Lock()
	s.entries["app/ephemeral"].state = "waiting"
	s.mu.Unlock()
	pending, err := b.PlanState(context.Background(), control.PlanRequest{Action: "close"})
	if err != nil || !slices.Contains(pending.Affected, "app/ephemeral") {
		t.Fatalf("pending ephemeral scope: %v %v", pending.Affected, err)
	}
	s.mu.Lock()
	s.entries["app/ephemeral"].state = "stopped"
	s.mu.Unlock()
	if err := s.Start(context.Background(), []model.ServiceID{"app/later"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Execute(context.Background(), control.PlanRequest{Action: "close"}, p); err != nil {
		t.Fatal(err)
	}
	for _, stopped := range []string{"app/a", "app/later"} {
		if !slices.Contains(f.stops, stopped) {
			t.Fatalf("whole-session close missed %s: %v", stopped, f.stops)
		}
	}
	if slices.Contains(f.stops, "app/observe") || slices.Contains(f.stops, "app/db") {
		t.Fatalf("preserved nodes stopped: %v", f.stops)
	}
}
