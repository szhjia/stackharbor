package sessionapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

type testBackend struct{ identity control.Identity }

func (b testBackend) Snapshot(context.Context) (control.Snapshot, error) {
	return control.Snapshot{Identity: b.identity}, nil
}
func (b testBackend) Logs(context.Context, string, uint64, int) (control.LogPage, error) {
	return control.LogPage{Entries: []control.LogEntry{}}, nil
}
func (b testBackend) PlanState(context.Context, control.PlanRequest) (control.PlanState, error) {
	return control.PlanState{Fingerprint: "fixed"}, nil
}
func (b testBackend) Execute(context.Context, control.PlanRequest, control.PlanState) ([]control.TargetResult, error) {
	return []control.TargetResult{}, nil
}
func fixture(t *testing.T) (*Server, *Client, *supervisor.Lock, *control.Coordinator) {
	t.Helper()
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	l, err := supervisor.Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	info := l.Info()
	id := control.Identity{WorkspaceID: info.WorkspaceID, SessionID: info.SessionID, ProtocolVersion: ProtocolVersion, Capabilities: []string{"control"}}
	c := control.New(id, testBackend{id}, control.Options{})
	t.Cleanup(func() { c.Close(context.Background()) })
	s, err := Listen(context.Background(), Registration{Info: info, PublishEndpoint: l.PublishEndpoint}, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	client, err := Connect(context.Background(), l.Info())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return s, client, l, c
}
func TestUDSHandshakeIdentityMismatch(t *testing.T) {
	_, _, l, _ := fixture(t)
	for _, mutate := range []func(*supervisor.SessionInfo){func(i *supervisor.SessionInfo) { i.PID++ }, func(i *supervisor.SessionInfo) { i.ProcessCreatedMillis++ }, func(i *supervisor.SessionInfo) { i.SessionID = "wrong" }, func(i *supervisor.SessionInfo) { i.WorkspaceID = "wrong" }, func(i *supervisor.SessionInfo) { i.NamespaceID = "wrong" }} {
		info := l.Info()
		mutate(&info)
		c, err := Connect(context.Background(), info)
		if c != nil {
			c.Close()
		}
		var api *control.APIError
		if !errors.As(err, &api) || api.Code != "identity_conflict" {
			t.Fatalf("identity accepted: %v", err)
		}
	}
}
func TestUnsupportedProtocolReadOnlyMetadata(t *testing.T) {
	_, _, l, _ := fixture(t)
	info := l.Info()
	info.ProtocolVersion++
	if _, err := Connect(context.Background(), info); err == nil {
		t.Fatal("unsupported protocol accepted")
	}
	info.ProtocolVersion = 0
	info.SocketPath = ""
	if _, err := Connect(context.Background(), info); err == nil {
		t.Fatal("legacy record accepted for control")
	}
	all, err := supervisor.ListSessions()
	if err != nil || len(all) != 1 || all[0].Root != info.Root {
		t.Fatalf("metadata unavailable: %v %v", all, err)
	}
}
func TestUnsafeSocketAndDirectoryRejected(t *testing.T) {
	_, _, l, _ := fixture(t)
	info := l.Info()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(info.SocketPath, alias); err != nil {
		t.Fatal(err)
	}
	info.SocketPath = alias
	if _, err := Connect(context.Background(), info); err == nil {
		t.Fatal("symlink socket accepted")
	}
	info = l.Info()
	if err := os.Chmod(filepath.Dir(info.SocketPath), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Connect(context.Background(), info); err == nil {
		t.Fatal("nonprivate directory accepted")
	}
	os.Chmod(filepath.Dir(info.SocketPath), 0700)
	info = l.Info()
	if err := os.Chmod(info.SocketPath, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := Connect(context.Background(), info); err == nil {
		t.Fatal("nonprivate socket accepted")
	}
	st, _ := os.Lstat(info.SocketPath)
	if err := checkOwner(st, os.Getuid()+1); err == nil {
		t.Fatal("foreign owner accepted")
	}
}
func TestLongSocketPathFallback(t *testing.T) {
	cache := filepath.Join(t.TempDir(), strings.Repeat("long", 40))
	if err := os.Mkdir(cache, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STACKHARBOR_CACHE_DIR", cache)
	l, err := supervisor.Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	info := l.Info()
	id := control.Identity{WorkspaceID: info.WorkspaceID, SessionID: info.SessionID, ProtocolVersion: ProtocolVersion}
	c := control.New(id, testBackend{id}, control.Options{})
	defer c.Close(context.Background())
	s, err := Listen(context.Background(), Registration{info, l.PublishEndpoint}, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if len(l.Info().SocketPath) >= 100 {
		t.Fatalf("unsafe path length %d", len(l.Info().SocketPath))
	}
	client, err := Connect(context.Background(), l.Info())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
}
func TestHTTPContract(t *testing.T) {
	_, client, _, _ := fixture(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/v1/plans", `{"action":"close","unknown":1}`, 400},
		{"POST", "/v1/plans", `{"action":"close"} {}`, 400},
		{"POST", "/v1/plans", strings.Repeat(" ", 65537), 400},
		{"PUT", "/v1/plans", `{}`, 405},
		{"POST", "/v1/operations", `{"plan_id":"unknown","idempotency_key":"other:nonce"}`, 409},
		{"GET", "/v1/logs?after=-1", "", 400},
		{"GET", "/v1/logs?limit=501", "", 400},
		{"GET", "/v1/logs?after=%zz", "", 400},
	} {
		resp, err := client.raw(context.Background(), tc.method, tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.status)
		}
	}
	plan, err := client.Plan(context.Background(), control.PlanRequest{Action: "start", Targets: []string{"service"}})
	if err != nil {
		t.Fatal(err)
	}
	req := control.SubmitRequest{PlanID: plan.ID, IdempotencyKey: control.NewIdempotencyKey(plan.ID)}
	op, err := client.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(req)
	retry, err := client.raw(context.Background(), "POST", "/v1/operations", strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	if retry.StatusCode != 202 {
		t.Fatalf("accepted submit retry status=%d", retry.StatusCode)
	}
	retry.Body.Close()
	if op.ID == "" {
		t.Fatal("missing accepted operation")
	}
	ops, err := client.Operations(context.Background())
	if err != nil || len(ops) != 1 {
		t.Fatalf("history: %v %v", ops, err)
	}
	if _, err := client.Operation(context.Background(), op.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Logs(context.Background(), "", 0, 10); err != nil {
		t.Fatal(err)
	}
}
func TestErrorStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{{"plan_capacity", 429}, {"queue_full", 429}, {"plan_expired", 410}, {"idempotency_conflict", 409}, {"session_closed", 409}, {"forbidden", 403}, {"not_found", 404}, {"invalid_request", 400}} {
		if got := StatusCode(&control.APIError{Code: tc.code}); got != tc.status {
			t.Errorf("%s=%d", tc.code, got)
		}
	}
}
func TestServerClosePreservesCoordinatorAndReplacementSocket(t *testing.T) {
	s, _, l, c := fixture(t)
	path := l.Info().SocketPath
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("removed replacement")
	}
	os.Remove(path)
	os.Remove(path + ".saved")
	if _, err := c.Plan(context.Background(), control.PlanRequest{Action: "start", Targets: []string{"service"}}); err != nil {
		t.Fatal("transport close shut down coordinator", err)
	}
}

func TestOccupiedEndpointNeverUnlinked(t *testing.T) {
	s, _, l, c := fixture(t)
	_, err := Listen(context.Background(), Registration{Info: l.Info(), PublishEndpoint: l.PublishEndpoint}, c)
	if err == nil {
		t.Fatal("duplicate listener replaced socket")
	}
	if !samePath(s.inode, l.Info().SocketPath) {
		t.Fatal("occupied socket inode changed")
	}
}
func TestUnreachableMetadataDoesNotDeleteSocket(t *testing.T) {
	_, _, l, _ := fixture(t)
	info := l.Info()
	info.SocketPath = filepath.Join(filepath.Dir(info.SocketPath), "unreachable.sock")
	if err := os.WriteFile(info.SocketPath, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Connect(context.Background(), info); err == nil {
		t.Fatal("regular file endpoint accepted")
	}
	if _, err := os.Stat(info.SocketPath); err != nil {
		t.Fatal("client removed unreachable endpoint")
	}
	os.Remove(info.SocketPath)
}

func TestHTTPPlanCapacityStatus(t *testing.T) {
	_, client, _, c := fixture(t)
	for i := 0; i < 1000; i++ {
		if _, err := c.Plan(context.Background(), control.PlanRequest{Action: "start", Targets: []string{"service"}}); err != nil {
			t.Fatal(err)
		}
	}
	response, err := client.raw(context.Background(), "POST", "/v1/plans", strings.NewReader(`{"action":"start","targets":["service"]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 429 {
		t.Fatalf("plan capacity status=%d", response.StatusCode)
	}
	var envelope Response[control.Plan]
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil || envelope.Error == nil || envelope.Error.Code != "plan_capacity" {
		t.Fatalf("capacity envelope=%+v err=%v", envelope, err)
	}
}
