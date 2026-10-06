package tests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/inventory"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"go.yaml.in/yaml/v3"
)

type webPTY struct {
	cmd    *exec.Cmd
	master *os.File
	root   string
	port   int
	client *sessionapi.Client
	info   supervisor.SessionInfo
	env    []string
	waited bool
}

func ownedWebPTY(t *testing.T, cache string) *webPTY {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	port := freePort(t)
	doc := map[string]any{"version": 1, "project": map[string]string{"id": "fixture", "name": "HTTP terminal fixture"}, "services": map[string]any{"web": map[string]any{
		"run": map[string]any{"command": []string{os.Args[0]}}, "env": map[string]string{"SH_HTTP_FIXTURE": "1", "SH_HTTP_PORT": fmt.Sprint(port)},
		"ports": []map[string]any{{"name": "http", "port": port}}, "ready": map[string]any{"http": fmt.Sprintf("http://127.0.0.1:%d/", port), "timeout_seconds": 5}, "stop": map[string]any{"timeout_seconds": 1}}}}
	raw, _ := yaml.Marshal(doc)
	if err := os.WriteFile(filepath.Join(root, "stackharbor.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return startWebPTY(t, root, cache, port)
}
func startWebPTY(t *testing.T, root, cache string, port int) *webPTY {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	pty.Setsize(master, &pty.Winsize{Rows: 35, Cols: 120})
	before, _ := term.GetState(slave.Fd())
	cmd := exec.Command(binary, "--root", root)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "NO_COLOR=1", "STACKHARBOR_CACHE_DIR="+cache)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	fixture := &webPTY{cmd: cmd, master: master, root: root, port: port, env: cmd.Env}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, master)
	t.Cleanup(func() {
		if fixture.client != nil {
			fixture.client.Close()
		}
		if !fixture.waited {
			cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(35 * time.Second):
				cmd.Process.Kill()
				<-done
			}
		}
		after, _ := term.GetState(slave.Fd())
		master.Close()
		slave.Close()
		if !reflect.DeepEqual(before, after) {
			t.Errorf("terminal settings not restored: before=%#v after=%#v", before, after)
		}
	})
	wait(t, func() bool {
		infos, e := supervisor.ListSessionsIn(cache)
		if e != nil {
			return false
		}
		for _, info := range infos {
			if info.PID == cmd.Process.Pid && info.SocketPath != "" {
				fixture.info = info
				return true
			}
		}
		return false
	})
	fixture.client, err = sessionapi.Connect(context.Background(), fixture.info)
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}
func (f *webPTY) cli(t *testing.T, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(binary, append(args, "--root", f.root, "--json")...)
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI %v: %v %s", args, err, out)
	}
	return out
}
func health(t *testing.T, port int) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("health", resp.StatusCode)
	}
}
func endPTY(t *testing.T, f *webPTY, op control.Operation) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f.cmd.Wait() }()
	select {
	case err := <-done:
		f.waited = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(35 * time.Second):
		t.Fatal("session close timed out")
	}
	record, err := sessionhost.ReadCompletion(f.info.CacheDir, f.info.SessionID)
	if err != nil || record.ID != op.ID || record.State != "succeeded" {
		t.Fatalf("completion %+v %v", record, err)
	}
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", f.port), time.Second); err == nil {
		conn.Close()
		t.Fatal("owned application listener survived close")
	}
}

