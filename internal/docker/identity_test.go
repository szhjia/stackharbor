package docker

import (
	"context"
	"errors"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
	"strings"
	"testing"
	"time"
)

func TestCanonicalIdentityAndUnknownMutationRefused(t *testing.T) {
	m := &Manager{File: "compose.yaml", run: func(_ context.Context, a ...string) ([]byte, error) {
		if a[0] == "info" {
			return []byte(`"full-daemon-id"`), nil
		}
		return []byte(`{"name":"effective-project"}`), nil
	}}
	id, err := m.Identity(context.Background())
	if err != nil || id.Endpoint == "" || id.Project != "effective-project" {
		t.Fatal(id, err)
	}
	m.run = func(context.Context, ...string) ([]byte, error) { return nil, nil }
	m.specs = nil
	if _, err = m.LockKeys(context.Background(), []string{"db"}); err == nil {
		t.Fatal("unknown endpoint accepted")
	}
	if strings.Contains(id.Endpoint, "full-daemon-id") {
		t.Fatal("raw endpoint exposed")
	}
}

func TestManagerBorrowedLockRetainedUntilSideEffectFinishes(t *testing.T) {
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	manager := &Manager{File: "compose.yaml", Project: "shared", specs: []model.DockerSnapshot{{Service: "db"}}, run: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "info" {
			return []byte(`"delayed-fixture-daemon"`), nil
		}
		close(entered)
		<-finish
		return nil, nil
	}}
	keys, err := manager.LockKeys(context.Background(), []string{"db"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, release, err := control.WithResourceLocks(context.Background(), control.ResourceLockNamespace, keys)
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- manager.Action(ctx, "start", []string{"db"}) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("manager deadlocked under borrowed resource lock")
	}
	release()
	deadline, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	other, err := control.AcquireResourceLocks(deadline, control.ResourceLockNamespace, keys)
	if err == nil {
		other()
		t.Fatal("competing session entered while side effect active")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	other, err = control.AcquireResourceLocks(context.Background(), control.ResourceLockNamespace, keys)
	if err != nil {
		t.Fatal(err)
	}
	other()
}
