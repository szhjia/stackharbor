package web

import (
	"context"
	"github.com/szhjia/stackharbor/internal/inventory"
	"sync/atomic"
	"testing"
	"time"
)

func TestForegroundWakeDoesNotWaitForBackgroundCadence(t *testing.T) {
	var calls atomic.Int64
	live := newLiveInventory(registrySource{}, func(context.Context) (inventory.Inventory, error) {
		calls.Add(1)
		return inventory.Inventory{Sessions: []inventory.Session{}, Resources: []inventory.Resource{}}, nil
	}, nil)
	live.interval = func() time.Duration { return 10 * time.Second }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); live.run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatal("initial refresh missing")
	}
	live.wake <- struct{}{}
	deadline = time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() < 2 {
		t.Fatal("returning to foreground waited for background tick")
	}
	cancel()
	<-done
}