func TestWebControlTwoPTYWorkspacesWithoutGateway(t *testing.T) {
	cache := t.TempDir()
	cache, _ = filepath.EvalSymlinks(cache)
	t.Setenv("STACKHARBOR_CACHE_DIR", cache)
	a, b := ownedWebPTY(t, cache), ownedWebPTY(t, cache)
	a.cli(t, "start", "--target", "fixture/web", "--yes")
	b.cli(t, "start", "--target", "fixture/web", "--yes")
	health(t, a.port)
	health(t, b.port)
	// A stale concrete plan fails after another CLI changes relevant node identity/state.
	p, err := a.client.Plan(context.Background(), control.PlanRequest{Action: "restart", Targets: []string{"fixture/web"}})
	if err != nil {
		t.Fatal(err)
	}
	a.cli(t, "stop", "--target", "fixture/web", "--yes")
	_, err = a.client.Submit(context.Background(), control.SubmitRequest{PlanID: p.ID, IdempotencyKey: control.NewIdempotencyKey(p.ID)})
	var api *control.APIError
	if !errors.As(err, &api) || sessionapi.StatusCode(api) != 409 {
		t.Fatal("stale plan did not return409", err)
	}
	a.cli(t, "start", "--target", "fixture/web", "--yes")
	// Legacy lock metadata is observable but cannot mutate through the new gateway.
	legacyRoot := t.TempDir()
	legacy, err := supervisor.Acquire(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	gateway := exec.Command(binary, "web", "--port", "0", "--no-open")
	gateway.Env = a.env
	var output capture
	gateway.Stdout = writerCapture{&output}
	gateway.Stderr = writerCapture{&output}
	if err = gateway.Start(); err != nil {
		t.Fatal(err)
	}
	gatewayWaited := false
	t.Cleanup(func() {
		if !gatewayWaited {
			gateway.Process.Signal(syscall.SIGTERM)
			gateway.Wait()
		}
	})
	var u *url.URL
	wait(t, func() bool {
		for _, line := range strings.Split(output.text(), "\n") {
			if strings.HasPrefix(line, "http://127.0.0.1:") {
				u, _ = url.Parse(line)
				return u != nil && u.Fragment == "" && u.Host != ""
			}
		}
		return false
	})
	origin := "http://" + u.Host
	req, _ := http.NewRequest("GET", origin+"/api/v1/auth/session", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var auth sessionapi.Response[struct {
		CSRF string `json:"csrf"`
	}]
	json.NewDecoder(resp.Body).Decode(&auth)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, auth.Error)
	}
	cookie := resp.Cookies()[0]
	get := func(path string) (*http.Response, []byte) {
		r, _ := http.NewRequest("GET", origin+path, nil)
		r.AddCookie(cookie)
		v, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		data, _ := io.ReadAll(v.Body)
		v.Body.Close()
		return v, data
	}
	_, data := get("/")
	if !bytes.Contains(data, []byte("/assets/index-")) {
		t.Fatal("real bundled UI missing")
	}
	wait(t, func() bool {
		_, data := get("/api/v1/inventory")
		var v sessionapi.Response[inventory.Inventory]
		json.Unmarshal(data, &v)
		ready, old := 0, false
		for _, s := range v.Data.Sessions {
			if s.Identity.SessionID == a.info.SessionID || s.Identity.SessionID == b.info.SessionID {
				if s.Available {
					ready++
				}
			}
			if s.Identity.SessionID == legacy.Info().SessionID {
				old = !s.Available && s.Error != nil && s.Error.Code == "unsupported_protocol"
			}
		}
		return ready == 2 && old
	})
	// Read only response headers then disconnect. Accepted operation continues independently.
	p, err = a.client.Plan(context.Background(), control.PlanRequest{Action: "restart", Targets: []string{"fixture/web"}})
	if err != nil {
		t.Fatal(err)
	}
	// Obtain the authoritative plan through gateway so it retains private forwarding metadata.
	planReq, _ := http.NewRequest("POST", origin+"/api/v1/sessions/"+a.info.SessionID+"/plans", strings.NewReader(`{"action":"restart","targets":["fixture/web"]}`))
	planReq.Header.Set("Origin", origin)
	planReq.Header.Set("Content-Type", "application/json")
	planReq.Header.Set("X-CSRF-Token", auth.Data.CSRF)
	planReq.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(planReq)
	if err != nil {
		t.Fatal(err)
	}
	var plan sessionapi.Response[control.Plan]
	json.NewDecoder(resp.Body).Decode(&plan)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, plan.Error)
	}
	payload, _ := json.Marshal(control.SubmitRequest{PlanID: plan.Data.ID, IdempotencyKey: control.NewIdempotencyKey(plan.Data.ID)})
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "POST /api/v1/sessions/%s/operations HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s=%s\r\nX-CSRF-Token: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", a.info.SessionID, u.Host, origin, cookie.Name, cookie.Value, auth.Data.CSRF, len(payload), payload)
	disconnected, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if disconnected.StatusCode != 202 {
		conn.Close()
		t.Fatal(disconnected.StatusCode)
	}
	conn.Close()
	var recovered control.Operation
	wait(t, func() bool {
		ops, e := a.client.Operations(context.Background())
		if e != nil {
			return false
		}
		for _, op := range ops {
			if op.Action == "restart" && op.CreatedAt.After(plan.Data.ExpiresAt.Add(-60*time.Second)) {
				recovered = op
				return op.State == "succeeded"
			}
		}
		return false
	})
	resp, data = get("/api/v1/sessions/" + a.info.SessionID + "/operations/" + recovered.ID)
	if resp.StatusCode != 200 || !bytes.Contains(data, []byte("succeeded")) {
		t.Fatal("accepted result not recoverable", resp.StatusCode, string(data))
	}
	if err = gateway.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err = gateway.Wait(); err != nil {
		t.Fatal(err)
	}
	gatewayWaited = true
	health(t, a.port)
	health(t, b.port)
	a.cli(t, "status")
	b.cli(t, "status")
	for _, f := range []*webPTY{a, b} {
		op := submitRemoteClose(t, f.client)
		endPTY(t, f, op)
	}
	t.Logf("two real PTYs; standalone CLI; stale plan409; legacy read-only; TCP submission disconnect recovered operation %s; stopped gateway preserved healthy apps; remote close restored terminals", recovered.ID)
}

