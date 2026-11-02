package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/process"
)

func TestTerminateRejectsUnrelatedPIDInAdvisoryMetadata(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	root := t.TempDir()
	lock, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	unrelated := exec.Command("sleep", "30")
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { unrelated.Process.Kill(); unrelated.Wait() }()
	created, err := process.CreatedMillis(context.Background(), int32(unrelated.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	info := readSessionInfo(lock.file)
	info.PID = unrelated.Process.Pid
	info.ProcessCreatedMillis = created
	if err := writeSessionInfo(lock.file, info); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, stopped, err := TerminateSession(ctx, root); err == nil || stopped {
		t.Fatal("advisory metadata authorized an unrelated process", err)
	}
	if err := unrelated.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("unrelated process was signaled", err)
	}
}

func TestTerminateRefusesSelfAndSignalVerificationFailure(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	root := t.TempDir()
	lock, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, stopped, err := TerminateSession(context.Background(), root); stopped || err == nil {
		t.Fatal("current process accepted as termination target")
	}
	info := SessionInfo{PID: os.Getpid(), ProcessCreatedMillis: 1}
	signals := 0
	verify := func(context.Context, SessionInfo) error { return errors.New("identity changed") }
	signal := func(int, syscall.Signal) error { signals++; return nil }
	if err := signalSession(context.Background(), info, verify, signal); err == nil || signals != 0 {
		t.Fatal("signal sent after verification failure", err, signals)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := signalSession(ctx, info, func(context.Context, SessionInfo) error { return nil }, signal); err == nil || signals != 0 {
		t.Fatal("signal sent despite cancellation", err, signals)
	}
}

func TestTerminateObservedSessionRejectsReplacement(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	root := t.TempDir()
	old, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	expected := old.Info()
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	// Both locks have the same PID and birth time. Session identity must still
	// distinguish them, including legacy records that now carry session IDs.
	if _, stopped, err := TerminateObservedSession(context.Background(), expected); err == nil || stopped || !strings.Contains(err.Error(), "Workspace session changed") {
		t.Fatalf("replacement accepted: stopped=%v err=%v", stopped, err)
	}
	if lock, err := Acquire(root); err == nil {
		lock.Close()
		t.Fatal("replacement owner lost its lock")
	}
}

func TestTerminateObservedSessionAlreadyEnded(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	lock, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	expected := lock.Info()
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, stopped, err := TerminateObservedSession(context.Background(), expected); err != nil || stopped {
		t.Fatalf("ended owner: stopped=%v err=%v", stopped, err)
	}
}
