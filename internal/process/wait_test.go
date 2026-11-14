package process

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitForTopologyWaiters(t *testing.T, reader *sharedReader, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		reader.mu.Lock()
		count := reader.waiters
		reader.mu.Unlock()
		if count == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiters=%d want=%d", count, want)
		}
		time.Sleep(time.Millisecond)
	}
}
func assertTopologyMonitorStopped(t *testing.T, reader *sharedReader) {
	t.Helper()
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.waiters != 0 || reader.monitor != nil {
		t.Fatalf("topology waiter/monitor survived completion: waiters=%d monitor=%p", reader.waiters, reader.monitor)
	}
}
func TestTopologyWaitersShareOneLazyMonitorAndCancelIt(t *testing.T) {
	reader := NewSharedReader(&sharedFixture{}, func() time.Duration { return time.Hour }).(*sharedReader)
	_, revision, err := TopologySince(context.Background(), reader, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if WaitTopology(ctx, reader, revision, nil) {
				t.Error("unchanged topology unexpectedly woke")
			}
		}()
	}
	waitForTopologyWaiters(t, reader, 32)
	reader.mu.Lock()
	monitor := reader.monitor
	reader.mu.Unlock()
	if monitor == nil {
		t.Fatal("no shared expiry monitor")
	}
	cancel()
	workers.Wait()
	assertTopologyMonitorStopped(t, reader)
	select {
	case <-monitor.done:
	default:
		t.Fatal("monitor goroutine was not drained")
	}
}
func TestTopologyWaitHonorsDynamicTTLShortening(t *testing.T) {
	var ttl atomic.Int64
	ttl.Store(int64(time.Hour))
	reader := NewSharedReader(&sharedFixture{}, func() time.Duration { return time.Duration(ttl.Load()) }).(*sharedReader)
	_, revision, _ := TopologySince(context.Background(), reader, 0)
	result := make(chan bool, 1)
	go func() { result <- WaitTopology(context.Background(), reader, revision, nil) }()
	waitForTopologyWaiters(t, reader, 1)
	ttl.Store(1)
	select {
	case ready := <-result:
		if !ready {
			t.Fatal("shortened TTL did not wake reader")
		}
	case <-time.After(time.Second):
		t.Fatal("wait retained the old foreground deadline")
	}
	assertTopologyMonitorStopped(t, reader)
}
func TestTopologyInvalidationWakesWithoutScanningAndStopsMonitor(t *testing.T) {
	base := &sharedFixture{}
	reader := NewSharedReader(base, func() time.Duration { return time.Hour }).(*sharedReader)
	_, revision, _ := TopologySince(context.Background(), reader, 0)
	result := make(chan bool, 1)
	go func() { result <- WaitTopology(context.Background(), reader, revision, nil) }()
	waitForTopologyWaiters(t, reader, 1)
	InvalidateTopology(reader)
	select {
	case ready := <-result:
		if !ready {
			t.Fatal("launch invalidation did not wake reader")
		}
	case <-time.After(time.Second):
		t.Fatal("invalidation waited for old TTL")
	}
	base.mu.Lock()
	scans := base.lists
	base.mu.Unlock()
	if scans != 1 {
		t.Fatalf("waiting/invalidation unexpectedly scanned processes %d times", scans)
	}
	assertTopologyMonitorStopped(t, reader)
}
func TestTopologyWaitFinishedNotificationDrainsMonitor(t *testing.T) {
	reader := NewSharedReader(&sharedFixture{}, func() time.Duration { return time.Hour }).(*sharedReader)
	_, revision, _ := TopologySince(context.Background(), reader, 0)
	finished := make(chan struct{})
	result := make(chan bool, 1)
	go func() { result <- WaitTopology(context.Background(), reader, revision, finished) }()
	waitForTopologyWaiters(t, reader, 1)
	close(finished)
	select {
	case ready := <-result:
		if ready {
			t.Fatal("finished handle requested another scan")
		}
	case <-time.After(time.Second):
		t.Fatal("finished handle retained monitor")
	}
	assertTopologyMonitorStopped(t, reader)
}
func TestTopologyWaitMonitorRestartsForNewWaiters(t *testing.T) {
	reader := NewSharedReader(&sharedFixture{}, func() time.Duration { return time.Hour }).(*sharedReader)
	_, revision, _ := TopologySince(context.Background(), reader, 0)
	for range 10 {
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan bool, 1)
		go func() { result <- WaitTopology(ctx, reader, revision, nil) }()
		waitForTopologyWaiters(t, reader, 1)
		cancel()
		if <-result {
			t.Fatal("canceled waiter woke")
		}
		assertTopologyMonitorStopped(t, reader)
	}
}
func TestTopologyUncachedFallbackRetainsBoundedPolling(t *testing.T) {
	reader := NewSharedReader(&sharedFixture{}, func() time.Duration { return 0 })
	TopologySince(context.Background(), reader, 0)
	started := time.Now()
	if !WaitTopology(context.Background(), reader, 1, nil) {
		t.Fatal("fallback did not become ready")
	}
	if time.Since(started) < 80*time.Millisecond {
		t.Fatal("zero TTL created a busy polling loop")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if WaitTopology(ctx, &sharedFixture{}, 0, nil) {
		t.Fatal("custom-reader fallback ignored cancellation")
	}
}