type writerCapture struct{ c *capture }

func (w writerCapture) Write(p []byte) (int, error) { w.c.add(p); return len(p), nil }

func TestWebControlHarborCafeLifecycle(t *testing.T) {
	if os.Getenv("STACKHARBOR_CAFE_TEST") != "1" {
		t.Skip("explicit original Harbor Cafe fixed-port verification")
	}
	for _, port := range []int{18281, 18282} {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("Harbor Cafe port %d is occupied; no release/takeover authorized: %v", port, err)
		}
		l.Close()
	}
	cache := t.TempDir()
	cache, _ = filepath.EvalSymlinks(cache)
	root, _ := filepath.Abs("../examples/harbor-cafe")
	root, _ = filepath.EvalSymlinks(root)
	f := startWebPTY(t, root, cache, 18282)
	f.cli(t, "start", "--target", "orders/service/web", "--yes")
	check := func() {
		for _, port := range []int{18281, 18282} {
			r, e := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
			if e != nil {
				t.Fatal(e)
			}
			data, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if r.StatusCode != 200 || strings.TrimSpace(string(data)) != "ready" {
				t.Fatal("Cafe health", port, r.StatusCode, string(data))
			}
		}
		r, e := http.Get("http://127.0.0.1:18282/")
		if e != nil {
			t.Fatal(e)
		}
		data, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if !bytes.Contains(data, []byte("Harbor espresso")) {
			t.Fatal("counter did not fetch real menu", string(data))
		}
	}
	check()
	f.cli(t, "restart", "--target", "menu/service/api", "--yes")
	check()
	f.cli(t, "stop", "--target", "menu/service/api", "--yes")
	for _, port := range []int{18281, 18282} {
		c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if e == nil {
			c.Close()
			t.Fatal("Cafe stop left listener", port)
		}
	}
	f.cli(t, "start", "--target", "orders/service/web", "--yes")
	check()
	op := submitRemoteClose(t, f.client)
	endPTY(t, f, op)
	t.Log("Original Harbor Cafe argv/registrations; ports18281/18282 verified free before launch; start/restart dependent/stop/restart/close; both healthz200 and real three-drink menu")
}

