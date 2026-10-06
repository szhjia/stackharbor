package web

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/inventory"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func gatewayHost(t *testing.T) (*Gateway, *sessionhost.Host, string, string) {
	t.Helper()
	ns := namespace(t)
	t.Setenv("STACKHARBOR_CACHE_DIR", ns)
	root := t.TempDir()
	h, err := sessionhost.New(context.Background(), model.Workspace{Root: root, Projects: []model.Project{{ID: "app", Services: []model.Service{{ID: "app/idle", Cwd: root, Command: []string{"false"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(context.Background()) })
	g := NewGateway("127.0.0.1:16800", Options{Namespace: ns})
	cookie, csrf := authenticated(t, g)
	return g, h, cookie, csrf
}
func data[T any](t *testing.T, bytes []byte) T {
	t.Helper()
	var envelope struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(bytes, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}
func planStop(t *testing.T, g *Gateway, sid, cookie, csrf string) control.Plan {
	t.Helper()
	w := request(g, "POST", "/api/v1/sessions/"+sid+"/plans", `{"action":"stop","targets":["app/idle"]}`, g.host, "http://"+g.host, cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("plan %d %s", w.Code, w.Body.String())
	}
	return data[control.Plan](t, w.Body.Bytes())
}
func TestGatewaySessionDTOAndConstrainedWrites(t *testing.T) {
	g, h, cookie, csrf := gatewayHost(t)
	sid := h.SessionInfo().SessionID
	base := "/api/v1/sessions/" + sid
	for _, route := range []string{"identity", "snapshot", "logs?target=app%2Fidle&after=0&limit=1", "operations"} {
		w := request(g, "GET", base+"/"+route, "", g.host, "", cookie, "")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body.String())
		}
		for _, private := range []string{"socket_path", "namespace_id", "command", "env"} {
			if strings.Contains(w.Body.String(), `"`+private+`"`) {
				t.Fatalf("private %s leaked: %s", private, w.Body.String())
			}
		}
	}
	w := request(g, "GET", "/api/v1/inventory", "", g.host, "", cookie, "")
	if w.Code != 200 || len(data[inventory.Inventory](t, w.Body.Bytes()).Sessions) != 1 {
		t.Fatalf("inventory %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"action":"stop","targets":["app/idle"],"pid":1}`, `{"action":"stop"} {}`, strings.Repeat("x", 65537)} {
		w := request(g, "POST", base+"/plans", body, g.host, "http://"+g.host, cookie, csrf)
		if w.Code != 400 {
			t.Fatalf("unknown/extra/oversized body %d", w.Code)
		}
	}
	for _, q := range []string{"limit=0", "after=-1", "pid=1", "after=1&after=2", "target=%zz"} {
		w := request(g, "GET", base+"/logs?"+q, "", g.host, "", cookie, "")
		if w.Code != 400 {
			t.Fatalf("logs %s: %d", q, w.Code)
		}
	}
	if w := request(g, "DELETE", base+"/snapshot", "", g.host, "http://"+g.host, cookie, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request(g, "GET", base+"/config", "", g.host, "", cookie, ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	p := planStop(t, g, sid, cookie, csrf)
	body, _ := json.Marshal(control.SubmitRequest{PlanID: p.ID, IdempotencyKey: control.NewIdempotencyKey(p.ID)})
	w = request(g, "POST", base+"/operations", string(body), g.host, "http://"+g.host, cookie, csrf)
	if w.Code != 202 {
		t.Fatalf("submit %d %s", w.Code, w.Body.String())
	}
	op := data[control.Operation](t, w.Body.Bytes())
	retry := request(g, "POST", base+"/operations", string(body), g.host, "http://"+g.host, cookie, csrf)
	if retry.Code != 202 || data[control.Operation](t, retry.Body.Bytes()).ID != op.ID {
		t.Fatal("retry changed op")
	}
	body, _ = json.Marshal(control.SubmitRequest{PlanID: p.ID, IdempotencyKey: control.NewIdempotencyKey(p.ID)})
	if w := request(g, "POST", base+"/operations", string(body), g.host, "http://"+g.host, cookie, csrf); w.Code != 409 {
		t.Fatalf("consumed %d", w.Code)
	}
}
func TestGatewayCloseReadsBoundCompletionAfterReopen(t *testing.T) {
	g, h, cookie, csrf := gatewayHost(t)
	sid := h.SessionInfo().SessionID
	base := "/api/v1/sessions/" + sid
	w := request(g, "POST", base+"/plans", `{"action":"close"}`, g.host, "http://"+g.host, cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("close plan %d %s", w.Code, w.Body.String())
	}
	p := data[control.Plan](t, w.Body.Bytes())
	body, _ := json.Marshal(control.SubmitRequest{PlanID: p.ID, IdempotencyKey: control.NewIdempotencyKey(p.ID)})
	w = request(g, "POST", base+"/operations", string(body), g.host, "http://"+g.host, cookie, csrf)
	if w.Code != 202 {
		t.Fatalf("close submit %d %s", w.Code, w.Body.String())
	}
	op := data[control.Operation](t, w.Body.Bytes())
	select {
	case <-h.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("host not closed")
	}
	// Journal terminal publication follows transport teardown; wait for exact evidence.
	deadline := time.Now().Add(time.Second)
	for {
		w = request(g, "GET", base+"/operations/"+op.ID, "", g.host, "", cookie, "")
		if w.Code == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("completion %d %s", w.Code, w.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	reopened, err := sessionhost.New(context.Background(), model.Workspace{Root: h.SessionInfo().Root})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	w = request(g, "GET", base+"/operations/"+op.ID, "", g.host, "", cookie, "")
	if w.Code != 200 {
		t.Fatalf("reopened lookup %d %s", w.Code, w.Body.String())
	}
	got := data[control.Operation](t, w.Body.Bytes())
	if got.ID != op.ID || got.SessionID != sid || got.State != "succeeded" {
		t.Fatalf("wrong close %#v", got)
	}
	if w := request(g, "GET", base+"/operations/unknown", "", g.host, "", cookie, ""); w.Code != 503 {
		t.Fatalf("unknown inferred terminal %d", w.Code)
	}
	if w := request(g, "GET", "/api/v1/sessions/"+reopened.SessionInfo().SessionID+"/operations/"+op.ID, "", g.host, "", cookie, ""); w.Code == 200 {
		t.Fatal("cross-session journal accepted")
	}
}
func sharedInventory(sid string) inventory.Inventory {
	return inventory.Inventory{Sessions: []inventory.Session{{Identity: control.Identity{SessionID: sid}, Available: true}, {Identity: control.Identity{SessionID: "other", WorkspaceID: "other-workspace"}, Root: "/other-workspace", Available: true}}, Resources: []inventory.Resource{{ID: "shared", IdentityKnown: true, References: []inventory.Reference{{SessionID: sid, Nodes: []inventory.NodeReference{{ID: "app/idle", Role: "owner"}}}, {SessionID: "other", WorkspaceID: "other-workspace", Nodes: []inventory.NodeReference{{ID: "other/api", Role: "consumer", State: "running", Ownership: "session"}}}}}}}
}
func TestGatewayRechecksSharedConsumersBeforeSubmit(t *testing.T) {
	g, h, cookie, csrf := gatewayHost(t)
	sid := h.SessionInfo().SessionID
	p := planStop(t, g, sid, cookie, csrf)
	g.options.Collect = func(context.Context) (inventory.Inventory, error) { return sharedInventory(sid), nil }
	body, _ := json.Marshal(control.SubmitRequest{PlanID: p.ID, IdempotencyKey: control.NewIdempotencyKey(p.ID)})
	w := request(g, "POST", "/api/v1/sessions/"+sid+"/operations", string(body), g.host, "http://"+g.host, cookie, csrf)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "other-workspace") || !strings.Contains(w.Body.String(), "other/api") {
		t.Fatalf("consumer not blocked %d %s", w.Code, w.Body.String())
	}
	c, err := g.find(context.Background(), sid)
	if err != nil || c.SessionID != sid {
		t.Fatal(err)
	}
}
func TestGatewayAcceptedRetryIgnoresNewSharedConsumers(t *testing.T) {
	g, h, cookie, csrf := gatewayHost(t)
	sid := h.SessionInfo().SessionID
	p := planStop(t, g, sid, cookie, csrf)
	body, _ := json.Marshal(control.SubmitRequest{PlanID: p.ID, IdempotencyKey: control.NewIdempotencyKey(p.ID)})
	path := "/api/v1/sessions/" + sid + "/operations"
	first := request(g, "POST", path, string(body), g.host, "http://"+g.host, cookie, csrf)
	if first.Code != 202 {
		t.Fatal(first.Body.String())
	}
	op := data[control.Operation](t, first.Body.Bytes())
	g.options.Collect = func(context.Context) (inventory.Inventory, error) { return sharedInventory(sid), nil }
	retry := request(g, "POST", path, string(body), g.host, "http://"+g.host, cookie, csrf)
	if retry.Code != 202 || data[control.Operation](t, retry.Body.Bytes()).ID != op.ID {
		t.Fatalf("accepted retry %d %s", retry.Code, retry.Body.String())
	}
}

func TestGatewayNamespaceDiscoveryIndependentOfEnvironment(t *testing.T) {
	g, h, cookie, _ := gatewayHost(t)
	other := namespace(t)
	t.Setenv("STACKHARBOR_CACHE_DIR", other)
	second, err := sessionhost.New(context.Background(), model.Workspace{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(context.Background())
	w := request(g, "GET", "/api/v1/inventory", "", g.host, "", cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	inv := data[inventory.Inventory](t, w.Body.Bytes())
	if len(inv.Sessions) != 1 || inv.Sessions[0].Identity.SessionID != h.SessionInfo().SessionID {
		t.Fatalf("namespace drift %#v", inv.Sessions)
	}
	if w := request(g, "GET", "/api/v1/sessions/"+h.SessionInfo().SessionID+"/snapshot", "", g.host, "", cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(g, "GET", "/api/v1/sessions/"+second.SessionInfo().SessionID+"/snapshot", "", g.host, "", cookie, ""); w.Code != 404 {
		t.Fatalf("other namespace accessible %d", w.Code)
	}
}

func TestPresentationCacheCannotAuthorizeSubmit(t *testing.T) {
	g, h, cookie, csrf := gatewayHost(t)
	var blocked atomic.Bool
	g.options.Collect = func(context.Context) (inventory.Inventory, error) {
		if blocked.Load() {
			return sharedInventory(h.SessionInfo().SessionID), nil
		}
		return inventory.Inventory{Sessions: []inventory.Session{}, Resources: []inventory.Resource{}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.Start(ctx)
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := g.presentation(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial cache missing")
		}
		time.Sleep(time.Millisecond)
	}
	p := planStop(t, g, h.SessionInfo().SessionID, cookie, csrf)
	blocked.Store(true)
	cached, err := g.presentation(ctx)
	if err != nil || len(cached.Resources) != 0 {
		t.Fatal("fixture cache unexpectedly refreshed")
	}
	body, _ := json.Marshal(control.SubmitRequest{PlanID: p.ID, IdempotencyKey: control.NewIdempotencyKey(p.ID)})
	w := request(g, "POST", "/api/v1/sessions/"+h.SessionInfo().SessionID+"/operations", string(body), g.host, "http://"+g.host, cookie, csrf)
	if w.Code != 409 {
		t.Fatalf("stale cache authorized new side effect %d %s", w.Code, w.Body.String())
	}
}

func TestGatewayPlansDiscloseNonblockingSharedReferences(t *testing.T) {
	for _, action := range []string{"stop", "restart"} {
		for _, consumer := range []string{"stopped", "observe"} {
			t.Run(action+"/"+consumer, func(t *testing.T) {
				g, h, cookie, csrf := gatewayHost(t)
				sid := h.SessionInfo().SessionID
				g.options.Collect = func(context.Context) (inventory.Inventory, error) {
					inv := sharedInventory(sid)
					if consumer == "stopped" {
						inv.Resources[0].References[1].Nodes[0].State = "stopped"
					} else {
						inv.Resources[0].References[1].Nodes[0].Ownership = "external"
					}
					inv.Partial = true
					return inv, nil
				}
				w := request(g, "POST", "/api/v1/sessions/"+sid+"/plans", `{"action":"`+action+`","targets":["app/idle"]}`, g.host, "http://"+g.host, cookie, csrf)
				if w.Code != http.StatusOK {
					t.Fatalf("nonblocking consumer refused %d %s", w.Code, w.Body.String())
				}
				plan := data[control.Plan](t, w.Body.Bytes())
				warnings := strings.Join(plan.Warnings, "\n")
				for _, disclosure := range []string{"Shared resource", "other-workspace", "other/api", "partial"} {
					if !strings.Contains(warnings, disclosure) {
						t.Fatalf("known impact omitted %q: %v", disclosure, plan.Warnings)
					}
				}
			})
		}
	}
}

func TestGatewayNonJSONWritesDoNotCreatePlansOrOperations(t *testing.T) {
	g, h, cookie, csrf := gatewayHost(t)
	base := "/api/v1/sessions/" + h.SessionInfo().SessionID
	nonJSON := func(path, body, contentType string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://"+g.host+path, bytes.NewBufferString(body))
		r.Header.Set("Origin", "http://"+g.host)
		r.Header.Set("Cookie", cookie)
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		w := nonJSON(base+"/plans", `{"action":"stop","targets":["app/idle"]}`, contentType)
		if w.Code != http.StatusBadRequest || len(g.plans) != 0 {
			t.Fatalf("non-JSON plan %q: %d retained=%d", contentType, w.Code, len(g.plans))
		}
		if w := nonJSON(base+"/operations/unknown/cancel", `{}`, contentType); w.Code != http.StatusBadRequest {
			t.Fatalf("non-JSON cancel %q: %d", contentType, w.Code)
		}
	}
	plan := planStop(t, g, h.SessionInfo().SessionID, cookie, csrf)
	body, _ := json.Marshal(control.SubmitRequest{PlanID: plan.ID, IdempotencyKey: control.NewIdempotencyKey(plan.ID)})
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		w := nonJSON(base+"/operations", string(body), contentType)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("non-JSON submit %q: %d %s", contentType, w.Code, w.Body.String())
		}
	}
	operations := request(g, "GET", base+"/operations", "", g.host, "", cookie, "")
	if operations.Code != http.StatusOK || len(data[[]control.Operation](t, operations.Body.Bytes())) != 0 {
		t.Fatalf("rejected writes created operations: %d %s", operations.Code, operations.Body.String())
	}
	if w := request(g, "POST", base+"/operations", string(body), g.host, "http://"+g.host, cookie, csrf); w.Code != http.StatusAccepted {
		t.Fatalf("rejection consumed valid plan: %d %s", w.Code, w.Body.String())
	}
}
