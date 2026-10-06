package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

func remoteFixture(t *testing.T) (*exec.Cmd, *os.File, *sessionapi.Client, supervisor.SessionInfo, string) {
	t.Helper()
	cmd, master, _, _, pidfile := terminalFixture(t)
	master.Write([]byte("js"))
	wait(t, func() bool { _, err := os.Stat(pidfile); return err == nil })
	list := exec.Command(binary, "sessions", "--json")
	list.Env = cmd.Env
	data, err := list.Output()
	if err != nil {
		t.Fatal(err)
	}
	var sessions []supervisor.SessionInfo
	if err = json.Unmarshal(data, &sessions); err != nil {
		t.Fatal(err)
	}
	var info supervisor.SessionInfo
	for _, s := range sessions {
		if s.PID == cmd.Process.Pid {
			info = s
		}
	}
	t.Setenv("STACKHARBOR_CACHE_DIR", info.CacheDir)
	client, err := sessionapi.Connect(context.Background(), info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return cmd, master, client, info, pidfile
}
func submitRemoteClose(t *testing.T, client *sessionapi.Client) control.Operation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	plan, err := client.Plan(ctx, control.PlanRequest{Action: "close", Targets: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	op, err := client.Submit(ctx, control.SubmitRequest{PlanID: plan.ID, IdempotencyKey: control.NewIdempotencyKey(plan.ID)})
	if err != nil {
		t.Fatal(err)
	}
	return op
}
func assertRemoteExit(t *testing.T, cmd *exec.Cmd, info supervisor.SessionInfo, pidfile string, op control.Operation) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("remote close did not finish TUI")
	}
	record, err := sessionhost.ReadCompletion(info.CacheDir, info.SessionID)
	if err != nil || record.ID != op.ID || record.State != "succeeded" {
		t.Fatalf("completion %+v error=%v", record, err)
	}
	if _, err := os.Stat(info.SocketPath); !os.IsNotExist(err) {
		t.Fatal("remote socket survived", err)
	}
	data, _ := os.ReadFile(pidfile)
	pid, _ := strconv.Atoi(string(data))
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatal("owned process survived", pid, err)
	}
	lock, err := supervisor.Acquire(filepath.Dir(pidfile))
	if err != nil {
		t.Fatal("lock remained after completion", err)
	}
	lock.Close()
}
func TestRemoteCloseRestoresTUIAndCleansOwnedServices(t *testing.T) {
	cmd, _, client, info, pidfile := remoteFixture(t)
	op := submitRemoteClose(t, client)
	assertRemoteExit(t, cmd, info, pidfile, op)
	// terminalFixture cleanup additionally compares exact term.GetState before/after.
}
func TestSignalAndRemoteCloseOnlyShutdownOnce(t *testing.T) {
	cmd, _, client, info, pidfile := remoteFixture(t)
	op := submitRemoteClose(t, client)
	_ = cmd.Process.Signal(syscall.SIGTERM)
	assertRemoteExit(t, cmd, info, pidfile, op)
	entries, err := os.ReadDir(filepath.Join(info.CacheDir, "completions"))
	records := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".json" {
			records++
		}
	}
	if err != nil || records != 1 {
		t.Fatalf("duplicate completion: %v %v", entries, err)
	}
	events, err := supervisor.ReadHistory(filepath.Dir(pidfile))
	if err != nil {
		t.Fatal(err)
	}
	stops := 0
	for _, event := range events {
		if event.Kind == "operation/stop" {
			stops++
		}
	}
	if stops != 1 {
		t.Fatalf("cleanup stop executions=%d events=%+v", stops, events)
	}
}

func TestPTYPortConfirmationRejectsReplacement(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	startExternal := func() *exec.Cmd {
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "SH_HTTP_FIXTURE=1", fmt.Sprintf("SH_HTTP_PORT=%d", port))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		wait(t, func() bool {
			c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
			if c != nil {
				c.Close()
			}
			return err == nil
		})
		return cmd
	}
	original := startExternal()
	t.Cleanup(func() { original.Process.Kill(); original.Wait() })
	cmd, master, _, captured, pidfile := terminalFixture(t, port)
	master.Write([]byte("js"))
	wait(t, func() bool { return strings.Contains(captured.text(), "Release conflicting ports") })
	original.Process.Kill()
	original.Wait()
	replacement := startExternal()
	defer func() { replacement.Process.Kill(); replacement.Wait() }()
	master.Write([]byte("y"))
	wait(t, func() bool { return strings.Contains(captured.text(), "Relevant state changed") })
	if _, err := os.Stat(pidfile); err == nil {
		t.Fatal("stale confirmation launched service")
	}
	if err := syscall.Kill(replacement.Process.Pid, 0); err != nil {
		t.Fatal("stale confirmation killed replacement", err)
	}
	master.Write([]byte("q"))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("quit blocked after stale confirmation")
	}
}