func TestWebControlPersistentComposeFixture(t *testing.T) {
	if os.Getenv("STACKHARBOR_DOCKER_TEST") != "1" {
		t.Skip("explicit isolated Docker Compose fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	project := fmt.Sprintf("sh-task10-%d-%d", os.Getpid(), time.Now().UnixNano())
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	cache := t.TempDir()
	cache, _ = filepath.EvalSymlinks(cache)
	t.Setenv("STACKHARBOR_CACHE_DIR", cache)
	compose := filepath.Join(root, "compose.yaml")
	doc := "services:\n  store:\n    image: redis:7.4.2-alpine\n    command: [redis-server, --appendonly, 'yes']\n    volumes: [fixture-data:/data]\n    healthcheck:\n      test: [CMD, redis-cli, ping]\n      interval: 1s\n      timeout: 1s\n      retries: 10\nvolumes:\n  fixture-data: {}\n"
	if err := os.WriteFile(compose, []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
	docker := func(args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "-f", compose, "-p", project}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture Docker %v: %v %s", args, err, out)
		}
		return out
	}
	// This project name and manifest are generated by this test; cleanup removes
	// only its own containers/network/volume. No global prune or developer data.
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cleanup, "docker", "compose", "-f", compose, "-p", project, "down", "--volumes", "--remove-orphans")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("fixture-only teardown %s: %v %s", project, err, out)
		}
	})
	node := model.Service{ID: "resource/store", ProjectID: "infra", Kind: "resource", Cwd: root, Name: "Isolated persistent Redis", Resource: &model.ResourceSpec{Adapter: "compose", File: compose, Project: project, Service: "store", Available: "healthy", Lifetime: "persistent", Control: "managed"}}
	host, err := sessionhost.New(ctx, model.Workspace{Version: 2, Root: root, Projects: []model.Project{{ID: "infra", Services: []model.Service{node}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	client, err := sessionapi.Connect(ctx, host.SessionInfo())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	operate := func(action string) {
		t.Helper()
		p, e := client.Plan(ctx, control.PlanRequest{Action: action, Targets: []string{"resource/store"}})
		if e != nil {
			t.Fatal(e)
		}
		op, e := client.Submit(ctx, control.SubmitRequest{PlanID: p.ID, IdempotencyKey: control.NewIdempotencyKey(p.ID)})
		if e != nil {
			t.Fatal(e)
		}
		wait(t, func() bool {
			v, e := client.Operation(ctx, op.ID)
			if e != nil {
				return false
			}
			if v.State == "failed" {
				t.Fatalf("fixture operation failed %+v", v)
			}
			return v.State == "succeeded"
		})
	}
	operate("start")
	docker("exec", "-T", "store", "redis-cli", "set", "stackharbor-task10", "retained")
	id := strings.TrimSpace(string(docker("ps", "-q", "store")))
	p, err := client.Plan(ctx, control.PlanRequest{Action: "restart", Targets: []string{"resource/store"}})
	if err != nil {
		t.Fatal(err)
	}
	docker("up", "-d", "--no-deps", "--force-recreate", "--wait", "--wait-timeout", "20", "store")
	replacement := strings.TrimSpace(string(docker("ps", "-q", "store")))
	if id == replacement {
		t.Fatal("fixture container not replaced")
	}
	wait(t, func() bool {
		snap, e := client.Snapshot(ctx)
		if e != nil {
			return false
		}
		for _, c := range snap.Containers {
			if strings.HasPrefix(c.ID, replacement) || strings.HasPrefix(replacement, c.ID) {
				return true
			}
		}
		return false
	})
	_, err = client.Submit(ctx, control.SubmitRequest{PlanID: p.ID, IdempotencyKey: control.NewIdempotencyKey(p.ID)})
	var api *control.APIError
	if !errors.As(err, &api) || sessionapi.StatusCode(api) != 409 {
		t.Fatal("recreated container accepted stale plan", err)
	}
	operate("stop")
	operate("start")
	operate("restart")
	if err = host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if value := strings.TrimSpace(string(docker("exec", "-T", "store", "redis-cli", "get", "stackharbor-task10"))); value != "retained" {
		t.Fatal("persistent data was lost", value)
	}
	if current := strings.TrimSpace(string(docker("ps", "-q", "store"))); current == "" {
		t.Fatal("session close stopped persistent container")
	}
	t.Logf("isolated Compose project=%s; healthy start/stop/start/restart; force-recreated container rejected stale plan409; close preserves running resource + named-volume value; exact fixture down --volumes cleanup", project)
}

func TestWebControlBinaryRunsWithoutNode(t *testing.T) {
	cmd := exec.Command(binary, "web", "--port", "0", "--no-open")
	cmd.Env = append(os.Environ(), "PATH="+t.TempDir(), "STACKHARBOR_CACHE_DIR="+t.TempDir())
	var output capture
	cmd.Stdout = writerCapture{&output}
	cmd.Stderr = writerCapture{&output}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		if err := cmd.Wait(); err != nil {
			t.Error("no-Node gateway shutdown", err, output.text())
		}
	})
	var origin string
	wait(t, func() bool {
		for _, line := range strings.Split(output.text(), "\n") {
			if strings.HasPrefix(line, "http://127.0.0.1:") {
				u, _ := url.Parse(line)
				if u != nil {
					origin = "http://" + u.Host
					return true
				}
			}
		}
		return false
	})
	resp, err := http.Get(origin)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Contains(data, []byte("/assets/index-")) {
		t.Fatal("embedded console absent without Node", resp.StatusCode, string(data))
	}
	t.Log("empty PATH gateway served actual embedded index, Node/Go unavailable in runtime path")
}
