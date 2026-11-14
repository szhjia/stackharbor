package process

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/model"
)

type sharedFixture struct {
	mu           sync.Mutex
	lists, reads int
	identity     model.ProcessIdentity
	block        <-chan struct{}
}

func (r *sharedFixture) List(ctx context.Context) ([]Info, error) {
	r.mu.Lock()
	r.lists++
	r.mu.Unlock()
	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []Info{{Identity: model.ProcessIdentity{PID: 1}}}, nil
}
func (r *sharedFixture) Read(_ context.Context, _ int32) (Info, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	return Info{Identity: r.identity}, nil
}
func TestSharedReaderCoalescesAndProtectsSnapshots(t *testing.T) {
	release := make(chan struct{})
	base := &sharedFixture{block: release, identity: model.ProcessIdentity{PID: 1, CreatedMillis: 10}}
	shared := NewSharedReader(base, func() time.Duration { return time.Hour })
	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, err := shared.List(context.Background()); err != nil {
				t.Error(err)
			}
			if _, err := shared.Read(context.Background(), 1); err != nil {
				t.Error(err)
			}
		}()
	}
	close(release)
	workers.Wait()
	base.mu.Lock()
	lists, reads := base.lists, base.reads
	base.mu.Unlock()
	if lists != 1 || reads != 1 {
		t.Fatalf("lists=%d reads=%d", lists, reads)
	}
	list, _ := shared.List(context.Background())
	list[0].PPID = 99
	list, _ = shared.List(context.Background())
	if list[0].PPID != 0 {
		t.Fatal("snapshot mutated by consumer")
	}
}
func TestFreshReaderBypassesAllCacheLayers(t *testing.T) {
	base := &sharedFixture{identity: model.ProcessIdentity{PID: 1, CreatedMillis: 10}}
	shared := NewSharedReader(NewSharedReader(base, func() time.Duration { return time.Hour }), func() time.Duration { return time.Hour })
	old, _ := shared.Read(context.Background(), 1)
	base.mu.Lock()
	base.identity.CreatedMillis = 20
	base.mu.Unlock()
	stale, _ := shared.Read(context.Background(), 1)
	fresh, _ := FreshReader(shared).Read(context.Background(), 1)
	if stale.Identity != old.Identity || fresh.Identity.CreatedMillis != 20 {
		t.Fatalf("stale=%+v fresh=%+v", stale, fresh)
	}
}
func TestSharedReaderWaiterCancellation(t *testing.T) {
	release := make(chan struct{})
	base := &sharedFixture{block: release}
	shared := NewSharedReader(base, func() time.Duration { return time.Hour })
	finished := make(chan struct{})
	go func() { shared.List(context.Background()); close(finished) }()
	deadline := time.Now().Add(time.Second)
	for {
		base.mu.Lock()
		started := base.lists > 0
		base.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("list never began")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := shared.List(ctx); err != context.Canceled {
		t.Fatal(err)
	}
	close(release)
	<-finished
}

func TestTopologySinceAvoidsCopiesAndInvalidationAdvancesGeneration(t *testing.T) {
	base := &sharedFixture{}
	shared := NewSharedReader(base, func() time.Duration { return time.Hour })
	first, revision, err := TopologySince(context.Background(), shared, 0)
	if err != nil || revision == 0 || len(first) != 1 {
		t.Fatalf("initial snapshot: %+v rev=%d err=%v", first, revision, err)
	}
	unchanged, same, err := TopologySince(context.Background(), shared, revision)
	if err != nil || same != revision || unchanged != nil {
		t.Fatalf("unchanged generation copied topology: %+v rev=%d err=%v", unchanged, same, err)
	}
	InvalidateTopology(shared)
	next, newRevision, err := TopologySince(context.Background(), shared, revision)
	if err != nil || newRevision <= revision || len(next) != 1 {
		t.Fatalf("did not refresh after launch: %+v rev=%d err=%v", next, newRevision, err)
	}
}
func TestReadIdentitySkipsNativeMetricCollection(t *testing.T) {
	info, err := ReadIdentity(context.Background(), NewReader(), int32(os.Getpid()))
	if err != nil || info.Identity.PID != int32(os.Getpid()) || info.Identity.CreatedMillis <= 0 {
		t.Fatalf("native identity: %+v %v", info, err)
	}
	if info.CPUKnown || info.MemoryKnown || info.MetricKnown {
		t.Fatalf("ownership check collected metrics: %+v", info)
	}
}
