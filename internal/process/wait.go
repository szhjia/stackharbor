package process

import (
	"context"
	"time"
)

type topologyMonitor struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// WaitTopology blocks until a shared topology generation changes or expires.
// One lazy monitor per reader reevaluates dynamic TTLs every100ms, so shortening
// foreground cadence does not leave waiters asleep on an older deadline.
// The monitor and its timer stop when the last waiter leaves. Custom readers
// preserve the previous100ms managed discovery cadence.
func WaitTopology(ctx context.Context, source Reader, after uint64, finished <-chan struct{}) bool {
	select {
	case <-ctx.Done():
		return false
	case <-finished:
		return false
	default:
	}
	shared, ok := source.(*sharedReader)
	if !ok {
		return waitTopologyFallback(ctx, finished)
	}
	shared.mu.Lock()
	ttl := shared.ttl()
	if ttl <= 0 {
		shared.mu.Unlock()
		return waitTopologyFallback(ctx, finished)
	}
	if shared.revision != after || shared.listAt.IsZero() || time.Since(shared.listAt) >= ttl {
		shared.mu.Unlock()
		return true
	}
	changed := shared.changed
	shared.waiters++
	shared.ensureMonitorLocked()
	shared.mu.Unlock()
	defer shared.leaveTopologyWait()
	select {
	case <-ctx.Done():
		return false
	case <-finished:
		return false
	case <-changed:
		return true
	}
}
func (r *sharedReader) topologyChangedLocked() {
	close(r.changed)
	r.changed = make(chan struct{})
}
func (r *sharedReader) ensureMonitorLocked() {
	if r.monitor != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	monitor := &topologyMonitor{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	r.monitor = monitor
	go r.monitorTopology(monitor)
}
func (r *sharedReader) leaveTopologyWait() {
	r.mu.Lock()
	r.waiters--
	var stopping <-chan struct{}
	if r.waiters == 0 && r.monitor != nil {
		r.monitor.cancel()
		stopping = r.monitor.done
	}
	r.mu.Unlock()
	if stopping != nil {
		<-stopping
	}
}
func (r *sharedReader) monitorTopology(monitor *topologyMonitor) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer func() {
		ticker.Stop()
		r.mu.Lock()
		r.monitor = nil
		close(monitor.done)
		if r.waiters > 0 {
			r.ensureMonitorLocked()
		}
		r.mu.Unlock()
	}()
	for {
		select {
		case <-monitor.ctx.Done():
			return
		case <-ticker.C:
			r.mu.Lock()
			if monitor.ctx.Err() == nil && r.waiters > 0 && (r.listAt.IsZero() || time.Since(r.listAt) >= r.ttl()) {
				r.topologyChangedLocked()
			}
			r.mu.Unlock()
		}
	}
}

func waitTopologyFallback(ctx context.Context, finished <-chan struct{}) bool {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-finished:
		return false
	case <-timer.C:
		return true
	}
}
