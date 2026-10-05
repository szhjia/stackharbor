package tests

import (
	"bytes"
	"fmt"
	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"go.yaml.in/yaml/v3"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var binary string

func TestMain(m *testing.M) {
	if os.Getenv("SH_HTTP_FIXTURE") == "1" {
		if extra := os.Getenv("SH_HTTP_EXTRA"); extra != "" {
			go func() {
				if e := http.ListenAndServe("127.0.0.1:"+extra, nil); e != nil {
					panic(e)
				}
			}()
		}

		fmt.Println("http fixture running")
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ready") })
		if e := http.ListenAndServe("127.0.0.1:"+os.Getenv("SH_HTTP_PORT"), nil); e != nil {
			panic(e)
		}
		return
	}

	if os.Getenv("SH_TUI_FIXTURE") == "1" {
		os.WriteFile(os.Getenv("SH_PID_FILE"), []byte(strconv.Itoa(os.Getpid())), 0600)
		fmt.Println("fixture-log-ready 中文")
		for {
			time.Sleep(time.Second)
		}
	}
	dir, e := os.MkdirTemp("", "stackharbor-cli-test-")
	if e != nil {
		panic(e)
	}
	binary = os.Getenv("SH_TEST_BINARY")
	if binary != "" {
		code := m.Run()
		os.RemoveAll(dir)
		os.Exit(code)
	}
	binary = filepath.Join(dir, "stackharbor")
	cmd := exec.Command("go", "build", "-o", binary, "../cmd/stackharbor")
	if out, e := cmd.CombinedOutput(); e != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type capture struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (c *capture) add(b []byte) { c.mu.Lock(); defer c.mu.Unlock(); c.b.Write(b) }
func (c *capture) text() string { c.mu.Lock(); defer c.mu.Unlock(); return c.b.String() }
func wait(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for terminal fixture")
}
func terminalFixture(t *testing.T, ports ...int) (*exec.Cmd, *os.File, *os.File, *capture, string) {
	return terminalFixtureWithColors(t, false, ports...)
}

func terminalFixtureWithColors(t *testing.T, colors bool, ports ...int) (*exec.Cmd, *os.File, *os.File, *capture, string) {
	t.Helper()
	root := t.TempDir()
	pidfile := filepath.Join(root, "pid")
	doc := map[string]any{"version": 1, "project": map[string]string{"id": "fixture", "name": "Terminal fixture"}, "services": map[string]any{"web": map[string]any{"run": map[string]any{"command": []string{os.Args[0]}}, "env": map[string]string{"SH_TUI_FIXTURE": "1", "SH_PID_FILE": pidfile}, "stop": map[string]any{"timeout_seconds": 1}}}}
	if len(ports) > 0 {
		web := doc["services"].(map[string]any)["web"].(map[string]any)
		web["ports"] = []map[string]any{{"name": "http", "port": ports[0]}}
	}
	b, _ := yaml.Marshal(doc)
	os.WriteFile(filepath.Join(root, "stackharbor.yaml"), b, 0600)
	master, slave, e := pty.Open()
	if e != nil {
		t.Fatal(e)
	}
	pty.Setsize(master, &pty.Winsize{Rows: 35, Cols: 120})
	cmd := exec.Command(binary, "--root", root)
	env := os.Environ()
	if colors {
		env = nil
		for _, item := range os.Environ() {
			if !strings.HasPrefix(item, "NO_COLOR=") {
				env = append(env, item)
			}
		}
		env = append(env, "COLORTERM=truecolor")
	} else {
		env = append(env, "NO_COLOR=1")
	}
	cmd.Env = append(env, "TERM=xterm-256color", "STACKHARBOR_CACHE_DIR="+filepath.Join(root, "cache"))
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	before, _ := term.GetState(slave.Fd())
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	c := &capture{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, e := master.Read(buf)
			if n > 0 {
				c.add(buf[:n])
			}
			if e != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		after, _ := term.GetState(slave.Fd())
		master.Close()
		slave.Close()
		if !reflect.DeepEqual(before, after) {
			t.Errorf("terminal settings not restored: before=%#v after=%#v", before, after)
		}
	})
	wait(t, func() bool { return bytes.Contains([]byte(c.text()), []byte("Dashboard")) })
	return cmd, master, slave, c, pidfile
}
func TestPTYQuitStopsFixtureAndRestoresTerminal(t *testing.T) {
	cmd, master, _, c, pidfile := terminalFixture(t)
	if _, e := os.Stat(pidfile); e == nil {
		t.Fatal("fixture automatically started")
	}
	master.Write([]byte("js"))
	wait(t, func() bool { _, e := os.Stat(pidfile); return e == nil })
	wait(t, func() bool { return bytes.Contains([]byte(c.text()), []byte("fixture-log-ready")) })
	master.Write([]byte("?"))
	wait(t, func() bool { return bytes.Contains([]byte(c.text()), []byte("PageUp")) })
	master.Write([]byte("q"))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e, c.text())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("quit did not finish")
	}
	b, _ := os.ReadFile(pidfile)
	pid, _ := strconv.Atoi(string(b))
	if e := syscall.Kill(pid, 0); e != syscall.ESRCH {
		t.Fatal("owned fixture survived", pid, e)
	}
	if path := os.Getenv("SH_TERMINAL_RECORD"); path != "" {
		os.WriteFile(path, []byte(c.text()), 0600)
	}
}
func TestSIGTERMStopsOwnedServices(t *testing.T) {
	cmd, master, _, _, pidfile := terminalFixture(t)
	master.Write([]byte("S"))
	wait(t, func() bool { _, e := os.Stat(pidfile); return e == nil })
	cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("SIGTERM cleanup blocked")
	}
	b, _ := os.ReadFile(pidfile)
	pid, _ := strconv.Atoi(string(b))
	if e := syscall.Kill(pid, 0); e != syscall.ESRCH {
		t.Fatal("owned fixture survived SIGTERM", pid, e)
	}
}

