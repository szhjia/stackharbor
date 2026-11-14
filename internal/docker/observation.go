package docker

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/szhjia/stackharbor/internal/execenv"
)

const observationMaxEntries = 256
const observationMaxBytes = 16 * 1024 * 1024
const observationMaxResult = 8 * 1024 * 1024
const observationMaxPending = 32
const observationMaxIDs = 4096

type observationEntry struct {
	key   string
	raw   []byte
	err   error
	at    time.Time
	bytes int
}
type observationFlight struct {
	done chan struct{}
	raw  []byte
	err  error
}
type observationDemand struct {
	ids []string
	err error
}
type observationCycle struct{ demands map[string]observationDemand }
type observationContextKey struct{}

// ObservationCache shares only explicitly marked read-only sampling commands.
// It retains no managers, environments or background workers. At most 256 raw
// results / 16MiB and 32 in-flight requests are held engine-wide.
type ObservationCache struct {
	ttl        func() time.Duration
	mu         sync.Mutex
	entries    map[string]*list.Element
	order      *list.List
	bytes      int
	pending    map[string]*observationFlight
	generation uint64
}

func NewObservationCache(ttl func() time.Duration) *ObservationCache {
	if ttl == nil {
		ttl = func() time.Duration { return 2 * time.Second }
	}
	return &ObservationCache{ttl: ttl, entries: map[string]*list.Element{}, order: list.New(), pending: map[string]*observationFlight{}}
}
func (m *Manager) SetObservationCache(cache *ObservationCache) { m.observer.Store(cache) }

// WithObservation opts a context into UI sampling. Mutation, Identity, Binding,
// Pin and CheckResolved never call the cached sampling command path.
func WithObservation(ctx context.Context) context.Context {
	if ctx.Value(observationContextKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, observationContextKey{}, &observationCycle{})
}
func (m *Manager) observationCache(ctx context.Context) *ObservationCache {
	if ctx.Value(observationContextKey{}) == nil {
		return nil
	}
	return m.observer.Load()
}
func digestObservation(value any) string {
	raw, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func (m *Manager) observationEnvironment() []string {
	if m.env != nil {
		return m.env
	}
	return os.Environ()
}

// The context includes Docker transport, credentials/configuration, proxy and
// executable selection. Relative security/tool paths additionally bind cwd.
// Compose commands also bind the entire frozen environment and execution root.
func (m *Manager) observationContext() string {
	environment := m.observationEnvironment()
	selected := map[string]string{}
	rootSensitive := false
	for _, pair := range environment {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		if dockerTargetVariable(key) || strings.HasPrefix(key, "DOCKER_") || key == "PATH" || key == "LANG" || strings.HasPrefix(key, "LC_") || strings.Contains(strings.ToUpper(key), "PROXY") {
			selected[key] = value
		}
		switch key {
		case "HOME", "USERPROFILE", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "SSL_CERT_FILE", "SSL_CERT_DIR", "SSH_AUTH_SOCK":
			if value != "" && !filepath.IsAbs(value) {
				rootSensitive = true
			}
		case "PATH":
			for _, entry := range filepath.SplitList(value) {
				if !filepath.IsAbs(entry) {
					rootSensitive = true
				}
			}
		case "DOCKER_HOST":
			if strings.HasPrefix(value, "unix://") && !filepath.IsAbs(strings.TrimPrefix(value, "unix://")) {
				rootSensitive = true
			}
		}
	}
	executable, _ := execenv.LookPath("docker", environment, m.root)
	root := ""
	if rootSensitive {
		root, _ = filepath.Abs(m.root)
	}
	var binary any
	if info, err := os.Stat(executable); err == nil {
		binary = []any{info.Size(), info.ModTime().UnixNano(), info.Mode()}
	}
	return digestObservation([]any{selected, executable, binary, root})
}
func (m *Manager) observationRun(ctx context.Context, args ...string) ([]byte, error) {
	cache := m.observationCache(ctx)
	if cache == nil {
		return m.run(ctx, args...)
	}
	scope := ""
	if len(args) > 0 && args[0] == "compose" {
		scope = digestObservation([]any{m.root, m.observationEnvironment()})
	}
	key := digestObservation([]any{m.observationContext(), scope, args})
	return cache.fetch(ctx, key, func() ([]byte, error) { return m.run(ctx, args...) })
}
func (c *ObservationCache) fetch(ctx context.Context, key string, run func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	generation := c.generation
	key = fmt.Sprintf("%d:%s", generation, key)
	if element := c.entries[key]; element != nil {
		entry := element.Value.(observationEntry)
		if time.Since(entry.at) < c.ttl() {
			c.order.MoveToBack(element)
			raw := append([]byte(nil), entry.raw...)
			c.mu.Unlock()
			return raw, entry.err
		}
		c.bytes -= entry.bytes
		c.order.Remove(element)
		delete(c.entries, key)
	}
	if flight := c.pending[key]; flight != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-flight.done:
			return append([]byte(nil), flight.raw...), flight.err
		}
	}
	if len(c.pending) >= observationMaxPending {
		c.mu.Unlock()
		return nil, errors.New("Docker observation concurrency limit reached")
	}
	flight := &observationFlight{done: make(chan struct{})}
	c.pending[key] = flight
	c.mu.Unlock()
	raw, err := run()
	if len(raw) > observationMaxResult {
		raw = nil
		err = errors.New("Docker observation output exceeds limit")
	}
	if err != nil && len(err.Error()) > 4096 {
		err = errors.New("Docker observation failed; error output exceeds limit")
	}
	owned := append([]byte(nil), raw...)
	size := len(owned) + len(key) + 256
	if err != nil {
		size += len(err.Error())
	}
	c.mu.Lock()
	if generation != c.generation {
		raw = nil
		owned = nil
		err = errors.New("Docker observation superseded by an operation")
	}
	flight.raw, flight.err = owned, err
	if ctx.Err() == nil && generation == c.generation {
		entry := observationEntry{key: key, raw: owned, err: err, at: time.Now(), bytes: size}
		c.entries[key] = c.order.PushBack(entry)
		c.bytes += size
		for len(c.entries) > observationMaxEntries || c.bytes > observationMaxBytes {
			first := c.order.Front()
			old := first.Value.(observationEntry)
			delete(c.entries, old.key)
			c.bytes -= old.bytes
			c.order.Remove(first)
		}
	}
	delete(c.pending, key)
	close(flight.done)
	c.mu.Unlock()
	return raw, err
}

