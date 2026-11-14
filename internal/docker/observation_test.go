package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/model"
)

type observationFixture struct {
	mu     sync.Mutex
	daemon string
	ids    map[string]string
	states map[string]string
	calls  [][]string
}

func (f *observationFixture) run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	switch args[0] {
	case "info":
		return json.Marshal(f.daemon)
	case "compose":
		project := ""
		for i, arg := range args {
			if arg == "-p" && i+1 < len(args) {
				project = args[i+1]
			}
		}
		for _, arg := range args {
			if arg == "config" {
				return json.Marshal(map[string]string{"name": project})
			}
		}
		return json.Marshal([]container{{ID: f.ids[project], Service: "db", State: f.states[project]}})
	case "stats":
		var out strings.Builder
		for _, id := range args[5:] {
			fmt.Fprintf(&out, "{\"Container\":%q,\"CPUPerc\":\"1%%\",\"MemUsage\":\"2MiB / 8GiB\"}\n", id)
		}
		return []byte(out.String()), nil
	}
	return nil, errors.New("unexpected Docker command")
}
func (f *observationFixture) manager(root, project string) *Manager {
	return &Manager{root: root, File: filepath.Join(root, "compose.yaml"), Project: project, specs: []model.DockerSnapshot{{Service: "db"}}, env: []string{"PATH=/usr/bin:/bin", "HOME=/shared-home"}, run: f.run}
}
func (f *observationFixture) count(command string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, args := range f.calls {
		if args[0] == command {
			n++
		}
	}
	return n
}
func TestSharedObservationTargetsUnionOncePerContext(t *testing.T) {
	fixture := &observationFixture{daemon: "same-daemon", ids: map[string]string{"one": "one-id", "two": "two-id"}, states: map[string]string{"one": "running", "two": "running"}}
	cache := NewObservationCache(func() time.Duration { return time.Hour })
	first, second := fixture.manager(t.TempDir(), "one"), fixture.manager(t.TempDir(), "two")
	first.SetObservationCache(cache)
	second.SetObservationCache(cache)
	ctx := cache.Prepare(context.Background(), []*Manager{first, second, first})
	for _, manager := range []*Manager{first, second} {
		rows, reason := manager.Observe(ctx)
		if reason != "" {
			t.Fatal(reason)
		}
		sampled := manager.SampleMetrics(ctx, rows)
		if len(sampled) != 1 || !sampled[0].Metric.Known || sampled[0].Metric.RSS != 2*1024*1024 {
			t.Fatalf("union projection lost caller's sample: %+v", sampled)
		}
	}
	if fixture.count("info") != 1 || fixture.count("stats") != 1 {
		t.Fatalf("context commands repeated: %+v", fixture.calls)
	}
	for _, args := range fixture.calls {
		if args[0] == "stats" && !reflect.DeepEqual(args[5:], []string{"one-id", "two-id"}) {
			t.Fatalf("stats target set incorrect: %v", args)
		}
	}
}
func TestObservationContextSeparatesEndpointSecurityAndToolchains(t *testing.T) {
	for _, key := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_CONFIG", "DOCKER_AUTH_CONFIG", "PATH", "SSH_AUTH_SOCK", "HTTPS_PROXY"} {
		t.Run(key, func(t *testing.T) {
			fixture := &observationFixture{daemon: "daemon", ids: map[string]string{"one": "id"}, states: map[string]string{"one": "running"}}
			first, second := fixture.manager(t.TempDir(), "one"), fixture.manager(t.TempDir(), "one")
			first.env = append(first.env, key+"=/first")
			second.env = append(second.env, key+"=/second")
			cache := NewObservationCache(func() time.Duration { return time.Hour })
			first.SetObservationCache(cache)
			second.SetObservationCache(cache)
			if first.observationContext() == second.observationContext() {
				t.Fatal("distinct execution/security contexts shared a key")
			}
			ctx := cache.Prepare(context.Background(), []*Manager{first, second})
			for _, manager := range []*Manager{first, second} {
				rows, _ := manager.Observe(ctx)
				manager.SampleMetrics(ctx, rows)
			}
			if fixture.count("info") != 2 || fixture.count("stats") != 2 {
				t.Fatalf("cross-context cache reuse: %v", fixture.calls)
			}
		})
	}
	first := &Manager{root: "/first", env: []string{"PATH=tools"}}
	second := &Manager{root: "/second", env: []string{"PATH=tools"}}
	if first.observationContext() == second.observationContext() {
		t.Fatal("relative tool/security paths ignored execution root")
	}
}
func TestSamplingNeverCachesFreshIdentityBindingOrUnmarkedObserve(t *testing.T) {
	fixture := &observationFixture{daemon: "old-daemon", ids: map[string]string{"one": "old-id"}, states: map[string]string{"one": "running"}}
	manager := fixture.manager(t.TempDir(), "one")
	manager.SetObservationCache(NewObservationCache(func() time.Duration { return time.Hour }))
	ctx := WithObservation(context.Background())
	rows, reason := manager.Observe(ctx)
	if reason != "" {
		t.Fatal(reason)
	}
	endpoint := rows[0].EndpointIdentity
	fixture.daemon = "new-daemon"
	fixture.ids["one"] = "new-id"
	cached, _ := manager.Observe(ctx)
	if cached[0].ID != "old-id" {
		t.Fatal("fixture failed to warm observation cache")
	}
	identity, err := manager.Identity(ctx)
	if err != nil || identity.Endpoint == endpoint {
		t.Fatalf("fresh identity used observation cache: %+v %v", identity, err)
	}
	bindingBefore, err := manager.Binding(ctx, []string{"db"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.ids["one"] = "another-id"
	bindingAfter, err := manager.Binding(ctx, []string{"db"})
	if err != nil || bindingAfter == bindingBefore {
		t.Fatal("fresh binding used cached status", err)
	}
	fresh, _ := manager.Observe(context.Background())
	if fresh[0].ID != "another-id" {
		t.Fatal("unmarked readiness/status observation was cached")
	}
}
func TestObservationDoesNotRunStatsWithoutRunningIDs(t *testing.T) {
	fixture := &observationFixture{daemon: "daemon", ids: map[string]string{"one": "stopped-id"}, states: map[string]string{"one": "exited"}}
	manager := fixture.manager(t.TempDir(), "one")
	cache := NewObservationCache(func() time.Duration { return time.Hour })
	manager.SetObservationCache(cache)
	ctx := cache.Prepare(context.Background(), []*Manager{manager})
	rows, _ := manager.Observe(ctx)
	manager.SampleMetrics(ctx, rows)
	manager.SampleMetrics(ctx, nil)
	if fixture.count("stats") != 0 {
		t.Fatalf("empty demand sampled entire engine: %v", fixture.calls)
	}
}
func TestAdoptedMutationPinAndResolvedChecksBypassObservationCache(t *testing.T) {
	manager := NewAdopted(t.TempDir(), "demo", []string{"db"})
	manager.env = []string{"PATH=/usr/bin:/bin"}
	current := strings.Repeat("a", 64)
	mutations := 0
	manager.run = func(_ context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "info":
			return []byte(`"daemon"`), nil
		case "ps":
			return []byte(current + " db\n"), nil
		case "inspect":
			return []byte(`[{"Id":"` + current + `","Config":{"Labels":{"com.docker.compose.project":"demo","com.docker.compose.service":"db"}},"State":{"Status":"running"}}]`), nil
		case "container":
			mutations++
			return nil, nil
		}
		return nil, errors.New("unexpected command")
	}
	manager.SetObservationCache(NewObservationCache(func() time.Duration { return time.Hour }))
	ctx := WithObservation(context.Background())
	if err := manager.Pin(ctx); err != nil {
		t.Fatal(err)
	}
	if _, reason := manager.Observe(ctx); reason != "" {
		t.Fatal(reason)
	}
	current = strings.Repeat("b", 64)
	if err := manager.CheckResolved(ctx); err == nil {
		t.Fatal("CheckResolved accepted stale cached container")
	}
	if err := manager.Action(ctx, "stop", []string{"db"}); err == nil || mutations != 0 {
		t.Fatal("mutation accepted stale cached identity", err)
	}
	if err := manager.Pin(ctx); err != nil || manager.pinned["db"] != current {
		t.Fatal("Pin used cached observation", err)
	}
}
func TestObservationCacheSingleflightAndBoundedStorage(t *testing.T) {
	cache := NewObservationCache(func() time.Duration { return time.Hour })
	var calls atomic.Int32
	release := make(chan struct{})
	var workers sync.WaitGroup
	for range 24 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			raw, err := cache.fetch(context.Background(), "same", func() ([]byte, error) { calls.Add(1); <-release; return []byte("sample"), nil })
			if err != nil || string(raw) != "sample" {
				t.Errorf("sample=%q err=%v", raw, err)
			}
		}()
	}
	close(release)
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent observation invoked %d commands", calls.Load())
	}
	for n := range observationMaxEntries + 4 {
		cache.fetch(context.Background(), fmt.Sprint(n), func() ([]byte, error) { return []byte("sample"), nil })
	}
	cache.mu.Lock()
	entries, bytes := len(cache.entries), cache.bytes
	cache.mu.Unlock()
	if entries > observationMaxEntries || bytes > observationMaxBytes {
		t.Fatalf("cache unbounded: entries=%d bytes=%d", entries, bytes)
	}
	for n := range 3 {
		cache.fetch(context.Background(), fmt.Sprint("large", n), func() ([]byte, error) { return make([]byte, 6*1024*1024), nil })
	}
	cache.mu.Lock()
	bytes = cache.bytes
	cache.mu.Unlock()
	if bytes > observationMaxBytes {
		t.Fatal("byte bound exceeded", bytes)
	}
	raw, err := cache.fetch(context.Background(), "oversize", func() ([]byte, error) { return make([]byte, observationMaxResult+1), nil })
	if err == nil || raw != nil {
		t.Fatal("oversized result retained")
	}
}
func TestObservationCacheCapsPendingCommandsAndHonorsCancellation(t *testing.T) {
	cache := NewObservationCache(func() time.Duration { return time.Hour })
	release := make(chan struct{})
	started := make(chan struct{}, observationMaxPending)
	var workers sync.WaitGroup
	for n := range observationMaxPending {
		workers.Add(1)
		go func(n int) {
			defer workers.Done()
			cache.fetch(context.Background(), fmt.Sprint(n), func() ([]byte, error) { started <- struct{}{}; <-release; return nil, nil })
		}(n)
	}
	for range observationMaxPending {
		<-started
	}
	if _, err := cache.fetch(context.Background(), "extra", func() ([]byte, error) { t.Error("pending command bound bypassed"); return nil, nil }); err == nil {
		t.Fatal("concurrency bound missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.fetch(ctx, "0", func() ([]byte, error) { t.Error("waiter reran command"); return nil, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("waiter cancellation lost", err)
	}
	close(release)
	workers.Wait()
}

func TestActionInvalidatesObservationAfterSuccessAndUncertainFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			fixture := &observationFixture{daemon: "mutation-fixture", ids: map[string]string{"one": "old-id"}, states: map[string]string{"one": "running"}}
			manager := fixture.manager(t.TempDir(), "one")
			manager.run = func(ctx context.Context, args ...string) ([]byte, error) {
				for _, arg := range args {
					if arg == "up" {
						fixture.mu.Lock()
						fixture.ids["one"] = "new-id"
						fixture.mu.Unlock()
						if fail {
							return nil, errors.New("write failed after changing container")
						}
						return nil, nil
					}
				}
				return fixture.run(ctx, args...)
			}
			manager.SetObservationCache(NewObservationCache(func() time.Duration { return time.Hour }))
			ctx := WithObservation(context.Background())
			rows, reason := manager.Observe(ctx)
			if reason != "" {
				t.Fatal(reason)
			}
			if rows = manager.SampleMetrics(ctx, rows); !rows[0].Metric.Known || rows[0].ID != "old-id" {
				t.Fatal("could not warm observation", rows)
			}
			err := manager.Action(ctx, "start", []string{"db"})
			if (err != nil) != fail {
				t.Fatalf("action error=%v wantFailure=%v", err, fail)
			}
			fresh, reason := manager.Observe(ctx)
			if reason != "" || fresh[0].ID != "new-id" {
				t.Fatalf("post-operation status remained stale: %+v reason=%s", fresh, reason)
			}
			fresh = manager.SampleMetrics(ctx, fresh)
			if !fresh[0].Metric.Known || fixture.count("stats") != 2 {
				t.Fatalf("post-operation stats were not refreshed: %+v calls=%v", fresh, fixture.calls)
			}
		})
	}
}
func TestInvalidationSeparatesPendingReadGenerations(t *testing.T) {
	cache := NewObservationCache(func() time.Duration { return time.Hour })
	started, release := make(chan struct{}), make(chan struct{})
	type outcome struct {
		raw []byte
		err error
	}
	old := make(chan outcome, 1)
	go func() {
		raw, err := cache.fetch(context.Background(), "status", func() ([]byte, error) { close(started); <-release; return []byte("old"), nil })
		old <- outcome{raw, err}
	}()
	<-started
	cache.invalidate()
	fresh, err := cache.fetch(context.Background(), "status", func() ([]byte, error) { return []byte("new"), nil })
	if err != nil || string(fresh) != "new" {
		t.Fatalf("new read joined stale in-flight result: %q %v", fresh, err)
	}
	close(release)
	stale := <-old
	if stale.err == nil || stale.raw != nil {
		t.Fatalf("superseded request returned stale data: %q %v", stale.raw, stale.err)
	}
	cached, err := cache.fetch(context.Background(), "status", func() ([]byte, error) { t.Error("fresh generation was overwritten or lost"); return nil, nil })
	if err != nil || string(cached) != "new" {
		t.Fatalf("stale in-flight request repopulated cache: %q %v", cached, err)
	}
}
