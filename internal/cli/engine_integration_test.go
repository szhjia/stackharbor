package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/szhjia/stackharbor/internal/engine"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

func TestManagedEngineServiceFixture(t *testing.T) {
	if os.Getenv("SH_MANAGED_SERVICE_FIXTURE") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("SH_MANAGED_SERVICE_PID"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("managed service retained log")
	for {
		time.Sleep(time.Second)
	}
}

func TestManagedEngineTaskFixture(t *testing.T) {
	if os.Getenv("SH_MANAGED_TASK_FIXTURE") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("SH_MANAGED_TASK_MARKER"), []byte("completed"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestManagedCLIWorkspaceLifecycleAndDetachment(t *testing.T) {
	t.Setenv("STACKHARBOR_ENGINE_MODE", "managed")
	if !engine.ManagedEnabled() {
		t.Skip("managed desktop mode is unavailable on this platform")
	}
	base, err := os.MkdirTemp("/tmp", "sh-cli-engine-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STACKHARBOR_ENGINE_STATE_DIR", filepath.Join(base, "state"))
	t.Setenv("STACKHARBOR_ENGINE_RUNTIME_DIR", filepath.Join(base, "runtime"))
	t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(base, "cache"))
	t.Setenv("TERM", "xterm-256color")
	bin := filepath.Join(base, "stackharbor")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), time.Minute)
	defer cancelBuild()
	if output, e := exec.CommandContext(buildCtx, "go", "build", "-o", bin, "../../cmd/stackharbor").CombinedOutput(); e != nil {
		t.Fatalf("build real CLI: %v\n%s", e, output)
	}
	logfile, err := os.Create(filepath.Join(base, "engine.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logfile.Close()
	server := exec.Command(bin, "engine", "serve", "--idle-timeout", "1h")
	server.Stdout, server.Stderr = logfile, logfile
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan struct{})
	var serverErr error
	go func() { serverErr = server.Wait(); close(serverDone) }()
	defer func() {
		// Close only this fixture's registered workspaces through their plans.
		if sessions, e := supervisor.ListSessions(); e == nil {
			for _, s := range sessions {
				if strings.HasPrefix(s.Root, base+string(os.PathSeparator)) {
					_, _, _ = managedCLIRun(t, "kill", "--root", s.Root, "--yes", "--timeout", "5s")
				}
			}
		}
		_, _, _ = managedCLIRun(t, "engine", "stop")
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			_ = server.Process.Kill()
			<-serverDone
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if client, e := engine.Connect(context.Background()); e == nil {
			_ = client.Close()
			break
		}
		select {
		case <-serverDone:
			data, _ := os.ReadFile(logfile.Name())
			t.Fatalf("engine exited: %v\n%s", serverErr, data)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("engine did not become available")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, _, errs := managedCLIRun(t, "engine", "status", "--json"); code != 0 {
		t.Fatal(code, errs)
	}
	if code, _, errs := managedCLIRun(t, "engine", "ensure", "--json"); code != 0 {
		t.Fatal(code, errs)
	}
	root1, pidfile1 := managedCLIWorkspace(t, base, "one")
	root2, pidfile2 := managedCLIWorkspace(t, base, "two")
	root3, pidfile3 := managedCLIWorkspace(t, base, "registration-only")
	managedCLIConsole(t, "run", root3, "q")
	if _, err = os.Stat(pidfile3); !os.IsNotExist(err) {
		t.Fatal("run registration started services", err)
	}
	if _, found, e := supervisor.SessionForRoot(root3); e != nil || !found {
		t.Fatal("run did not retain its session after terminal exit", found, e)
	}
	if code, out, errs := managedCLIRun(t, "kill", "--root", root3, "--yes"); code != 0 {
		t.Fatal(code, out, errs)
	}
	// Run the new startup entry point as a separate client so its exit proves
	// that the service is owned by the engine rather than the invoking CLI.
	previewCode, preview, previewErr := managedCLIRun(t, "--daemon", "--root", root2, "--dry-run", "--json")
	if previewCode != 0 || !strings.Contains(preview, "app/service/api") {
		t.Fatal("daemon preview did not report the registered service", previewCode, preview, previewErr)
	}
	if _, found, e := supervisor.SessionForRoot(root2); e != nil || found {
		t.Fatal("daemon preview opened a session", found, e)
	}
	startCtx, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStart()
	out, startErr := exec.CommandContext(startCtx, bin, "-d", "--root", root2, "--yes", "--json").CombinedOutput()
	if startErr != nil || !strings.Contains(string(out), `"action":"release-all-start"`) || !strings.Contains(string(out), `"state":"succeeded"`) {
		_, logs, _ := managedCLIRun(t, "logs", "--root", root2, "--target", "app/service/api")
		t.Fatalf("daemon startup failed: %v\n%s\n%s", startErr, out, logs)
	}
	daemonPID := managedCLIPID(t, pidfile2)
	if code, out, errs := managedCLIRun(t, "--daemon", "--root", root2, "--yes", "--json"); code != 0 || !strings.Contains(out, `"state":"succeeded"`) {
		t.Fatal("daemon startup did not reuse its session", code, out, errs)
	}
	if current := managedCLIPID(t, pidfile2); current != daemonPID || syscall.Kill(current, 0) != nil {
		t.Fatalf("repeating daemon startup replaced or stopped the service: %d -> %d", daemonPID, current)
	}
	if _, err = os.Stat(filepath.Join(root2, "task-done")); !os.IsNotExist(err) {
		t.Fatal("daemon startup ran an unrelated task", err)
	}
	taskRoot, taskPIDFile := managedCLIWorkspace(t, base, "task-only")
	if code, out, errs := managedCLIRun(t, "task", "run", "app/task/inspect", "--root", taskRoot, "--json"); code != 0 || !strings.Contains(out, `"state":"succeeded"`) {
		t.Fatal("managed task did not use engine operation", code, out, errs)
	}
	taskPID := managedCLIPID(t, taskPIDFile)
	if _, err = os.Stat(filepath.Join(taskRoot, "task-done")); err != nil || syscall.Kill(taskPID, 0) != nil {
		t.Fatal("task client stopped its engine dependency", err)
	}
	if code, out, errs := managedCLIRun(t, "task", "run", "app/service/api", "--root", taskRoot, "--json"); code != 2 || !strings.Contains(errs, "not a registered task") {
		t.Fatal("task command accepted a service", code, out, errs)
	}
	if code, out, errs := managedCLIRun(t, "kill", "--root", taskRoot, "--yes"); code != 0 {
		t.Fatal(code, out, errs)
	}
	if code, out, errs := managedCLIRun(t, "start", "--root", root1, "--target", "app/service/api", "--dry-run", "--json"); code != 0 || !strings.Contains(out, "release-start") {
		t.Fatal(code, out, errs)
	}
	if _, err = os.Stat(pidfile1); !os.IsNotExist(err) {
		t.Fatal("dry-run launched a service", err)
	}
	info1, found, err := supervisor.SessionForRoot(root1)
	if err != nil || !found || info1.OwnerKind != "engine" || info1.ProtocolVersion == 0 {
		t.Fatal("start did not register a managed session", info1, found, err)
	}
	if code, out, errs := managedCLIRun(t, "start", "--root", root1, "--target", "app/service/api", "--yes", "--json"); code != 0 {
		t.Fatal(code, out, errs)
	}
	pid1, pid2 := managedCLIPID(t, pidfile1), managedCLIPID(t, pidfile2)
	info2, found, err := supervisor.SessionForRoot(root2)
	if err != nil || !found || info1.SessionID == info2.SessionID || info1.PID != info2.PID {
		t.Fatal("workspaces do not share one independently identified engine", info1, info2, err)
	}
	managedCLIBrowserCatalogue(t, root1)
	if code, _, _ := managedCLIRun(t, "engine", "stop"); code == 0 {
		t.Fatal("engine stop terminated active workspaces")
	}
	for _, key := range []string{"q", "\x03"} {
		managedCLIConsole(t, "attach", root1, key)
	}
	current, found, err := supervisor.SessionForRoot(root1)
	if err != nil || !found || current.SessionID != info1.SessionID || syscall.Kill(pid1, 0) != nil || syscall.Kill(pid2, 0) != nil {
		t.Fatal("terminal exit changed workspace ownership", current, found, err)
	}
	client, err := sessionapi.Connect(context.Background(), current)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := client.Logs(context.Background(), "", 0, 500)
	_ = client.Close()
	if err != nil || len(logs.Entries) == 0 {
		t.Fatal("reconnect lost logs", logs, err)
	}
	if code, out, errs := managedCLIRun(t, "kill", "--root", root1, "--yes", "--json"); code != 0 {
		t.Fatal(code, out, errs)
	}
	if syscall.Kill(pid2, 0) != nil {
		t.Fatal("closing first workspace stopped second")
	}
	if _, found, err = supervisor.SessionForRoot(root2); err != nil || !found {
		t.Fatal("closing first workspace released second", err)
	}
	if code, out, errs := managedCLIRun(t, "kill", "--root", root2, "--yes"); code != 0 {
		t.Fatal(code, out, errs)
	}
	if code, out, errs := managedCLIRun(t, "engine", "stop", "--json"); code != 0 {
		t.Fatal(code, out, errs)
	}
	// Status is a read-only connection attempt and cannot relaunch the engine.
	deadline = time.Now().Add(5 * time.Second)
	for {
		if client, e := engine.Connect(context.Background()); e != nil {
			break
		} else {
			_ = client.Close()
		}
		if time.Now().After(deadline) {
			t.Fatal("engine did not stop")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, _, _ := managedCLIRun(t, "engine", "status"); code == 0 {
		t.Fatal("status relaunched a stopped engine")
	}
}

func managedCLIBrowserCatalogue(t *testing.T, root string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan int, 1)
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("managed gateway cleanup timed out")
			}
		}
	}()
	var errs bytes.Buffer
	go func() {
		defer writer.Close()
		done <- Run(ctx, []string{"web", "--port", "0", "--no-open"}, strings.NewReader(""), writer, &errs)
	}()
	address, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		cancel()
		<-done
		joined = true
		t.Fatal("managed browser gateway did not start", err, errs.String())
	}
	address = strings.TrimRight(strings.TrimSpace(address), "/")
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: time.Second}
	response, err := client.Get(address + "/api/v1/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("browser auth failed", response.StatusCode)
	}
	response, err = client.Get(address + "/api/v1/workspaces")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(data), root) || !strings.Contains(string(data), `"workspaces"`) {
		t.Fatal("managed browser catalogue did not use engine handler", response.StatusCode, string(data), err)
	}
	cancel()
	select {
	case code := <-done:
		joined = true
		if code != 0 {
			t.Fatal("browser gateway exit", code, errs.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("browser gateway did not detach")
	}
}

func managedCLIRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code := Run(ctx, args, strings.NewReader(""), &out, &errs)
	return code, out.String(), errs.String()
}
func managedCLIWorkspace(t *testing.T, base, name string) (string, string) {
	t.Helper()
	root := filepath.Join(base, name)
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	pidfile := filepath.Join(root, "pid")
	config := fmt.Sprintf("version: 2\nproject: {id: app, name: App}\ncontext: {cwd: .}\nservices:\n  api:\n    run: {command: [%q, '-test.run=^TestManagedEngineServiceFixture$']}\n    env: {SH_MANAGED_SERVICE_FIXTURE: '1', SH_MANAGED_SERVICE_PID: %q}\n    stop: {timeout_seconds: 1}\ntasks:\n  inspect:\n    effect: read-only\n    policy: always\n    run: {command: [%q, '-test.run=^TestManagedEngineTaskFixture$']}\n    env: {SH_MANAGED_TASK_FIXTURE: '1', SH_MANAGED_TASK_MARKER: %q}\n    requires: [{node: app/service/api, condition: started}]\n", os.Args[0], pidfile, os.Args[0], filepath.Join(root, "task-done"))
	if err := os.WriteFile(filepath.Join(root, "stackharbor.yaml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return root, pidfile
}
func managedCLIPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, _ := strconv.Atoi(string(data))
			if pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("service did not start", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func managedCLIConsole(t *testing.T, command, root, key string) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err = pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 100}); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	go func() {
		buffer := make([]byte, 1024)
		if n, _ := master.Read(buffer); n > 0 {
			close(ready)
		}
		_, _ = io.Copy(io.Discard, master)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan int, 1)
	var errs bytes.Buffer
	go func() { done <- Run(ctx, []string{command, "--root", root}, slave, slave, &errs) }()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("console never rendered")
	}
	if _, err = master.Write([]byte(key)); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatal("attach failed", code, errs.String())
		}
	case <-ctx.Done():
		t.Fatal("attach failed to detach")
	}
}
