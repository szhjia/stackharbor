package process

import (
	"context"
	"errors"
	gp "github.com/shirou/gopsutil/v4/process"
	"os"
	"sync"
	"syscall"
	"time"
)

type cachedRead struct {
	info Info
	err  error
	at   time.Time
}
type sharedReader struct {
	base     Reader
	ttl      func() time.Duration
	mu       sync.Mutex
	list     []Info
	listErr  error
	listAt   time.Time
	revision uint64
	listing  chan struct{}
	reads    map[int32]cachedRead
	reading  map[int32]chan struct{}
	changed  chan struct{}
	waiters  int
	monitor  *topologyMonitor
}

// NewSharedReader coalesces concurrent observations and caches at most 4096 PID
// reads. Use separate ownership and adaptive metrics readers. Safety decisions
// must use fresh topology and identity checks.
func NewSharedReader(base Reader, ttl func() time.Duration) Reader {
	if ttl == nil {
		ttl = func() time.Duration { return time.Second }
	}
	return &sharedReader{base: base, ttl: ttl, reads: map[int32]cachedRead{}, reading: map[int32]chan struct{}{}, changed: make(chan struct{})}
}

// FreshReader bypasses observation caches for identity and topology checks.
func FreshReader(reader Reader) Reader {
	for {
		shared, ok := reader.(*sharedReader)
		if !ok {
			return reader
		}
		reader = shared.base
	}
}

// FreshTopology bypasses observation caches and uses the native managed
// production topology path where available, without changing custom readers.
func FreshTopology(ctx context.Context, source Reader) ([]Info, error) {
	return platformTopology(ctx, FreshReader(source))
}

func (r *sharedReader) List(ctx context.Context) ([]Info, error) {
	all, _, err := r.topology(ctx, 0)
	return all, err
}

// TopologySince returns no copied topology when the shared generation is
// unchanged. Non-shared readers always return a fresh list and revision zero.
func TopologySince(ctx context.Context, reader Reader, after uint64) ([]Info, uint64, error) {
	if shared, ok := reader.(*sharedReader); ok {
		return shared.topology(ctx, after)
	}
	all, err := platformTopology(ctx, reader)
	return all, 0, err
}
func (r *sharedReader) topology(ctx context.Context, after uint64) ([]Info, uint64, error) {
	for {
		r.mu.Lock()
		if !r.listAt.IsZero() && time.Since(r.listAt) < r.ttl() {
			var out []Info
			if r.revision != after {
				out = append([]Info(nil), r.list...)
			}
			revision := r.revision
			err := r.listErr
			r.mu.Unlock()
			return out, revision, err
		}
		if wait := r.listing; wait != nil {
			r.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			}
		}
		wait := make(chan struct{})
		r.listing = wait
		r.mu.Unlock()
		out, err := platformTopology(ctx, r.base)
		r.mu.Lock()
		r.list = append([]Info(nil), out...)
		r.listErr = err
		r.listAt = time.Now()
		r.revision++
		r.topologyChangedLocked()
		revision := r.revision
		r.listing = nil
		close(wait)
		r.mu.Unlock()
		return out, revision, err
	}
}
func (r *sharedReader) Read(ctx context.Context, pid int32) (Info, error) {
	for {
		r.mu.Lock()
		if value, ok := r.reads[pid]; ok && time.Since(value.at) < r.ttl() {
			r.mu.Unlock()
			return value.info, value.err
		}
		if wait := r.reading[pid]; wait != nil {
			r.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return Info{}, ctx.Err()
			}
		}
		wait := make(chan struct{})
		r.reading[pid] = wait
		r.mu.Unlock()
		var value Info
		var err error
		if _, native := r.base.(reader); native {
			value, err = sharedNativeMetrics(ctx, pid)
		} else {
			value, err = r.base.Read(ctx, pid)
		}
		r.mu.Lock()
		if len(r.reads) >= 4096 {
			for key := range r.reads {
				delete(r.reads, key)
				break
			}
		}
		r.reads[pid] = cachedRead{value, err, time.Now()}
		delete(r.reading, pid)
		close(wait)
		r.mu.Unlock()
		return value, err
	}
}

// InvalidateTopology ensures the next shared observation includes a newly
// launched process and its children instead of a pre-launch topology snapshot.
func InvalidateTopology(reader Reader) {
	for {
		shared, ok := reader.(*sharedReader)
		if !ok {
			return
		}
		shared.mu.Lock()
		shared.listAt = time.Time{}
		shared.topologyChangedLocked()
		shared.mu.Unlock()
		reader = shared.base
	}
}

// ReadIdentity performs a fresh ownership check without collecting CPU/RSS on
// the native reader. Custom reader contracts continue to use their Read method.
func ReadIdentity(ctx context.Context, source Reader, pid int32) (Info, error) {
	base := FreshReader(source)
	if _, ok := base.(reader); !ok {
		return base.Read(ctx, pid)
	}
	info, err := nativeIdentity(ctx, pid)
	if err != nil && (errors.Is(err, syscall.ESRCH) || errors.Is(syscall.Kill(int(pid), 0), syscall.ESRCH)) {
		return Info{}, os.ErrNotExist
	}
	return info, err
}

// sharedNativeMetrics avoids the Darwin per-PID ps status helper while keeping
// memory/CPU availability independent, matching the ordinary Reader contract.
func sharedNativeMetrics(ctx context.Context, pid int32) (Info, error) {
	info, err := ReadIdentity(ctx, reader{}, pid)
	if err != nil {
		return Info{}, err
	}
	proc, err := gp.NewProcessWithContext(ctx, pid)
	if err != nil {
		return Info{}, err
	}
	if memory, err := proc.MemoryInfoWithContext(ctx); err == nil {
		info.RSS = memory.RSS
		info.MemoryKnown = true
	}
	if cpu, err := proc.TimesWithContext(ctx); err == nil {
		info.CPUSeconds = cpu.User + cpu.System
		info.CPUKnown = true
	}
	info.MetricKnown = info.MemoryKnown && info.CPUKnown
	return info, nil
}
