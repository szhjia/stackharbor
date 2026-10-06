package sessionhost

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

var sessionIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// ReadCompletion returns only a durable terminal result for this exact session.
// A missing or interrupted finalization is unknown, never inferred success.
func ReadCompletion(cache, sessionID string) (control.Operation, error) {
	if !sessionIDPattern.MatchString(sessionID) {
		return control.Operation{}, fmt.Errorf("invalid session ID")
	}
	dir := filepath.Join(cache, "completions")
	if err := sessionapi.ValidatePrivateDirectory(dir); err != nil {
		return control.Operation{}, err
	}
	op, err := readCompletion(filepath.Join(dir, sessionID+".json"))
	if err != nil {
		return op, err
	}
	if op.SessionID != sessionID || op.Action != "close" || time.Since(op.UpdatedAt) >= 24*time.Hour || !terminal(op.State) {
		return control.Operation{}, &control.APIError{Code: "result_unknown", Message: "Session completion is unknown or expired"}
	}
	return op, nil
}
func terminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "partial" || state == "canceled"
}

type completionRecord struct {
	control.Operation
	WorkspaceID string `json:"workspace_id,omitempty"`
}

func readCompletion(path string) (control.Operation, error) {
	record, err := readCompletionRecord(path)
	return record.Operation, err
}
func readCompletionRecord(path string) (completionRecord, error) {
	var op completionRecord
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return op, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return op, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Getuid() || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 65536 {
		return op, fmt.Errorf("unsafe completion record")
	}
	err = json.NewDecoder(file).Decode(&op)
	return op, err
}
func writeCompletion(ctx context.Context, cache string, op control.Operation) error {
	return writeBoundCompletion(ctx, cache, "", op)
}
func writeBoundCompletion(ctx context.Context, cache, workspaceID string, op control.Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !sessionIDPattern.MatchString(op.SessionID) {
		return fmt.Errorf("invalid session ID")
	}
	dir := filepath.Join(cache, "completions")
	if err := supervisor.EnsurePrivateDirectory(dir); err != nil {
		return err
	}
	if err := sessionapi.ValidatePrivateDirectory(dir); err != nil {
		return err
	}
	lock, err := lockJournal(ctx, dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	// Reserve one slot before publishing. Cross-process flock keeps the cap
	// meaningful when independent foreground hosts share this cache.
	if err = pruneCompletions(dir, time.Now(), op.SessionID); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	file, err := os.CreateTemp(dir, ".completion-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = json.NewEncoder(file).Encode(completionRecord{Operation: op, WorkspaceID: workspaceID}); err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	inode, err := os.Stat(name)
	if err != nil {
		return err
	}
	destination := filepath.Join(dir, op.SessionID+".json")
	if err = os.Rename(name, destination); err != nil {
		return err
	}
	err = errors.Join(directory.Sync(), ctx.Err())
	if err != nil {
		// A failed durable publish must not leave a visible success record.
		if named, e := os.Lstat(destination); e == nil && os.SameFile(inode, named) {
			err = errors.Join(err, os.Remove(destination))
		}
		return err
	}
	return nil
}
func pruneCompletions(dir string, now time.Time, replacing string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type record struct {
		name string
		at   time.Time
	}
	records := []record{}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" || entry.Name() == replacing+".json" {
			continue
		}
		op, err := readCompletion(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		if now.Sub(op.UpdatedAt) >= 24*time.Hour {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		} else {
			records = append(records, record{entry.Name(), op.UpdatedAt})
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].at.Before(records[j].at) })
	for _, record := range records[:max(0, len(records)-999)] {
		if err := os.Remove(filepath.Join(dir, record.name)); err != nil {
			return err
		}
	}
	return nil
}

func lockJournal(ctx context.Context, dir string) (*os.File, error) {
	path := filepath.Join(dir, ".journal.lock")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Getuid() || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		f.Close()
		return nil, fmt.Errorf("unsafe journal lock")
	}
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return f, nil
}

// ReadOperationCompletion recovers a closed session result using the canonical
// root and exact operation ID, even if a new session has replaced registration.
// Unbound older records cannot authorize a root-scoped result query.
func ReadOperationCompletion(cache, canonicalRoot, operationID string) (control.Operation, error) {
	unknown := &control.APIError{Code: "result_unknown", Message: "No bound terminal operation record for this workspace"}
	if operationID == "" {
		return control.Operation{}, unknown
	}
	root, e := filepath.Abs(canonicalRoot)
	if e == nil {
		root, e = filepath.EvalSymlinks(root)
	}
	if e != nil || root != canonicalRoot {
		return control.Operation{}, unknown
	}
	workspaceID := fmt.Sprintf("%x", sha256.Sum256([]byte(root)))
	dir := filepath.Join(cache, "completions")
	if e := sessionapi.ValidatePrivateDirectory(dir); e != nil {
		return control.Operation{}, e
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return control.Operation{}, e
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		record, e := readCompletionRecord(filepath.Join(dir, entry.Name()))
		if e != nil {
			continue
		}
		if record.ID != operationID || record.WorkspaceID != workspaceID || !sessionIDPattern.MatchString(record.SessionID) || entry.Name() != record.SessionID+".json" {
			continue
		}
		if record.Action != "close" || time.Since(record.UpdatedAt) >= 24*time.Hour || !terminal(record.State) {
			return control.Operation{}, unknown
		}
		return record.Operation, nil
	}
	return control.Operation{}, unknown
}
