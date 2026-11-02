package tests

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/desktop"
)

func TestDesktopSidecarPipeEOFDoesNotStopTerminalOwnedService(t *testing.T) {
	terminal, _, client, info, pidfile := remoteFixture(t)
	cmd := exec.Command(binary, "desktop-backend", "--namespace", info.CacheDir, "--expected-version", buildinfo.Version, "--protocol", "1")
	parent, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { parent.Close(); cmd.Process.Kill() })
	ready := make(chan desktop.BackendReady, 1)
	go func() { var v desktop.BackendReady; _ = json.NewDecoder(out).Decode(&v); ready <- v }()
	select {
	case v := <-ready:
		if !v.Owned || v.Error != "" {
			t.Fatalf("sidecar %+v", v)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("sidecar startup timed out")
	}
	parent.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("sidecar did not stop")
	}
	snapshot, err := client.Snapshot(context.Background())
	if err != nil || len(snapshot.Processes) == 0 {
		t.Fatalf("terminal-owned service lost: %+v %v", snapshot, err)
	}
	op := submitRemoteClose(t, client)
	assertRemoteExit(t, terminal, info, pidfile, op)
}

func TestTerminalHangupCleansOwnedService(t *testing.T) {
	terminal, _, client, info, pidfile := remoteFixture(t)
	_ = client
	_ = terminal.Process.Signal(syscall.SIGHUP)
	done := make(chan error, 1)
	go func() { done <- terminal.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("hangup cleanup did not finish")
	}
	data, _ := os.ReadFile(pidfile)
	pid, _ := strconv.Atoi(string(data))
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatal("owned service survived HUP", err)
	}
	if _, err := os.Stat(info.SocketPath); !os.IsNotExist(err) {
		t.Fatal("session endpoint survived HUP", err)
	}
}

func TestTerminalRefusesServiceWhenProcessInspectionIsUnavailable(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ps"), []byte("#!/bin/sh\necho 'process inspection blocked by test' >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	terminal, master, _, output, pidfile := terminalFixture(t)
	master.Write([]byte("js"))
	wait(t, func() bool { return strings.Contains(output.text(), "service not started") })
	if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
		t.Fatalf("unobservable service was launched: %v", err)
	}
	master.Write([]byte("q"))
	done := make(chan error, 1)
	go func() { done <- terminal.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("terminal did not exit after refusing unobservable service")
	}
}
