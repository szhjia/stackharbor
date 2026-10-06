package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

func TestCLIProcessFixture(t *testing.T) {
	if os.Getenv("SH_CLI_FIXTURE") != "1" {
		return
	}
	if f := os.Getenv("SH_CLI_PID_FILE"); f != "" {
		_ = os.WriteFile(f, []byte("started"), 0600)
	}
	for {
		time.Sleep(time.Second)
	}
}
func controlHost(t *testing.T, ready bool) *sessionhost.Host {
	t.Helper()
	t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	root := t.TempDir()
	n := model.Service{ID: "app/web", ProjectID: "app", Cwd: root, Command: []string{os.Args[0], "-test.run=^TestCLIProcessFixture$"}, Env: map[string]string{"SH_CLI_FIXTURE": "1"}, Stop: model.StopPolicy{TimeoutSeconds: 1}}
	if ready {
		n.Ready = &model.ReadyProbe{TCP: "127.0.0.1:1", TimeoutSeconds: 10}
	}
	n.Ports = []model.Port{{Number: freeCLIPort(t)}}
	h, err := sessionhost.New(context.Background(), model.Workspace{Root: root, Projects: []model.Project{{ID: "app", Services: []model.Service{n}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		if e := h.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	return h
}
func controlRun(t *testing.T, h *sessionhost.Host, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	args = append(args, "--root", h.SessionInfo().Root)
	code := Run(context.Background(), args, strings.NewReader(""), &out, &errs)
	return code, out.String(), errs.String()
}
func TestControlCommandsWithoutWeb(t *testing.T) {
	h := controlHost(t, false)
	// No TCP server is created: the only endpoint is the host's private Unix socket.
	if !strings.HasSuffix(h.SessionInfo().SocketPath, ".sock") {
		t.Fatal(h.SessionInfo())
	}
	for _, args := range [][]string{{"status", "--json"}, {"start", "--target", "app/web", "--yes", "--json"}, {"restart", "--target", "app/web", "--yes"}, {"logs", "--target", "app/web", "--json"}, {"operations", "--json"}, {"stop", "--target", "app/web", "--yes"}} {
		if code, out, err := controlRun(t, h, args...); code != 0 {
			t.Fatalf("%v code=%d out=%s err=%s", args, code, out, err)
		}
	}
}
func TestNonInteractiveMutationRequiresYes(t *testing.T) {
	h := controlHost(t, false)
	for _, action := range []string{"start", "stop", "restart", "release", "kill"} {
		args := []string{action}
		if action != "kill" {
			args = append(args, "--target", "app/web")
		}
		if action == "release" {
			args = append(args, "--port", strconv.Itoa(h.Controller().Snapshot().Services[0].Spec.Ports[0].Number))
		}
		if code, _, err := controlRun(t, h, args...); code != 2 || !strings.Contains(err, "--yes") {
			t.Fatalf("%s code=%d err=%s", action, code, err)
		}
	}
	if len(h.Coordinator().Operations()) != 0 {
		t.Fatal("confirmation mutated")
	}
}
func TestDryRunNeverMutates(t *testing.T) {
	h := controlHost(t, false)
	for _, action := range []string{"start", "stop", "restart", "kill"} {
		args := []string{action, "--dry-run", "--json"}
		if action != "kill" {
			args = append(args, "--target", "app/web")
		}
		code, out, err := controlRun(t, h, args...)
		var plan control.Plan
		if code != 0 || json.Unmarshal([]byte(out), &plan) != nil || plan.ID == "" {
			t.Fatalf("%s code=%d out=%s err=%s", action, code, out, err)
		}
	}
	if len(h.Coordinator().Operations()) != 0 {
		t.Fatal("dry run mutated")
	}
}
func TestTimeoutReportsOperationID(t *testing.T) {
	h := controlHost(t, true)
	code, out, err := controlRun(t, h, "start", "--target", "app/web", "--yes", "--json", "--timeout", "30ms")
	var op control.Operation
	if code != 3 || json.Unmarshal([]byte(out), &op) != nil || op.ID == "" || !(op.State == "running" || op.State == "queued") {
		t.Fatalf("code=%d out=%s err=%s", code, out, err)
	}
	if code, _, err = controlRun(t, h, "operations", "--id", op.ID, "--json"); code != 3 {
		t.Fatalf("query code=%d err=%s", code, err)
	}
}
func TestReleaseDoesNotStartService(t *testing.T) {
	h := controlHost(t, false)
	if code, out, err := controlRun(t, h, "release", "--target", "app/web", "--port", strconv.Itoa(h.Controller().Snapshot().Services[0].Spec.Ports[0].Number), "--yes"); code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out, err)
	}
	for _, n := range h.Controller().Snapshot().Services {
		if n.State != "stopped" {
			t.Fatal("release started", n.State)
		}
	}
}
func TestLegacyKillFallback(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	lock, e := supervisor.Acquire(root)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"kill", "--root", root, "--yes"}, strings.NewReader(""), &out, &errs)
	if code != 1 || !strings.Contains(errs.String(), "current process") {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errs.String())
	}
}
func TestKillReadsExactCompletion(t *testing.T) {
	h := controlHost(t, false)
	if code, out, err := controlRun(t, h, "kill", "--yes", "--json"); code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out, err)
	}
	info := h.SessionInfo()
	op, e := sessionhost.ReadCompletion(info.CacheDir, info.SessionID)
	if e != nil || op.State != "succeeded" {
		t.Fatal(op, e)
	}
	if _, e := readCompletedOperation(info.Root, op.ID); e != nil {
		t.Fatal("completion lookup", e)
	}
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"operations", "--root", info.Root, "--id", op.ID, "--json"}, strings.NewReader(""), &out, &errs); code != 0 {
		t.Fatalf("post-close query code=%d out=%s err=%s", code, out.String(), errs.String())
	}
}
func TestControlRootBeforeCommandAndStaleConfigStop(t *testing.T) {
	h := controlHost(t, false)
	if e := os.WriteFile(filepath.Join(h.SessionInfo().Root, "stackharbor.yaml"), []byte("invalid: ["), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(h.SessionInfo().Root, "stackharbor.workspace.yaml"), []byte("invalid: ["), 0600); e != nil {
		t.Fatal(e)
	}
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"--root", h.SessionInfo().Root, "stop", "--target", "app/web", "--yes"}, strings.NewReader(""), &out, &errs); code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errs.String())
	}
}
func TestLogsFollowHonorsContext(t *testing.T) {
	h := controlHost(t, false)
	ctx, c := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer c()
	var out, errs bytes.Buffer
	if code := Run(ctx, []string{"logs", "--root", h.SessionInfo().Root, "--follow", "--json"}, strings.NewReader(""), &out, &errs); code != 0 {
		t.Fatalf("code=%d err=%s", code, errs.String())
	}
}

