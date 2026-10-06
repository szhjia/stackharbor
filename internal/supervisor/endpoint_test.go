package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalRootAndNamespaceIsolation(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	os.Symlink(root, alias)
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	a, err := Acquire(alias)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ia := a.Info()
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	b, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ib := b.Info()
	if ia.WorkspaceID != ib.WorkspaceID || ia.NamespaceID == ib.NamespaceID || ia.SessionID == ib.SessionID {
		t.Fatalf("bad identity/isolation: %+v %+v", ia, ib)
	}
}
func TestOccupiedLockNeverReplaced(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	root := t.TempDir()
	l, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	st, _ := os.Stat(l.file.Name())
	info := l.Info()
	if err := l.PublishEndpoint(Endpoint{SocketPath: "/unreachable", ProtocolVersion: 1}); err != nil {
		t.Fatal(err)
	}
	current, _ := os.Stat(l.file.Name())
	if !os.SameFile(st, current) {
		t.Fatal("publication replaced inode")
	}
	_, err = Acquire(root)
	var conflict *SessionConflict
	if !errors.As(err, &conflict) || conflict.Session.SessionID != info.SessionID {
		t.Fatal("occupied lock replaced", err)
	}
	if err := os.Rename(l.file.Name(), l.file.Name()+".old"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(l.file.Name(), []byte("replacement"), 0600)
	if err := l.PublishEndpoint(Endpoint{ProtocolVersion: 1}); err == nil {
		t.Fatal("published through replaced pathname")
	}
}

func TestRegistryRejectsUnsafeCacheAndLockPermissions(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("STACKHARBOR_CACHE_DIR", cache)
	root := t.TempDir()
	l, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err = os.Chmod(l.file.Name(), 0666); err != nil {
		t.Fatal(err)
	}
	all, err := ListSessions()
	if err != nil || len(all) != 0 {
		t.Fatalf("unsafe lock listed %v %v", all, err)
	}
	os.Chmod(l.file.Name(), 0600)
	os.Chmod(cache, 0777)
	defer os.Chmod(cache, 0700)
	if _, err := ListSessions(); err == nil {
		t.Fatal("writable shared cache accepted")
	}
	if _, err := Acquire(t.TempDir()); err == nil {
		t.Fatal("unsafe cache acquired")
	}
}

// Pause the actual lock-file writer immediately before its bytes are written.
// Readers must keep seeing nonempty metadata throughout endpoint publication.
type pausedMetadataFile struct {
	*os.File
	entered chan struct{}
	release chan struct{}
}

func (f *pausedMetadataFile) Write(p []byte) (int, error) {
	close(f.entered)
	<-f.release
	return f.File.Write(p)
}
func (f *pausedMetadataFile) WriteAt(p []byte, offset int64) (int, error) {
	close(f.entered)
	<-f.release
	return f.File.WriteAt(p, offset)
}
func TestEndpointPublicationNeverExposesEmptyLegacyRecord(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	l, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	before, _ := l.file.Stat()
	info := l.Info()
	info.SocketPath = "/new-endpoint"
	info.ProtocolVersion = 1
	writer := &pausedMetadataFile{File: l.file, entered: make(chan struct{}), release: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- writeSessionInfo(writer, info) }()
	<-writer.entered
	st, statErr := l.file.Stat()
	observed := readSessionInfo(l.file)
	close(writer.release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if statErr != nil || st.Size() == 0 || observed.SessionID != info.SessionID {
		t.Fatalf("publication exposed empty/legacy metadata: stat=%v size=%d observed=%+v", statErr, st.Size(), observed)
	}
	after, _ := l.file.Stat()
	if !os.SameFile(before, after) {
		t.Fatal("publication changed locked inode")
	}
	if got := readSessionInfo(l.file); got.SocketPath != info.SocketPath || got.SessionID != info.SessionID {
		t.Fatalf("published metadata=%+v", got)
	}
	// A shorter rewrite must remove the old JSON tail only after replacement bytes.
	info.SocketPath = ""
	if err := writeSessionInfo(l.file, info); err != nil {
		t.Fatal(err)
	}
	if got := readSessionInfo(l.file); got.SocketPath != "" || got.SessionID != info.SessionID {
		t.Fatalf("shorter publication=%+v", got)
	}
}
