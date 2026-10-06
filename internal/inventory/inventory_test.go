package inventory

import (
	"context"
	"encoding/json"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	infos       []supervisor.SessionInfo
	snapshots   map[string]control.Snapshot
	active, max atomic.Int32
	block       bool
}

func (s *fakeSource) ListSessions(context.Context) ([]supervisor.SessionInfo, error) {
	return s.infos, nil
}
func (s *fakeSource) Connect(ctx context.Context, i supervisor.SessionInfo) (SnapshotClient, error) {
	n := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		old := s.max.Load()
		if n <= old || s.max.CompareAndSwap(old, n) {
			break
		}
	}
	if s.block && i.SessionID != "ok" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return fakeClient{s.snapshots[i.SessionID]}, nil
}

type fakeClient struct{ s control.Snapshot }

func (c fakeClient) Snapshot(context.Context) (control.Snapshot, error) { return c.s, nil }
func (c fakeClient) Close() error                                       { return nil }
func ptr[T any](v T) *T                                                 { return &v }
func TestSharedResourceDedupAndReferences(t *testing.T) {
	s := &fakeSource{snapshots: map[string]control.Snapshot{}}
	for _, id := range []string{"a", "b"} {
		s.infos = append(s.infos, supervisor.SessionInfo{SessionID: id, WorkspaceID: id, SocketPath: "secret-socket", CacheDir: "secret-cache"})
		s.snapshots[id] = control.Snapshot{Identity: control.Identity{SessionID: id, WorkspaceID: id}, Containers: []control.Container{{ID: "full-id", EndpointIdentity: "daemon", ResourceRef: "container:daemon:full-id", Metric: control.Metric{Known: true, RSSBytes: ptr(uint64(5))}}, {ID: "unknown", ResourceRef: "container:unknown"}}, Processes: []control.Process{{ID: "process", PID: 7, CreatedUnixMillis: ptr(int64(1)), Metric: control.Metric{Known: true, RSSBytes: ptr(uint64(10))}}, {ID: "reused", PID: 7, CreatedUnixMillis: ptr(int64(2))}, {ID: "unknown", PID: 8}}}
	}
	got, err := New(s, Options{}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Resources) != 7 {
		t.Fatalf("want shared container/process, distinct reuse and four unknowns: %+v", got.Resources)
	}
	for _, r := range got.Resources {
		if r.IdentityKnown && (r.Kind == "container" || r.PID == 7 && r.CreatedUnixMillis != nil && *r.CreatedUnixMillis == 1) {
			if len(r.References) != 2 {
				t.Fatal(r)
			}
		}
	}
	if got.Totals.Processes.MemoryBytes == nil || *got.Totals.Processes.MemoryBytes != 10 || got.Totals.Containers.MemoryBytes == nil || *got.Totals.Containers.MemoryBytes != 5 {
		t.Fatal(got.Totals)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "secret-socket") || strings.Contains(string(raw), "secret-cache") {
		t.Fatal(string(raw))
	}
}
func TestPartialInventoryDoesNotZeroUnknownMetrics(t *testing.T) {
	s := &fakeSource{infos: []supervisor.SessionInfo{{SessionID: "a"}}, snapshots: map[string]control.Snapshot{"a": {Identity: control.Identity{SessionID: "a"}, Processes: []control.Process{{ID: "unknown", PID: 1}}}}}
	got, _ := New(s, Options{}).Collect(context.Background())
	if !got.Partial || !got.Totals.Processes.Partial || got.Totals.Processes.MemoryBytes != nil || got.Totals.Processes.CPUPercent != nil {
		t.Fatal(got)
	}
}
func TestUnreachableSessionDoesNotBlockOthers(t *testing.T) {
	s := &fakeSource{block: true, snapshots: map[string]control.Snapshot{"ok": {Identity: control.Identity{SessionID: "ok"}}}}
	s.infos = append(s.infos, supervisor.SessionInfo{SessionID: "ok"})
	for n := 0; n < 16; n++ {
		s.infos = append(s.infos, supervisor.SessionInfo{SessionID: "blocked"})
	}
	start := time.Now()
	got, err := New(s, Options{SessionTimeout: 40 * time.Millisecond}).Collect(context.Background())
	if err != nil || !got.Partial || len(got.Sessions) != 17 || s.max.Load() > 8 || time.Since(start) > 500*time.Millisecond {
		t.Fatal(got, err, s.max.Load())
	}
	if !got.Sessions[0].Available {
		t.Fatal("healthy session unavailable")
	}
}

func TestSharedResourceReferencesIncludeDeclaredActiveConsumers(t *testing.T) {
	source := &fakeSource{snapshots: map[string]control.Snapshot{}}
	for _, id := range []string{"a", "b"} {
		source.infos = append(source.infos, supervisor.SessionInfo{SessionID: id, WorkspaceID: id})
		source.snapshots[id] = control.Snapshot{Identity: control.Identity{SessionID: id, WorkspaceID: id}, Nodes: []control.Node{{ID: "db", Kind: "resource", State: "available", ResourceRefs: []string{"container:daemon:shared"}}, {ID: "api", State: "running", DependsOn: []string{"db"}}, {ID: "web", State: "running", DependsOn: []string{"api"}}, {ID: "unrelated", State: "running"}}, Containers: []control.Container{{ID: "shared", EndpointIdentity: "daemon", ResourceRef: "container:daemon:shared"}}}
	}
	got, err := New(source, Options{}).Collect(context.Background())
	if err != nil || len(got.Resources) != 1 {
		t.Fatal(got, err)
	}
	for _, ref := range got.Resources[0].References {
		if len(ref.Nodes) != 3 {
			t.Fatal("transitive consumers missing or invented", ref)
		}
		for _, node := range ref.Nodes {
			if node.ID == "api" && (node.State != "running" || node.Role != "consumer") {
				t.Fatal(node)
			}
		}
	}
}

func TestRetainedStaleSnapshotProjectionIsPartial(t *testing.T) {
	at := time.Now().UTC()
	memory := uint64(1024)
	birth := int64(12)
	out := Project(Inventory{Sessions: []Session{{Identity: control.Identity{SessionID: "retained"}, Stale: true, Snapshot: &control.Snapshot{ObservedAt: at, Processes: []control.Process{{ID: "p", PID: 1, CreatedUnixMillis: &birth, Metric: control.Metric{Known: true, RSSBytes: &memory, SampledAt: &at}}}}}}})
	if len(out.Resources) != 1 || !out.Resources[0].Metric.Partial || !out.Totals.Processes.Partial || !out.Partial {
		t.Fatalf("retained resource provenance hidden %+v", out)
	}
	if !out.Resources[0].Metric.SampledAt.Equal(at) || !out.Sessions[0].Snapshot.ObservedAt.Equal(at) {
		t.Fatal("cached projection refreshed timestamps")
	}
}

func TestProjectKeepsEmptyCollectionFailure(t *testing.T) {
	out := Project(Inventory{Sessions: []Session{}, CollectionError: &control.APIError{Code: "unavailable", Message: "Inventory discovery unavailable"}})
	if !out.Partial || !out.Totals.Processes.Partial || !out.Totals.Containers.Partial || out.CollectionError == nil {
		t.Fatalf("empty collection failure erased %+v", out)
	}
	if !Project(Inventory{Partial: true}).Partial {
		t.Fatal("explicit partial collection erased")
	}
	if Project(Inventory{Sessions: []Session{}}).Partial {
		t.Fatal("healthy empty inventory marked partial")
	}
}
