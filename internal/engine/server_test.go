package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/process"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/web"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func privateTemp(t *testing.T) string {
	t.Helper()
	temp, err := os.MkdirTemp("/private/tmp", "sh-engine-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(temp) })
	p, err := filepath.EvalSymlinks(temp)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(p, 0700); err != nil {
		t.Fatal(err)
	}
	return p
}
func testServer(t *testing.T) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dir := privateTemp(t)
	store, err := readState(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := process.NewReader()
	s := &Server{ctx: ctx, cancel: cancel, state: store, hosts: map[string]*hosted{}, gateways: map[string]*web.Gateway{}, budget: logs.NewBudget(32 << 20), lastUse: time.Now(), observeWake: make(chan struct{}, 1), owners: base, metrics: base, ports: observe.NewPortProbe()}
	t.Cleanup(func() {
		cancel()
		c, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		s.mu.Lock()
		hosts := []*sessionhost.Host{}
		for _, h := range s.hosts {
			hosts = append(hosts, h.host)
		}
		s.mu.Unlock()
		for _, h := range hosts {
			_ = h.Close(c)
		}
	})
	return s
}
func registered(t *testing.T) string {
	t.Helper()
	root := privateTemp(t)
	text := "version: 2\nproject: {id: demo, name: Demo}\ncontext: {cwd: .}\nservices:\n  api:\n    run: {command: [/bin/sleep, '60']}\n"
	if err := os.WriteFile(filepath.Join(root, "stackharbor.yaml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestOpenDoesNotStartAndNamespaceEnvironmentIsolated(t *testing.T) {
	s := testServer(t)
	root := registered(t)
	nsA, nsB := privateTemp(t), privateTemp(t)
	a, err := s.Open(context.Background(), OpenRequest{Root: root, Namespace: nsA, Environment: []string{"PATH=/usr/bin:/bin", "MARKER=one"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Open(context.Background(), OpenRequest{Root: root, Namespace: nsB, Environment: []string{"PATH=/usr/bin:/bin", "MARKER=two"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.OwnerKind != "engine" || a.ProtocolVersion == 0 || a.SessionID == b.SessionID || a.PID != b.PID {
		t.Fatalf("invalid managed identities: %+v %+v", a, b)
	}
	again, err := s.Open(context.Background(), OpenRequest{Root: root, Namespace: nsA, Environment: []string{"MARKER=overwrite"}})
	if err != nil || again.SessionID != a.SessionID {
		t.Fatal("existing session replaced", err)
	}
	for _, tc := range []struct{ ns, marker string }{{nsA, "one"}, {nsB, "two"}} {
		h := s.hosts[workspaceKey(tc.ns, root)].host
		snapshot := h.Controller().Snapshot()
		if len(snapshot.Services) != 1 || len(snapshot.Services[0].Owned) != 0 || snapshot.Services[0].State != "stopped" {
			t.Fatalf("opening started a process: %+v", snapshot.Services)
		}
		if snapshot.Services[0].Spec.Env["MARKER"] != tc.marker {
			t.Fatal("environment crossed workspace boundaries")
		}
	}
	if os.Getenv("MARKER") == "one" || os.Getenv("MARKER") == "two" {
		t.Fatal("global environment mutated")
	}
}

func TestReloadInvalidatesPlanAndKeepsSession(t *testing.T) {
	s := testServer(t)
	root, ns := registered(t), privateTemp(t)
	info, err := s.Open(context.Background(), OpenRequest{Root: root, Namespace: ns, Environment: []string{"PATH=/usr/bin:/bin", "MARKER=old"}})
	if err != nil {
		t.Fatal(err)
	}
	h := s.hosts[workspaceKey(ns, root)].host
	plan, err := h.Coordinator().Plan(context.Background(), control.PlanRequest{Action: "all-start"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.reload(context.Background(), OpenRequest{Root: root, Namespace: ns, Environment: []string{"PATH=/usr/bin:/bin", "MARKER=new"}})
	if err != nil || next.SessionID != info.SessionID {
		t.Fatal("reload replaced session", err)
	}
	if h.Controller().Snapshot().Services[0].Spec.Env["MARKER"] != "new" {
		t.Fatal("environment not reloaded")
	}
	if _, err = h.Coordinator().Submit(context.Background(), control.SubmitRequest{PlanID: plan.ID, IdempotencyKey: "old"}); err == nil {
		t.Fatal("old environment plan remained executable")
	}
}

func TestCatalogueAndModernMetadataProtectSharedEngine(t *testing.T) {
	s := testServer(t)
	root, ns := registered(t), privateTemp(t)
	info, err := s.Open(context.Background(), OpenRequest{Root: root, Namespace: ns, Environment: []string{"PATH=/usr/bin:/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/workspaces", nil)
	r.Header.Set("X-StackHarbor-Namespace", ns)
	s.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), info.SessionID) {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "socket_path") || strings.Contains(w.Body.String(), "cache_dir") {
		t.Fatal("private transport exposed in browser catalogue")
	}
	if list := s.catalogue(privateTemp(t)); len(list) != 0 {
		t.Fatal("namespace leaked")
	}
}

func TestRecoveryJournalSurvivesUncleanRestartAndRefusesReplay(t *testing.T) {
	s := testServer(t)
	root, ns := registered(t), privateTemp(t)
	key := workspaceKey(ns, root)
	s.state.entries[key] = &workspaceState{Record: WorkspaceRecord{Root: root, Namespace: ns, State: "offline"}, Attempts: map[string]attempt{}}
	journal := workspaceJournal{s.state, key}
	id, err := journal.Intent(model.Service{ID: "demo/service/api", Env: map[string]string{"SECRET": "must-not-persist"}})
	if err != nil {
		t.Fatal(err)
	}
	birth, err := process.CreatedMillis(context.Background(), int32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.Launched(id, model.ProcessIdentity{PID: int32(os.Getpid()), CreatedMillis: birth - 1000}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.state.directory, "workspaces.json"))
	if err != nil || strings.Contains(string(raw), "must-not-persist") {
		t.Fatal("environment persisted", err)
	}
	loaded, err := readState(s.state.directory)
	if err != nil {
		t.Fatal(err)
	}
	s.state = loaded
	if _, err = s.Open(context.Background(), OpenRequest{Root: root, Namespace: ns, Environment: []string{}}); err == nil || !strings.Contains(err.Error(), "recovery") {
		t.Fatal("unclean workspace reopened", err)
	}
	r := httptest.NewRequest("GET", "/engine/v1/recovery?root="+root+"&namespace="+ns, nil)
	plan, err := s.recoveryPlan(r, root, ns)
	if err != nil || len(plan.Processes) != 1 || plan.Processes[0].State != "identity_changed" {
		t.Fatal(plan, err)
	}
	body := fmt.Sprintf(`{"root":%q,"namespace":%q,"action":"stop","plan_id":%q}`, root, ns, plan.PlanID)
	request := httptest.NewRequest("POST", "/engine/v1/recovery", strings.NewReader(body))
	if _, err = s.recovery(httptest.NewRecorder(), request); err == nil {
		t.Fatal("reused PID could be signalled")
	}
}

func TestStateRejectsCorruptAndSymlinkedCatalogue(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		dir := privateTemp(t)
		path := filepath.Join(dir, "workspaces.json")
		if symlink {
			target := filepath.Join(privateTemp(t), "other")
			_ = os.WriteFile(target, []byte(`{}`), 0600)
			_ = os.Symlink(target, path)
		} else {
			_ = os.WriteFile(path, []byte(`{"version":99,"workspaces":{}}`), 0600)
		}
		if _, err := readState(dir); err == nil {
			t.Fatal("unsafe state accepted")
		}
		if _, err := os.Lstat(path); err != nil {
			t.Fatal("bad state was removed")
		}
	}
}

func TestEngineSingletonReadOnlyConnectAndIdleExit(t *testing.T) {
	runtimeDir, stateDir := privateTemp(t), privateTemp(t)
	t.Setenv("STACKHARBOR_ENGINE_RUNTIME_DIR", runtimeDir)
	t.Setenv("STACKHARBOR_ENGINE_STATE_DIR", stateDir)
	missing := filepath.Join(runtimeDir, "missing")
	t.Setenv("STACKHARBOR_ENGINE_RUNTIME_DIR", missing)
	if c, err := Connect(context.Background()); err == nil {
		c.Close()
		t.Fatal("missing engine connected")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("status created runtime directory")
	}
	t.Setenv("STACKHARBOR_ENGINE_RUNTIME_DIR", runtimeDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{RuntimeDir: runtimeDir, StateDir: stateDir, IdleTimeout: 250 * time.Millisecond})
	}()
	var c *Client
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		c, err = Connect(context.Background())
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c == nil {
		t.Fatal("engine did not publish endpoint")
	}
	defer c.Close()
	if c.Endpoint.BuildVersion != buildinfo.Version {
		t.Fatal(c.Endpoint)
	}
	if err := Serve(ctx, Options{RuntimeDir: runtimeDir, StateDir: stateDir}); err == nil {
		t.Fatal("second engine started")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle engine did not exit")
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "engine.json")); !os.IsNotExist(err) {
		t.Fatal("endpoint retained after normal exit")
	}
}

func TestLocalAPIRejectsMutationOnWrongMethod(t *testing.T) {
	s := testServer(t)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/engine/v1/open", nil))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	var data map[string]any
	if json.Unmarshal(w.Body.Bytes(), &data) != nil || data["error"] == nil {
		t.Fatal(w.Body.String())
	}
}

func TestBackgroundOwnershipCadencePreservesInteractiveRefresh(t *testing.T) {
	s := testServer(t)
	if s.ownershipCadence() != 5*time.Second || s.cadence() != 10*time.Second {
		t.Fatal("background observations exceed their budgeted cadence")
	}
	s.foreground.Add(1)
	if s.ownershipCadence() != time.Second || s.cadence() != 2*time.Second {
		t.Fatal("foreground refresh is not responsive")
	}
}

func TestResourceMutationIntentSurvivesBeforeAnyLocalExec(t *testing.T) {
	s := testServer(t)
	root, ns := registered(t), privateTemp(t)
	key := workspaceKey(ns, root)
	s.state.entries[key] = &workspaceState{Record: WorkspaceRecord{Root: root, Namespace: ns, State: "offline"}, Attempts: map[string]attempt{}}
	journal := workspaceJournal{s.state, key}
	if err := journal.BeforeSubmit(control.PlanRequest{Action: "all-start", Targets: []string{"resource/db"}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := readState(s.state.directory)
	if err != nil {
		t.Fatal(err)
	}
	entry := loaded.entries[key]
	if !entry.RecoveryRequired || entry.Record.State != "recovery_required" || len(entry.Attempts) != 0 {
		t.Fatal("resource-only startup would be replayed", entry)
	}
	journal.Closed(control.Operation{State: "failed"})
	loaded, err = readState(s.state.directory)
	if err != nil || !loaded.entries[key].RecoveryRequired {
		t.Fatal("failed cleanup erased resource intent", err)
	}
}

func TestSlowWorkspaceOpenDoesNotBlockOtherControls(t *testing.T) {
	s := testServer(t)
	rootA, rootB, ns := registered(t), registered(t), privateTemp(t)
	info, err := s.Open(context.Background(), OpenRequest{Root: rootA, Namespace: ns, Environment: []string{"PATH=/usr/bin:/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := filepath.Join(privateTemp(t), "entered"), filepath.Join(privateTemp(t), "release")
	script := filepath.Join(privateTemp(t), "shell")
	text := fmt.Sprintf("#!/bin/sh\nprintf entered > %s\nwhile [ ! -f %s ]; do /bin/sleep 0.01; done\nprintf '[\"PATH=/usr/bin:/bin\"]' >&3\n", entered, release)
	if err = os.WriteFile(script, []byte(text), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", script)
	done := make(chan error, 1)
	go func() {
		_, err := s.Open(context.Background(), OpenRequest{Root: rootB, Namespace: ns, Shell: true})
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(entered); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer os.WriteFile(release, nil, 0600)
	response := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/engine/v1/identity", nil))
		response <- w.Code
	}()
	select {
	case code := <-response:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("workspace B startup blocked engine controls")
	}
	again, err := s.Open(context.Background(), OpenRequest{Root: rootA, Namespace: ns, Environment: []string{}})
	if err != nil || again.SessionID != info.SessionID {
		t.Fatal("unrelated workspace unavailable", err)
	}
	_ = os.WriteFile(release, nil, 0600)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("workspace B failed to finish")
	}
}