// Reserve a free declared port without retaining any TCP listener.
func freeCLIPort(t *testing.T) int {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}

func TestStatusAllWithoutRoot(t *testing.T) {
	h := controlHost(t, false)
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"status", "--all", "--json"}, strings.NewReader(""), &out, &errs)
	if code != 0 || !strings.Contains(out.String(), h.SessionInfo().SessionID) {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errs.String())
	}
}
func TestCloseQuerySurvivesReopenedSession(t *testing.T) {
	h := controlHost(t, false)
	root := h.SessionInfo().Root
	if code, _, err := controlRun(t, h, "kill", "--yes"); code != 0 {
		t.Fatal(code, err)
	}
	old := h.Coordinator().Operations()[0]
	newer, e := sessionhost.New(context.Background(), model.Workspace{Root: root})
	if e != nil {
		t.Fatal(e)
	}
	defer newer.Close(context.Background())
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"operations", "--root", root, "--id", old.ID, "--json"}, strings.NewReader(""), &out, &errs); code != 0 || !strings.Contains(out.String(), old.SessionID) {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errs.String())
	}
}
func TestTaskRunRegistersAndRemoteCloseCleansDependencies(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	root := t.TempDir()
	config := fmt.Sprintf(`version: 2
project: {id: app, name: App}
context: {cwd: .}
services:
  dependency:
    run: {command: [%q, '-test.run=^TestCLIProcessFixture$']}
    env: {SH_CLI_FIXTURE: '1'}
    stop: {timeout_seconds: 1}
tasks:
  inspect:
    effect: read-only
    policy: always
    run: {command: [%q, '-test.run=^TestCLIProcessFixture$']}
    env: {SH_CLI_FIXTURE: '1'}
    requires: [{node: app/service/dependency, condition: started}]
`, os.Args[0], os.Args[0])
	if e := os.WriteFile(filepath.Join(root, "stackharbor.yaml"), []byte(config), 0600); e != nil {
		t.Fatal(e)
	}
	var taskOut, taskErr bytes.Buffer
	done := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		done <- Run(ctx, []string{"task", "run", "app/task/inspect", "--root", root}, strings.NewReader(""), &taskOut, &taskErr)
	}()
	canonical, _ := filepath.EvalSymlinks(root)
	var info supervisor.SessionInfo
	deadline := time.Now().Add(5 * time.Second)
	for {
		found, e := supervisor.ListSessions()
		if e != nil {
			t.Fatal(e)
		}
		for _, s := range found {
			if s.Root == canonical {
				info = s
			}
		}
		if info.SocketPath != "" {
			client, e := sessionapi.Connect(context.Background(), info)
			if e == nil {
				snap, e := client.Snapshot(context.Background())
				client.Close()
				if e == nil {
					running := 0
					for _, n := range snap.Nodes {
						if n.State == "running" || n.State == "started" || n.State == "running-task" {
							running++
						}
					}
					if running == 2 {
						break
					}
				}
			}
		}
		select {
		case code := <-done:
			t.Fatalf("task returned early code=%d out=%s err=%s", code, taskOut.String(), taskErr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("task never registered running dependencies")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"kill", "--root", root, "--yes"}, strings.NewReader(""), &out, &errs); code != 0 {
		t.Fatalf("close code=%d out=%s err=%s", code, out.String(), errs.String())
	}
	select {
	case code := <-done:
		if code != 1 {
			t.Fatalf("remote cancellation code=%d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("task survived remote close")
	}
	sessions, e := supervisor.ListSessions()
	if e != nil || len(sessions) != 0 {
		t.Fatal("task left registration", sessions, e)
	}
	if op, e := sessionhost.ReadCompletion(info.CacheDir, info.SessionID); e != nil || op.State != "succeeded" {
		t.Fatal("dependency cleanup failed", op, e)
	}
}

func TestLegacyCLISessionFixture(t *testing.T) {
	if os.Getenv("SH_LEGACY_CLI_FIXTURE") != "1" {
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	lock, e := supervisor.Acquire(os.Getenv("SH_LEGACY_CLI_ROOT"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(os.Getenv("SH_LEGACY_CLI_READY"), []byte("ready"), 0600); e != nil {
		t.Fatal(e)
	}
	<-ctx.Done()
	if e = lock.Close(); e != nil {
		t.Fatal(e)
	}
}
func TestLegacyKillSignalsVerifiedOwner(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	root := t.TempDir()
	ready := filepath.Join(root, "ready")
	bin := filepath.Join(t.TempDir(), "stackharbor")
	data, e := os.ReadFile(os.Args[0])
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(bin, data, 0700); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(bin, "-test.run=^TestLegacyCLISessionFixture$")
	cmd.Env = append(os.Environ(), "SH_LEGACY_CLI_FIXTURE=1", "SH_LEGACY_CLI_ROOT="+root, "SH_LEGACY_CLI_READY="+ready)
	var child bytes.Buffer
	cmd.Stdout = &child
	cmd.Stderr = &child
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer cmd.Process.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(ready); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("legacy fixture not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"kill", "--root", root, "--yes", "--json", "--timeout", "5s"}, strings.NewReader(""), &out, &errs); code != 0 || !strings.Contains(out.String(), `"legacy":true`) {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errs.String())
	}
	if e = cmd.Wait(); e != nil {
		t.Fatal("legacy cleanup", e, child.String())
	}
}
