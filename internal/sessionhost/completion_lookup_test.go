package sessionhost

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/szhjia/stackharbor/internal/control"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBoundOperationCompletionScopeAndTerminal(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	base, _ := filepath.EvalSymlinks(t.TempDir())
	cache := filepath.Join(base, "cache")
	os.Mkdir(cache, 0700)
	workspace := fmt.Sprintf("%x", sha256.Sum256([]byte(root)))
	id := "11111111111111111111111111111111"
	op := control.Operation{ID: "operation", SessionID: id, Action: "close", State: "succeeded", UpdatedAt: time.Now().UTC()}
	if e := writeCompletion(context.Background(), cache, op); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadOperationCompletion(cache, root, op.ID); e == nil {
		t.Fatal("accepted unbound record")
	}
	if e := writeBoundCompletion(context.Background(), cache, workspace, op); e != nil {
		t.Fatal(e)
	}
	if got, e := ReadOperationCompletion(cache, root, op.ID); e != nil || got.SessionID != id {
		t.Fatal(got, e)
	}
	other, _ := filepath.EvalSymlinks(t.TempDir())
	if _, e := ReadOperationCompletion(cache, other, op.ID); e == nil {
		t.Fatal("accepted other workspace")
	}
	if _, e := ReadOperationCompletion(cache, root, "other"); e == nil {
		t.Fatal("accepted other operation")
	}
	path := filepath.Join(cache, "completions", id+".json")
	wrong := filepath.Join(filepath.Dir(path), "22222222222222222222222222222222.json")
	os.Rename(path, wrong)
	if _, e := ReadOperationCompletion(cache, root, op.ID); e == nil {
		t.Fatal("accepted mismatched session file")
	}
	os.Rename(wrong, path)
	op.State = "finalizing"
	writeBoundCompletion(context.Background(), cache, workspace, op)
	if _, e := ReadOperationCompletion(cache, root, op.ID); e == nil {
		t.Fatal("accepted finalizing")
	}
	op.State = "succeeded"
	op.UpdatedAt = time.Now().Add(-25 * time.Hour)
	writeBoundCompletion(context.Background(), cache, workspace, op)
	if _, e := ReadOperationCompletion(cache, root, op.ID); e == nil {
		t.Fatal("accepted expired")
	}
}
