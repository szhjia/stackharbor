package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/inventory"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

func liveBrowser(t *testing.T, ns string) (*http.Client, string, context.CancelFunc, <-chan error) {
	t.Helper()
	s, cancel, done := launch(t, Options{Namespace: ns, PortExplicit: true, Port: 0, NoOpen: true})
	u, _ := url.Parse(s)
	u.Fragment = ""
	base := "http://" + u.Host
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	req, _ := http.NewRequest("GET", base+"/api/v1/auth/session", nil)
	req.Header.Set("Origin", base)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	return client, base, cancel, done
}
func scanSSE(t *testing.T, s *bufio.Scanner) Event {
	t.Helper()
	var e Event
	for s.Scan() {
		line := s.Text()
		if line == "" && e.Name != "" {
			return e
		}
		if strings.HasPrefix(line, "id: ") {
			e.ID = strings.TrimPrefix(line, "id: ")
		}
		if strings.HasPrefix(line, "event: ") {
			e.Name = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			e.Data = []byte(strings.TrimPrefix(line, "data: "))
		}
	}
	t.Fatalf("stream ended: %v", s.Err())
	return e
}
func getStream(t *testing.T, client *http.Client, base, path, after string) (*http.Response, *bufio.Scanner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", base+path, nil)
	req.Header.Set("Last-Event-ID", after)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		bytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("SSE %d %s", resp.StatusCode, bytes)
	}
	t.Cleanup(func() { resp.Body.Close() })
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	return resp, scanner
}
func TestDefaultRunLiveInventoryAndSSERecovery(t *testing.T) {
	ns := namespace(t)
	t.Setenv("STACKHARBOR_CACHE_DIR", ns)
	client, base, cancel, done := liveBrowser(t, ns)
	resp, scan := getStream(t, client, base, "/api/v1/events", "")
	reset := scanSSE(t, scan)
	if reset.Name != "reset" || reset.ID == "" {
		t.Fatal("no default bootstrap reset")
	}
	h, err := sessionhost.New(context.Background(), model.Workspace{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	deadline := time.Now().Add(3 * time.Second)
	var inv inventory.Inventory
	var cursor string
	for {
		response, err := client.Get(base + "/api/v1/inventory")
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode == 200 {
			var v struct {
				Data struct {
					inventory.Inventory
					Cursor string `json:"event_cursor"`
				}
			}
			json.NewDecoder(response.Body).Decode(&v)
			inv = v.Data.Inventory
			cursor = v.Data.Cursor
		}
		response.Body.Close()
		if len(inv.Sessions) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("default live discovery missing")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if inv.Sessions[0].Identity.SessionID != h.SessionInfo().SessionID || cursor == "" {
		t.Fatalf("snapshot %#v", inv)
	}
	e := scanSSE(t, scan)
	if e.Name != "inventory" {
		t.Fatal("missing default invalidation")
	}
	resp.Body.Close()
	_, replay := getStream(t, client, base, "/api/v1/events", reset.ID)
	if e := scanSSE(t, replay); e.Name != "inventory" {
		t.Fatalf("Last-Event-ID replay %#v", e)
	}
	_, bootstrap := getStream(t, client, base, "/api/v1/events?after="+url.QueryEscape(reset.ID), "")
	if e := scanSSE(t, bootstrap); e.Name != "inventory" {
		t.Fatal("page reload cursor unsupported")
	}
	_, wrong := getStream(t, client, base, "/api/v1/events?after=other:1", "")
	if e := scanSSE(t, wrong); e.Name != "reset" {
		t.Fatal("generation cursor accepted")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("active SSE delayed shutdown: %v", err)
	}
	c, err := sessionapi.Connect(context.Background(), h.SessionInfo())
	if err != nil {
		t.Fatal("gateway stopped independent session", err)
	}
	defer c.Close()
}
func TestBrowserLogGapCursorAndDisconnect(t *testing.T) {
	ns := namespace(t)
	t.Setenv("STACKHARBOR_CACHE_DIR", ns)
	root := t.TempDir()
	h, err := sessionhost.New(context.Background(), model.Workspace{Root: root, Projects: []model.Project{{ID: "app", Services: []model.Service{{ID: "app/api", Cwd: root, Command: []string{"/bin/sh", "-c", "printf 'first\\n'; sleep 0.2; printf 'after-browser-close\\n'"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	for i := 0; i < 2002; i++ {
		h.Controller().Logs().Append(logs.Entry{ServiceID: "app/api", Text: fmt.Sprint(i)})
	}
	client, base, _, _ := liveBrowser(t, ns)
	sid := h.SessionInfo().SessionID
	resp, scan := getStream(t, client, base, "/api/v1/events?session_id="+sid+"&target=app%2Fapi", "")
	sawGap := false
	var page control.BoundLogPage
	for i := 0; i < 6; i++ {
		e := scanSSE(t, scan)
		if e.Name == "gap" {
			sawGap = true
		}
		if e.Name == "log" {
			if err := json.Unmarshal(e.Data, &page); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if !sawGap || !page.Gap || page.Cursor == "" || page.SessionID != sid {
		t.Fatalf("log loss hidden %+v", page)
	}
	resp.Body.Close()
	if err := h.Controller().Start(context.Background(), []model.ServiceID{"app/api"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries := h.Controller().Logs().Entries(nil)
		found := false
		for _, e := range entries {
			found = found || strings.Contains(e.Text, "after-browser-close")
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("browser disconnect stopped real log capture")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Restart exact workspace; the old cursor must bootstrap the new generation.
	old := page.Cursor
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	h2, err := sessionhost.New(context.Background(), model.Workspace{Root: root, Projects: []model.Project{{ID: "app", Services: []model.Service{{ID: "app/api", Cwd: root, Command: []string{"false"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close(context.Background())
	response, err := client.Get(base + "/api/v1/sessions/" + h2.SessionInfo().SessionID + "/logs?target=app%2Fapi&cursor=" + url.QueryEscape(old))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope sessionapi.Response[control.BoundLogPage]
	json.NewDecoder(response.Body).Decode(&envelope)
	if response.StatusCode != 200 || !envelope.Data.Reset || envelope.Data.SessionID == sid || envelope.Data.NextCursor != 0 {
		t.Fatalf("new session inherited old cursor %+v", envelope)
	}
}
func TestDefaultSSEHeartbeatOutlivesOrdinaryDeadline(t *testing.T) {
	client, base, _, _ := liveBrowser(t, namespace(t))
	resp, scan := getStream(t, client, base, "/api/v1/events", "")
	start := time.Now()
	for scan.Scan() {
		if scan.Text() == ": heartbeat" {
			if time.Since(start) < 10*time.Second {
				t.Fatal("heartbeat timing")
			}
			resp.Body.Close()
			return
		}
	}
	t.Fatalf("stream died before15s heartbeat: %v", scan.Err())
}
