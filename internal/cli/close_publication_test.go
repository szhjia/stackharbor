package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
)

func lockCLICompletionJournal(t *testing.T, dir string) *os.File {
	t.Helper()
	fd, err := syscall.Open(filepath.Join(dir, ".journal.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "completion-fixture-lock")
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		t.Fatal(err)
	}
	return file
}

func writeDurableCLICompletion(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	lock := lockCLICompletionJournal(t, dir)
	defer lock.Close()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = file.Sync(); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if err = directory.Sync(); err != nil {
		t.Fatal(err)
	}
}

func TestWaitOperationDoesNotAcceptUncommittedJournalOrResubmit(t *testing.T) {
	h := controlHost(t, false)
	info := h.SessionInfo()
	dir := filepath.Join(info.CacheDir, "completions")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	writer := lockCLICompletionJournal(t, dir)
	defer writer.Close()
	client, err := sessionapi.Connect(context.Background(), info)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	plan, err := client.Plan(context.Background(), control.PlanRequest{Action: "close", Targets: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	op, err := client.Submit(context.Background(), control.SubmitRequest{PlanID: plan.ID, IdempotencyKey: control.NewIdempotencyKey(plan.ID)})
	if err != nil {
		t.Fatal(err)
	}
	visible := op
	visible.State = "succeeded"
	visible.UpdatedAt = time.Now().UTC()
	data, _ := json.Marshal(visible)
	path := filepath.Join(dir, info.SessionID+".json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	// Force the same completion-journal fallback as a closing Session API.
	_ = client.Close()
	if err = os.Remove(info.SocketPath); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if waited, known := waitOperation(ctx, client, info, op, time.Second); known || waited.ID != op.ID {
		t.Fatal("uncommitted journal resolved the operation", waited, known)
	}
	operations := h.Coordinator().Operations()
	if len(operations) != 1 || operations[0].ID != op.ID || operationTerminal(operations[0].State) {
		t.Fatal("wait resubmitted or fabricated completion", operations)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err = h.Close(cleanup); err != nil {
		t.Fatal(err)
	}
	if final, e := sessionhost.ReadCompletion(info.CacheDir, info.SessionID); e != nil || final.ID != op.ID || final.State != "succeeded" {
		t.Fatal(final, e)
	}
}
