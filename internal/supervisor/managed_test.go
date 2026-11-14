package supervisor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestManagedLockNeverAdvertisesLegacyShutdownWindow(t *testing.T) {
	root, namespace := t.TempDir(), t.TempDir()
	a, err := AcquireIn(root, namespace, true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	info := a.Info()
	if info.ProtocolVersion == 0 || info.OwnerKind != "engine" {
		t.Fatal("initial record is legacy", info)
	}
	raw, err := os.ReadFile(filepath.Join(namespace, info.WorkspaceID+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("initial identity not durable")
	}
	if _, err = AcquireIn(root, namespace, true); err == nil {
		t.Fatal("duplicate root lock accepted")
	}
}

func TestManagedPublicationNeverExposesAnOccupiedBlankRecord(t *testing.T) {
	root, namespace := t.TempDir(), t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	namespace, _ = filepath.EvalSymlinks(namespace)
	path := filepath.Join(namespace, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(root))))
	prepared, release := make(chan struct{}), make(chan struct{})
	result := make(chan *Lock, 1)
	fail := make(chan error, 1)
	go func() {
		lock, err := acquireManaged(root, namespace, path, func(SessionInfo) { close(prepared); <-release })
		if err != nil {
			fail <- err
			return
		}
		result <- lock
	}()
	<-prepared
	if _, active, err := activeSession(path); err != nil || active {
		t.Fatal("unpublished registration became observable", active, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("final lock path exists before complete publication")
	}
	close(release)
	var lock *Lock
	select {
	case err := <-fail:
		t.Fatal(err)
	case lock = <-result:
	}
	defer lock.Close()
	info, active, err := activeSession(path)
	if err != nil || !active || info.OwnerKind != "engine" || info.ProtocolVersion == 0 {
		t.Fatal(info, active, err)
	}
	signalled, verified := false, false
	err = signalSession(context.Background(), info, func(context.Context, SessionInfo) error { verified = true; return nil }, func(int, syscall.Signal) error { signalled = true; return nil })
	if err == nil || verified || signalled {
		t.Fatal("shared engine reached legacy signal path")
	}
	if err = lock.PublishEndpoint(Endpoint{SocketPath: "/private/tmp/fake.sock", ProtocolVersion: 1}); err != nil {
		t.Fatal("prepared inode lost public-path identity", err)
	}
}

func TestManagedRegistrationReplacesOnlyReleasedNonemptyRecord(t *testing.T) {
	root, namespace := t.TempDir(), t.TempDir()
	first, err := AcquireIn(root, namespace, true)
	if err != nil {
		t.Fatal(err)
	}
	oldID := first.Info().SessionID
	_ = first.Close()
	second, err := AcquireIn(root, namespace, true)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.Info().SessionID == oldID {
		t.Fatal("new session reused old id")
	}
	emptyRoot := t.TempDir()
	real, _ := filepath.EvalSymlinks(emptyRoot)
	path := filepath.Join(namespace, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(real))))
	_ = os.WriteFile(path, nil, 0600)
	if _, err = AcquireIn(real, namespace, true); err == nil {
		t.Fatal("empty legacy record was adopted")
	}
	st, _ := os.Stat(path)
	if st.Size() != 0 {
		t.Fatal("legacy empty record modified")
	}
}
