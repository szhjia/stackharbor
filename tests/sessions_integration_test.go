package tests

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
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
	duplicate, _, dupOut := start(rootA)
	done := make(chan error, 1)
	go func() { done <- duplicate.Wait() }()
	select {
	case err := <-done:
		if err == nil || duplicate.ProcessState.ExitCode() != 1 {
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
	kill := exec.Command(binary, "kill")
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
	if output, err := kill.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("no signal sent")) {
		t.Fatalf("wrong lock owner accepted: %s %v", output, err)
	}
	if syscall.Kill(a.Process.Pid, 0) != nil || syscall.Kill(b.Process.Pid, 0) != nil {
		t.Fatal("ownership rejection affected a running session")
	}
	if err := os.WriteFile(lockPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	kill = exec.Command(binary, "kill")
	kill.Dir, kill.Env = rootA, a.Env
	output, err := kill.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Closed session PID "+strconv.Itoa(a.Process.Pid)) {
		t.Fatalf("kill failed: %s %v", output, err)
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
	noop := exec.Command(binary, "kill")
	noop.Dir, noop.Env = rootA, a.Env
	if output, err := noop.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("No active StackHarbor session")) {
		t.Fatalf("repeated kill should succeed without a session: %s %v", output, err)
	}
	masterB.Write([]byte("q"))
	if err := b.Wait(); err != nil {
		t.Fatal(err)
	}
}
