package observe

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
)

type listenerSnapshot struct {
	listeners map[int][]model.Listener
	known     bool
	reason    string
}
type sharedPortProbe struct {
	base     PortProbe
	ttl      func() time.Duration
	mu       sync.Mutex
	snapshot listenerSnapshot
	at       time.Time
	pending  chan struct{}
	raw      func(context.Context) listenerSnapshot
	reader   process.Reader
}

// NewSharedPortProbe caches raw system listeners, never a workspace ownership
// projection. Custom probes lacking raw listener access are delegated uncached.
// CheckStart always performs the base probe's fresh checks.
func NewSharedPortProbe(base PortProbe, ttl func() time.Duration) PortProbe {
	if ttl == nil {
		ttl = func() time.Duration { return 2 * time.Second }
	}
	probe := &sharedPortProbe{base: base, ttl: ttl, reader: process.NewReader()}
	if _, ok := base.(portProbe); ok {
		probe.raw = scanSharedListeners
	}
	return probe
}
func scanSharedListeners(ctx context.Context) listenerSnapshot {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcn"}
	if runtime.GOOS == "darwin" {
		args = append(args, "-b", "-w")
	}
	data, err := exec.CommandContext(ctx, "lsof", args...).Output()
	result := listenerSnapshot{known: true}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || len(exit.Stderr) > 0 || ctx.Err() != nil {
			result.known = false
			result.reason = err.Error()
		}
	}
	result.listeners, err = parsePorts(data)
	if err != nil {
		result.known = false
		result.reason = err.Error()
	}
	return result
}
func (p *sharedPortProbe) load(ctx context.Context) listenerSnapshot {
	for {
		p.mu.Lock()
		if !p.at.IsZero() && time.Since(p.at) < p.ttl() {
			result := p.snapshot
			p.mu.Unlock()
			return result
		}
		if wait := p.pending; wait != nil {
			p.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return listenerSnapshot{reason: ctx.Err().Error()}
			}
		}
		wait := make(chan struct{})
		p.pending = wait
		p.mu.Unlock()
		result := p.raw(ctx)
		p.mu.Lock()
		p.snapshot = result
		p.at = time.Now()
		p.pending = nil
		close(wait)
		p.mu.Unlock()
		return result
	}
}
func (p *sharedPortProbe) Observe(ctx context.Context, declared []model.Port, owned []model.ProcessIdentity) []model.PortObservation {
	if p.raw == nil {
		return p.base.Observe(ctx, declared, owned)
	}
	if len(declared) == 0 && len(owned) == 0 {
		return nil
	}
	snapshot := p.load(ctx)
	ownedPIDs := map[int32]bool{}
	for _, id := range owned {
		if info, err := process.ReadIdentity(ctx, p.reader, id.PID); err == nil && info.Identity == id {
			ownedPIDs[id.PID] = true
		}
	}
	ports := map[int]bool{}
	for _, port := range declared {
		ports[port.Number] = true
	}
	for port, listeners := range snapshot.listeners {
		for _, listener := range listeners {
			if ownedPIDs[listener.PID] {
				ports[port] = true
			}
		}
	}
	nums := make([]int, 0, len(ports))
	for port := range ports {
		nums = append(nums, port)
	}
	sort.Ints(nums)
	out := make([]model.PortObservation, 0, len(nums))
	for _, port := range nums {
		observation := model.PortObservation{Port: port, Status: "free", Listeners: append([]model.Listener(nil), snapshot.listeners[port]...)}
		if !snapshot.known {
			observation.Status = "unknown"
			observation.Reason = snapshot.reason
		} else if len(observation.Listeners) > 0 {
			observation.Status = "owned"
			for _, listener := range observation.Listeners {
				if !ownedPIDs[listener.PID] {
					observation.Status = "external"
				}
			}
		}
		out = append(out, observation)
	}
	return out
}
func (p *sharedPortProbe) CheckStart(ctx context.Context, ports []model.Port) error {
	return p.base.CheckStart(ctx, ports)
}
