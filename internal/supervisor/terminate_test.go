package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	gp "github.com/shirou/gopsutil/v4/process"
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
	p, err := gp.NewProcess(int32(unrelated.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.CreateTime()
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
