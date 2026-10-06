package web

import (
	"context"
	"github.com/szhjia/stackharbor/internal/inventory"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"sync"
	"time"
)

const discoveryInterval = 2 * time.Second
const statusInterval = time.Second
const staleAfter = 5 * time.Second

// discoveryCache is private to the single refresh worker. Fresh mutation checks
// use Gateway.collect's uncached registry source instead.
type discoveryCache struct {
	source  inventory.Source
	now     func() time.Time
	checked time.Time
	infos   []supervisor.SessionInfo
}

func (s *discoveryCache) ListSessions(ctx context.Context) ([]supervisor.SessionInfo, error) {
	now := s.now()
	if s.checked.IsZero() || now.Sub(s.checked) >= discoveryInterval {
		infos, err := s.source.ListSessions(ctx)
		if err != nil {
			return nil, err
		}
		s.infos = infos
		s.checked = now
	}
	return s.infos, nil
}
func (s *discoveryCache) Connect(ctx context.Context, i supervisor.SessionInfo) (inventory.SnapshotClient, error) {
	return s.source.Connect(ctx, i)
}

type liveInventory struct {
	mu        sync.RWMutex
	now       func() time.Time
	source    *discoveryCache
	collector *inventory.Collector
	collect   func(context.Context) (inventory.Inventory, error)
	hub       *EventHub
	current   inventory.Inventory
	ready     bool
}

func newLiveInventory(source inventory.Source, collect func(context.Context) (inventory.Inventory, error), hub *EventHub) *liveInventory {
	l := &liveInventory{now: time.Now, collect: collect, hub: hub, current: inventory.Inventory{Sessions: []inventory.Session{}, Resources: []inventory.Resource{}}}
	l.source = &discoveryCache{source: source, now: func() time.Time { return l.now() }}
	l.collector = inventory.New(l.source, inventory.Options{})
	return l
}
func (l *liveInventory) refresh(ctx context.Context) {
	collect := l.collect
	if collect == nil {
		collect = l.collector.Collect
	}
	// Bound the whole fan-out cycle so a batch of unreachable sessions cannot
	// stretch the one-second refresh cadence into repeated two-second waves.
	request, cancel := context.WithTimeout(ctx, statusInterval)
	out, err := collect(request)
	cancel()
	if ctx.Err() != nil {
		return
	}
	now := l.now().UTC()
	l.mu.Lock()
	old := map[string]inventory.Session{}
	for _, s := range l.current.Sessions {
		old[s.Identity.SessionID] = s
	}
	if err != nil {
		out = l.current
		out.CollectionError = apiError("unavailable", "Inventory discovery unavailable")
		out.Sessions = append([]inventory.Session{}, out.Sessions...)
		for i := range out.Sessions {
			out.Sessions[i].Available = false
			out.Sessions[i].Error = apiError("unavailable", "Inventory discovery unavailable")
		}
	} else {
		out.CollectionError = nil
		l.ready = true
		for i := range out.Sessions {
			row := &out.Sessions[i]
			prior := old[row.Identity.SessionID]
			if !row.Available && row.Identity.WorkspaceID == prior.Identity.WorkspaceID {
				row.Snapshot = prior.Snapshot
				row.LastSeen = prior.LastSeen
			}
		}
	}
	// CollectedAt reflects the last attempted refresh, while ObservedAt and
	// LastSeen remain original observation/connection times on failures.
	out.CollectedAt = now
	l.current = out
	l.mu.Unlock()
	if l.hub != nil {
		l.hub.Publish("inventory", map[string]any{"collected_at": now})
	}
}
func (l *liveInventory) snapshot() inventory.Inventory {
	l.mu.RLock()
	out := l.current
	out.Sessions = append([]inventory.Session{}, out.Sessions...)
	l.mu.RUnlock()
	now := l.now()
	for i := range out.Sessions {
		s := &out.Sessions[i]
		s.Stale = s.Snapshot == nil || s.Snapshot.ObservedAt.IsZero() || now.Sub(s.Snapshot.ObservedAt) > staleAfter
	}
	return inventory.Project(out)
}
func (l *liveInventory) run(ctx context.Context) {
	l.refresh(ctx)
	ticker := time.NewTicker(statusInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.refresh(ctx)
		}
	}
}

// Start attaches collection and streaming to the foreground gateway lifetime.
// It starts no session hosts and never stops independently collected session logs.
func (g *Gateway) Start(ctx context.Context) {
	g.liveMu.Lock()
	if g.live != nil {
		g.liveMu.Unlock()
		return
	}
	g.live = newLiveInventory(registrySource{g.options.Namespace}, g.options.Collect, g.hub)
	live := g.live
	g.liveMu.Unlock()
	go live.run(ctx)
	go func() { <-ctx.Done(); g.hub.Close() }()
}
func (g *Gateway) presentation(ctx context.Context) (inventory.Inventory, error) {
	g.liveMu.RLock()
	l := g.live
	g.liveMu.RUnlock()
	if l == nil {
		return g.collect(ctx)
	}
	l.mu.RLock()
	ready := l.ready
	failure := l.current.CollectionError
	l.mu.RUnlock()
	if !ready {
		if failure != nil {
			return inventory.Inventory{}, failure
		}
		return inventory.Inventory{}, apiError("unavailable", "Inventory initial refresh pending")
	}
	return l.snapshot(), nil
}