// Prepare first observes all current managers, then samples the union of their
// running IDs once per Docker context. The returned context carries immutable
// demand sets for the subsequent host sampling cycle. No manager registration
// or unregister operation is needed, and empty sets never invoke docker stats.
// Callers supply an overall deadline; individual status/stats reads are bounded.
func (c *ObservationCache) Prepare(ctx context.Context, managers []*Manager) context.Context {
	cycle := &observationCycle{demands: map[string]observationDemand{}}
	prepared := context.WithValue(ctx, observationContextKey{}, cycle)
	sets := map[string]map[string]bool{}
	representatives := map[string]*Manager{}
	seen := map[*Manager]bool{}
	total := 0
	for _, manager := range managers {
		if ctx.Err() != nil {
			break
		}
		if manager == nil || seen[manager] || manager.observer.Load() != c {
			continue
		}
		seen[manager] = true
		key := manager.observationContext()
		if sets[key] == nil {
			if len(sets) >= observationMaxEntries {
				continue
			}
			sets[key] = map[string]bool{}
			representatives[key] = manager
		}
		rows, reason := manager.Observe(prepared)
		if reason != "" {
			continue
		}
		for _, row := range rows {
			if row.State == "running" && row.ID != "" && !strings.HasPrefix(row.ID, "-") && !sets[key][row.ID] {
				if total >= observationMaxIDs {
					cycle.demands[key] = observationDemand{err: errors.New("Docker observation container demand exceeds limit")}
					continue
				}
				sets[key][row.ID] = true
				total++
			}
		}
	}
	for key, set := range sets {
		if cycle.demands[key].err != nil {
			continue
		}
		ids := make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		cycle.demands[key] = observationDemand{ids: ids}
		if len(ids) > 0 && ctx.Err() == nil {
			bounded, cancel := context.WithTimeout(prepared, 4*time.Second)
			representatives[key].sampleStats(bounded, ids)
			cancel()
		}
	}
	return prepared
}

func (m *Manager) sampleStats(ctx context.Context, requested []string) ([]byte, error) {
	ids := requested
	if cache := m.observationCache(ctx); cache != nil {
		if cycle, _ := ctx.Value(observationContextKey{}).(*observationCycle); cycle != nil {
			if demand, exists := cycle.demands[m.observationContext()]; exists {
				if demand.err != nil {
					return nil, demand.err
				}
				// A new container appearing after Prepare gets a bounded supplemental
				// request rather than a zero-ID/all-container fallback.
				set := map[string]bool{}
				for _, id := range demand.ids {
					set[id] = true
				}
				for _, id := range requested {
					set[id] = true
				}
				if len(set) > observationMaxIDs {
					return nil, errors.New("Docker observation container demand exceeds limit")
				}
				ids = make([]string, 0, len(set))
				for id := range set {
					ids = append(ids, id)
				}
				sort.Strings(ids)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	args := append([]string{"stats", "--no-stream", "--no-trunc", "--format", "{{json .}}"}, ids...)
	return m.observationRun(ctx, args...)
}

// invalidate also separates pending requests by generation: a read started
// before a potentially partial mutation cannot repopulate or satisfy the new
// observation epoch. Requests already running remain bounded by their context.
func (c *ObservationCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.entries = map[string]*list.Element{}
	c.order.Init()
	c.bytes = 0
}
