package sessionhost

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
)

func TestCompletionReadersRejectVisibleUncommittedTerminalRecord(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	cache := filepath.Join(root, "cache")
	workspace := fmt.Sprintf("%x", sha256.Sum256([]byte(root)))
	op := control.Operation{ID: "operation", SessionID: "11111111111111111111111111111111", Action: "close", State: "finalizing", UpdatedAt: time.Now().UTC()}
	if err := writeBoundCompletion(context.Background(), cache, workspace, op); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cache, "completions")
	writer, err := lockJournal(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	op.State = "succeeded"
	data, _ := json.Marshal(completionRecord{Operation: op, WorkspaceID: workspace})
	path := filepath.Join(dir, op.SessionID+".json")
	// Simulate the writer's renamed terminal file before directory fsync has
	// completed. Its exclusive journal lock remains held across that boundary.
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, read := range []func() (control.Operation, error){func() (control.Operation, error) { return ReadCompletion(cache, op.SessionID) }, func() (control.Operation, error) { return ReadOperationCompletion(cache, root, op.ID) }} {
		if value, e := read(); e == nil {
			t.Fatal("uncommitted terminal bytes became success evidence", value)
		} else {
			var api *control.APIError
			if !errors.As(e, &api) || api.Code != "result_unknown" {
				t.Fatal(e)
			}
		}
	}
	// A post-rename durability failure removes the unpublished success while
	// retaining the lock; subsequent readers must see missing, never success.
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadCompletion(cache, op.SessionID); !os.IsNotExist(err) {
		t.Fatal("missing completion semantics changed", err)
	}
	if err = writeBoundCompletion(context.Background(), cache, workspace, op); err != nil {
		t.Fatal(err)
	}
	if value, e := ReadCompletion(cache, op.SessionID); e != nil || value.State != "succeeded" {
		t.Fatal(value, e)
	}
	if value, e := ReadOperationCompletion(cache, root, op.ID); e != nil || value.State != "succeeded" {
		t.Fatal(value, e)
	}
}

func TestCompletionReadNeverCreatesMissingJournalLock(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	cache := filepath.Join(root, "cache")
	dir := filepath.Join(cache, "completions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCompletion(cache, "11111111111111111111111111111111"); err == nil {
		t.Fatal("missing lock accepted")
	}
	if _, err := ReadOperationCompletion(cache, root, "operation"); err == nil {
		t.Fatal("missing lock accepted")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatal("read path wrote journal state", entries, err)
	}
}

func TestJournalUnlockWithRetainedDescriptor(t *testing.T) {
	dir := t.TempDir()
	writer, err := lockJournal(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	// A duplicate shares the original open file description and its flock,
	// just like a descriptor temporarily inherited by a forked process.
	duplicate, err := syscall.Dup(int(writer.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(duplicate)
	if reader, err := readJournalLock(dir); err == nil {
		reader.Close()
		t.Fatal("reader acquired the lock while publication held it")
	}

	unlockJournal(writer)
	reader, err := readJournalLock(dir)
	if err != nil {
		t.Fatal("reader could not acquire the lock after publication:", err)
	}
	defer reader.Close()
}
