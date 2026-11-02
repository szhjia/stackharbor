package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

func TestPTYSessionRegistryDuplicateAndIndependentExit(t *testing.T) {
	cache := t.TempDir()
	env := append(os.Environ(), "TERM=xterm-256color", "TERM_PROGRAM=StackHarborTest", "STACKHARBOR_CACHE_DIR="+cache)
	start := func(root string) (*exec.Cmd, *os.File, *capture) {
		t.Helper()
		cmd := exec.Command(binary, "--root", root)
		cmd.Env = env
		master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 35, Cols: 120})
		if err != nil {
			t.Fatal(err)
		}
		c := &capture{}
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := master.Read(buf)
				if n > 0 {
					c.add(buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()
		t.Cleanup(func() { cmd.Process.Kill(); master.Close() })
		return cmd, master, c
	}
	rootA, rootB := t.TempDir(), t.TempDir()
	rootA, _ = filepath.EvalSymlinks(rootA)
	rootB, _ = filepath.EvalSymlinks(rootB)
	a, ttyA, outA := start(rootA)
	wait(t, func() bool { return strings.Contains(outA.text(), "Dashboard") })
	b, ttyB, outB := start(rootB)
	wait(t, func() bool { return strings.Contains(outB.text(), "Dashboard") })
	list := func() []struct {
		Root string `json:"root"`
		PID  int    `json:"pid"`
		TTY  string `json:"tty"`
	} {
		t.Helper()
		cmd := exec.Command(binary, "sessions", "--json")
		cmd.Env = env
		cmd.Dir = t.TempDir() // No workspace or terminal is required for listing.
		data, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		var result []struct {
			Root string `json:"root"`
			PID  int    `json:"pid"`
			TTY  string `json:"tty"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err, string(data))
		}
		return result
	}
	all := list()
	if len(all) != 2 {
		t.Fatal("independent sessions not listed", all)
	}
	for _, s := range all {
		if s.Root != rootA && s.Root != rootB || !strings.HasPrefix(s.TTY, "/dev/") || s.PID != a.Process.Pid && s.PID != b.Process.Pid {
			t.Fatalf("incorrect session owner: %+v", s)
		}
	}
	duplicate, dupTTY, dupOut := start(rootA)
	wait(t, func() bool { return strings.Contains(dupOut.text(), "Close this session and reopen here?") })
	dupTTY.Write([]byte("n\r"))
	done := make(chan error, 1)
	go func() { done <- duplicate.Wait() }()
	select {
	case err := <-done:
		if err == nil || duplicate.ProcessState.ExitCode() != 2 {
			t.Fatal("duplicate workspace opened another manager", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("duplicate session did not finish")
	}
	wait(t, func() bool {
		text := dupOut.text()
		return strings.Contains(text, rootA) && strings.Contains(text, "PID: "+strconv.Itoa(a.Process.Pid)) && strings.Contains(text, "TTY:") && strings.Contains(text, "sessions --focus")
	})
	if len(list()) != 2 {
		t.Fatal("duplicate launch changed existing sessions")
	}
	quit := func(cmd *exec.Cmd, tty io.Writer) {
		t.Helper()
		tty.Write([]byte("q"))
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("session did not quit")
		}
	}
	quit(a, ttyA)
	remaining := list()
	if len(remaining) != 1 || remaining[0].PID != b.Process.Pid {
		t.Fatal("closing A affected B or left stale session", remaining)
	}
	quit(b, ttyB)
	if all := list(); len(all) != 0 {
		t.Fatal("closed sessions still listed", all)
	}
	cmd := exec.Command(binary, "sessions", "--focus", strconv.Itoa(a.Process.Pid))
	cmd.Env = env
	data, err := cmd.CombinedOutput()
	if err == nil || !bytes.Contains(data, []byte("No active StackHarbor session")) {
		t.Fatal("released PID accepted for window location", string(data), err)
	}
}

// Every process is reaped once; completion synchronizes ProcessState reads and
// terminal-state assertions even when a recovery is interrupted by a signal.
type recoveryPTY struct {
	cmd    *exec.Cmd
	master *os.File
	output *capture
	done   chan struct{}
}

func startRecoveryPTY(t *testing.T, args, env []string) *recoveryPTY {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := pty.Setsize(master, &pty.Winsize{Rows: 35, Cols: 120}); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	f := &recoveryPTY{cmd: cmd, master: master, output: &capture{}, done: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go io.Copy(f.output, master)
	go func() { _ = cmd.Wait(); close(f.done) }()
	t.Cleanup(func() {
		select {
		case <-f.done:
		default:
			cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-f.done:
		case <-time.After(35 * time.Second):
			cmd.Process.Kill()
			<-f.done
		}
		after, err := term.GetState(slave.Fd())
		master.Close()
		slave.Close()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Errorf("recovery left terminal settings changed: before=%#v after=%#v err=%v", before, after, err)
		}
	})
	return f
}

func (f *recoveryPTY) exit(t *testing.T, code int) {
	t.Helper()
	select {
	case <-f.done:
		if got := f.cmd.ProcessState.ExitCode(); got != code {
			t.Fatalf("recovery exit=%d want=%d output=%s", got, code, f.output.text())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("recovery did not exit: %s", f.output.text())
	}
}

func TestPTYRecoveryClosesOnlySelectedSessionAndReloadsConfig(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "StackHarborTest")
	cache := t.TempDir()
	a, b := ownedWebPTY(t, cache), ownedWebPTY(t, cache)
	a.cli(t, "start", "--target", "fixture/web", "--yes")
	b.cli(t, "start", "--target", "fixture/web", "--yes")
	f := startRecoveryPTY(t, []string{"--root", a.root}, a.env)
	wait(t, func() bool { return strings.Contains(f.output.text(), "Close this session and reopen here?") })
	for _, text := range []string{"PID: " + strconv.Itoa(a.cmd.Process.Pid), a.info.SessionID, "affected=[fixture/web]"} {
		if !strings.Contains(f.output.text(), text) {
			t.Fatalf("recovery did not disclose %q: %s", text, f.output.text())
		}
	}
	path := filepath.Join(a.root, "stackharbor.yaml")
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.ReplaceAll(config, []byte("HTTP terminal fixture"), []byte("Reloaded")), 0600); err != nil {
		t.Fatal(err)
	}
	f.master.Write([]byte("yes\r"))
	wait(t, func() bool {
		return strings.Contains(f.output.text(), "Dashboard") && strings.Contains(f.output.text(), "Reloaded")
	})
	completion, err := sessionhost.ReadCompletion(a.info.CacheDir, a.info.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	endPTY(t, a, completion)
	health(t, b.port)
	infos, err := supervisor.ListSessionsIn(cache)
	if err != nil || len(infos) != 2 {
		t.Fatal("recovery changed independent sessions", infos, err)
	}
	for _, info := range infos {
		if info.Root != a.root {
			continue
		}
		if info.PID != f.cmd.Process.Pid || info.SessionID == a.info.SessionID {
			t.Fatal("old owner was reused instead of reopening", info)
		}
		client, err := sessionapi.Connect(context.Background(), info)
		if err != nil {
			t.Fatal(err)
		}
		snap, err := client.Snapshot(context.Background())
		client.Close()
		if err != nil || len(snap.Nodes) != 1 || snap.Nodes[0].State != "stopped" {
			t.Fatal("reopened console automatically started services", snap, err)
		}
	}
	f.master.Write([]byte("q"))
	f.exit(t, 0)
	health(t, b.port)
}

func TestPTYRecoveryDeclineLocateAndCancellationPreserveSession(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply string
		code  int
	}{
		{name: "decline", reply: "n\r", code: 2},
		{name: "default", reply: "\r", code: 2},
		{name: "interrupt", reply: "\x03", code: 2},
		{name: "signal", code: 2},
		{name: "locate unsupported terminal", reply: "f\r", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", "StackHarborTest")
			a := ownedWebPTY(t, t.TempDir())
			a.cli(t, "start", "--target", "fixture/web", "--yes")
			f := startRecoveryPTY(t, []string{"--root", a.root}, a.env)
			wait(t, func() bool { return strings.Contains(f.output.text(), "Close this session and reopen here?") })
			if tc.name == "signal" {
				f.cmd.Process.Signal(syscall.SIGTERM)
			} else {
				f.master.Write([]byte(tc.reply))
			}
			f.exit(t, tc.code)
			health(t, a.port)
			infos, err := supervisor.ListSessionsIn(a.info.CacheDir)
			if err != nil || len(infos) != 1 || infos[0].SessionID != a.info.SessionID {
				t.Fatal("refusal replaced the running owner", infos, err)
			}
			var completion control.Operation
			if err := json.Unmarshal(a.cli(t, "kill", "--yes"), &completion); err != nil {
				t.Fatal(err)
			}
			endPTY(t, a, completion)
		})
	}
}

func TestPTYRecoveryCannotCloseReplacementSession(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "StackHarborTest")
	cache := t.TempDir()
	a := ownedWebPTY(t, cache)
	a.cli(t, "start", "--target", "fixture/web", "--yes")
	f := startRecoveryPTY(t, []string{"--root", a.root}, a.env)
	wait(t, func() bool { return strings.Contains(f.output.text(), "Close this session and reopen here?") })
	var completion control.Operation
	if err := json.Unmarshal(a.cli(t, "kill", "--yes"), &completion); err != nil {
		t.Fatal(err)
	}
	endPTY(t, a, completion)
	replacement := startWebPTY(t, a.root, cache, a.port)
	replacement.cli(t, "start", "--target", "fixture/web", "--yes")
	f.master.Write([]byte("yes\r"))
	f.exit(t, 2)
	wait(t, func() bool { return strings.Contains(f.output.text(), "session process or endpoint identity changed") })
	health(t, replacement.port)
	infos, err := supervisor.ListSessionsIn(cache)
	if err != nil || len(infos) != 1 || infos[0].SessionID != replacement.info.SessionID {
		t.Fatal("stale consent changed replacement session", infos, err)
	}
}

func TestPTYRecoveryRejectsChangedConfigurationBeforeReopening(t *testing.T) {
	for _, change := range []string{"invalid", "root"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			workspace := filepath.Join(t.TempDir(), "workspace.yaml")
			if err := os.WriteFile(workspace, []byte(fmt.Sprintf("version: 1\nroot: %q\ndiscover: false\n", root)), 0600); err != nil {
				t.Fatal(err)
			}
			cache := t.TempDir()
			env := append(os.Environ(), "TERM=xterm-256color", "TERM_PROGRAM=StackHarborTest", "STACKHARBOR_CACHE_DIR="+cache)
			args := []string{"--workspace", workspace}
			old := startRecoveryPTY(t, args, env)
			wait(t, func() bool { return strings.Contains(old.output.text(), "Dashboard") })
			f := startRecoveryPTY(t, args, env)
			wait(t, func() bool { return strings.Contains(f.output.text(), "Close this session and reopen here?") })
			updated := "version: [invalid"
			if change == "root" {
				updated = fmt.Sprintf("version: 1\nroot: %q\ndiscover: false\n", t.TempDir())
			}
			if err := os.WriteFile(workspace, []byte(updated), 0600); err != nil {
				t.Fatal(err)
			}
			f.master.Write([]byte("yes\r"))
			f.exit(t, 2)
			old.exit(t, 0)
			infos, err := supervisor.ListSessionsIn(cache)
			if err != nil || len(infos) != 0 {
				t.Fatal("changed configuration opened another host", infos, err)
			}
		})
	}
}

func TestPTYKillOnlyCurrentWorkspaceAndCleanOwnedServices(t *testing.T) {
	a, masterA, _, _, pidfile := terminalFixture(t)
	t.Cleanup(func() {
		if a.ProcessState == nil {
			a.Process.Signal(syscall.SIGTERM)
			a.Wait()
		}
	})
	rootA, _ := filepath.EvalSymlinks(filepath.Dir(pidfile))
	rootB := t.TempDir()
	b := exec.Command(binary, "--root", rootB)
	b.Env = a.Env // Share the registry while selecting a different workspace.
	masterB, err := pty.StartWithSize(b, &pty.Winsize{Rows: 35, Cols: 120})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if b.ProcessState == nil {
			b.Process.Signal(syscall.SIGTERM)
			b.Wait()
		}
		masterB.Close()
	}()
	captureB := &capture{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := masterB.Read(buf)
			if n > 0 {
				captureB.add(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	wait(t, func() bool { return strings.Contains(captureB.text(), "Dashboard") })
	masterA.Write([]byte("js"))
	wait(t, func() bool { _, err := os.Stat(pidfile); return err == nil })
	pidData, _ := os.ReadFile(pidfile)
	servicePID, _ := strconv.Atoi(string(pidData))
	kill := exec.Command(binary, "kill", "--yes")
	kill.Dir = rootA // The simple command must infer scope from the current project.
	kill.Env = a.Env
	// Even another real StackHarbor PID in the metadata must not authorize signaling it.
	registry := exec.Command(binary, "sessions", "--json")
	registry.Env = a.Env
	registered, err := registry.Output()
	if err != nil {
		t.Fatal(err)
	}
	var owners []struct {
		PID     int   `json:"pid"`
		Created int64 `json:"process_created_millis"`
	}
	if err := json.Unmarshal(registered, &owners); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(filepath.Dir(pidfile), "cache", fmt.Sprintf("%x.lock", sha256.Sum256([]byte(rootA))))
	original, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var forged map[string]any
	if err := json.Unmarshal(original, &forged); err != nil {
		t.Fatal(err)
	}
	for _, owner := range owners {
		if owner.PID == b.Process.Pid {
			forged["pid"], forged["process_created_millis"] = owner.PID, owner.Created
		}
	}
	dataForged, _ := json.Marshal(forged)
	if err := os.WriteFile(lockPath, dataForged, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := kill.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("session process or endpoint identity changed")) {
		t.Fatalf("wrong lock owner accepted: %s %v", output, err)
	}
	if syscall.Kill(a.Process.Pid, 0) != nil || syscall.Kill(b.Process.Pid, 0) != nil {
		t.Fatal("ownership rejection affected a running session")
	}
	if err := os.WriteFile(lockPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	kill = exec.Command(binary, "kill", "--yes", "--json")
	kill.Dir, kill.Env = rootA, a.Env
	var closeErrors bytes.Buffer
	kill.Stderr = &closeErrors
	output, err := kill.Output()
	var completion struct {
		Action    string `json:"action"`
		State     string `json:"state"`
		SessionID string `json:"session_id"`
	}
	if err != nil || json.Unmarshal(output, &completion) != nil || completion.Action != "close" || completion.State != "succeeded" || completion.SessionID == "" {
		t.Fatalf("kill failed: %s stderr=%s %v", output, closeErrors.String(), err)
	}
	if err := a.Wait(); err != nil {
		t.Fatal("session did not finish its normal shutdown", err)
	}
	if err := syscall.Kill(servicePID, 0); err != syscall.ESRCH {
		t.Fatal("owned service survived session termination", servicePID, err)
	}
	list := exec.Command(binary, "sessions", "--json")
	list.Env = a.Env
	data, err := list.Output()
	var remaining []struct {
		PID int `json:"pid"`
	}
	if err != nil || json.Unmarshal(data, &remaining) != nil || len(remaining) != 1 || remaining[0].PID != b.Process.Pid {
		t.Fatalf("kill affected another workspace: %s %v", data, err)
	}
	noop := exec.Command(binary, "kill", "--yes")
	noop.Dir, noop.Env = rootA, a.Env
	if output, err := noop.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("No active StackHarbor session")) {
		t.Fatalf("repeated kill should succeed without a session: %s %v", output, err)
	}
	masterB.Write([]byte("q"))
	if err := b.Wait(); err != nil {
		t.Fatal(err)
	}
}