func TestPTYThemeFollowsTerminalBackground(t *testing.T) {
	cmd, master, _, c, _ := terminalFixtureWithColors(t, true)
	wait(t, func() bool { return strings.Contains(c.text(), "\x1b]11;?") })
	master.Write([]byte("\x1b]11;rgb:ffff/ffff/ffff\x1b\\"))
	wait(t, func() bool { return strings.Contains(c.text(), "48;2;240;242;244") })
	before := len(c.text())
	master.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\"))
	wait(t, func() bool { return strings.Contains(c.text()[before:], "48;2;39;49;61") })
	master.Write([]byte("q"))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("theme preview did not exit")
	}
}

func TestPTYQuitAfterRecoverableActionError(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	cmd, master, _, capture, pidfile := terminalFixture(t, listener.Addr().(*net.TCPAddr).Port)
	master.Write([]byte("js"))
	wait(t, func() bool {
		s := capture.text()
		return strings.Contains(s, "Release conflicting ports") || strings.Contains(s, "protected") || strings.Contains(s, "Unavailable")
	})
	if _, e := os.Stat(pidfile); e == nil {
		t.Fatal("external port bypassed")
	}
	master.Write([]byte("q"))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal("successful quit retained old action failure", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("quit blocked")
	}
	connection, e := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if e != nil {
		t.Fatal("external listener was stopped")
	}
	connection.Close()
}

func TestPTYConflictCancelTakeoverAndRestart(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	external := exec.Command(os.Args[0])
	external.Env = append(os.Environ(), "SH_HTTP_FIXTURE=1", fmt.Sprintf("SH_HTTP_PORT=%d", port))
	if e = external.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { external.Process.Kill(); external.Wait() }()
	wait(t, func() bool {
		c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if e == nil {
			c.Close()
		}
		return e == nil
	})
	cmd, master, _, capture, pidfile := terminalFixture(t, port)
	master.Write([]byte("js"))
	wait(t, func() bool { return strings.Contains(capture.text(), "Release conflicting ports") })
	master.Write([]byte("n"))
	time.Sleep(100 * time.Millisecond)
	if e := syscall.Kill(external.Process.Pid, 0); e != nil {
		t.Fatal("cancel killed external", e)
	}
	if _, e := os.Stat(pidfile); e == nil {
		t.Fatal("cancel started replacement")
	}
	before := len(capture.text())
	master.Write([]byte("s"))
	wait(t, func() bool { return strings.Contains(capture.text()[before:], "Release conflicting ports") })
	master.Write([]byte("y"))
	wait(t, func() bool { _, e := os.Stat(pidfile); return e == nil })
	first, _ := os.ReadFile(pidfile)
	wait(t, func() bool { return strings.Contains(capture.text(), "Operation complete") })
	master.Write([]byte("r"))
	wait(t, func() bool { next, _ := os.ReadFile(pidfile); return len(next) > 0 && string(next) != string(first) })
	master.Write([]byte("q"))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e, capture.text())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("quit blocked")
	}
	pid, _ := strconv.Atoi(string(first))
	if e := syscall.Kill(pid, 0); e != syscall.ESRCH {
		t.Fatal("old managed instance survived restart", pid, e)
	}
}
