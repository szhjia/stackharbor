package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSessionRegistryIsolatedRootsAndReleasedLocks(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	rootA, rootB := t.TempDir(), t.TempDir()
	rootA, _ = filepath.EvalSymlinks(rootA)
	rootB, _ = filepath.EvalSymlinks(rootB)
	a, err := Acquire(rootA)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Acquire(rootB)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	all, err := ListSessions()
	if err != nil || len(all) != 2 {
		t.Fatalf("active roots: %+v, %v", all, err)
	}
	for _, info := range all {
		if info.PID != os.Getpid() || info.ProcessCreatedMillis <= 0 || info.StartedAt.IsZero() {
			t.Fatalf("missing owner identity: %+v", info)
		}
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(rootA, alias); err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(alias)
	var conflict *SessionConflict
	if !errors.As(err, &conflict) || conflict.Session.PID != os.Getpid() || conflict.Session.Root != rootA {
		t.Fatalf("canonical-root conflict missing metadata: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	all, err = ListSessions()
	if err != nil || len(all) != 1 || all[0].Root != rootB {
		t.Fatalf("released root still listed: %+v, %v", all, err)
	}
	reopened, err := Acquire(rootA)
	if err != nil {
		t.Fatal("released lock could not reopen", err)
	}
	reopened.Close()
}

func TestRegistryIgnoresSymlinkFIFOAndStaleMetadata(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("STACKHARBOR_CACHE_DIR", cache)
	root := t.TempDir()
	lock, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	path := lock.file.Name()
	info := readSessionInfo(lock.file)
	info.ProcessCreatedMillis++
	if validSessionInfo(info, path) {
		t.Fatal("reused PID accepted without matching process birth time")
	}
	lock.Close()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Rename(path, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(root); err == nil {
		t.Fatal("acquired symlink lock")
	}
	all, err := ListSessions()
	if err != nil || len(all) != 0 {
		t.Fatalf("symlink listed: %+v %v", all, err)
	}
	os.Remove(path)
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	all, err = ListSessions()
	if err != nil || len(all) != 0 {
		t.Fatalf("FIFO listed: %+v %v", all, err)
	}
	if _, err := Acquire(root); err == nil {
		t.Fatal("acquired FIFO lock")
	}
}
