package observe

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
)

type sharedPortReader struct {
	mu sync.Mutex
	id model.ProcessIdentity
}

func (r *sharedPortReader) List(context.Context) ([]process.Info, error) { return nil, nil }
func (r *sharedPortReader) Read(context.Context, int32) (process.Info, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return process.Info{Identity: r.id}, nil
}
func TestSharedPortsCacheRawListenersAndProjectEachWorkspace(t *testing.T) {
	id := model.ProcessIdentity{PID: 123, CreatedMillis: 10}
	reader := &sharedPortReader{id: id}
	calls := 0
	probe := &sharedPortProbe{ttl: func() time.Duration { return time.Hour }, reader: process.NewSharedReader(reader, func() time.Duration { return time.Hour }), raw: func(context.Context) listenerSnapshot {
		calls++
		return listenerSnapshot{known: true, listeners: map[int][]model.Listener{8080: {{PID: id.PID, Command: "app"}}, 9090: {{PID: 999, Command: "other"}}}}
	}}
	first := probe.Observe(context.Background(), nil, []model.ProcessIdentity{id})
	if len(first) != 1 || first[0].Port != 8080 || first[0].Status != "owned" {
		t.Fatalf("first projection: %+v", first)
	}
	first[0].Listeners[0].Command = "mutated"
	second := probe.Observe(context.Background(), []model.Port{{Number: 8080}, {Number: 9090}}, nil)
	if len(second) != 2 || second[0].Status != "external" || second[0].Listeners[0].Command != "app" || second[1].Status != "external" {
		t.Fatalf("workspace leaked ownership: %+v", second)
	}
	reader.mu.Lock()
	reader.id.CreatedMillis++
	reader.mu.Unlock()
	third := probe.Observe(context.Background(), []model.Port{{Number: 8080}}, []model.ProcessIdentity{id})
	if len(third) != 1 || third[0].Status != "external" {
		t.Fatalf("stale identity became owned: %+v", third)
	}
	if calls != 1 {
		t.Fatalf("raw scanner invoked %d times", calls)
	}
}

type customSharedProbe struct{ observed, checked int }

func (p *customSharedProbe) Observe(context.Context, []model.Port, []model.ProcessIdentity) []model.PortObservation {
	p.observed++
	return nil
}
func (p *customSharedProbe) CheckStart(context.Context, []model.Port) error { p.checked++; return nil }
func TestSharedPortProbeFreshStartAndCustomProbeFallback(t *testing.T) {
	base := &customSharedProbe{}
	probe := NewSharedPortProbe(base, func() time.Duration { return time.Hour })
	for range 2 {
		probe.Observe(context.Background(), nil, nil)
		if err := probe.CheckStart(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if base.observed != 2 || base.checked != 2 {
		t.Fatalf("unsafe custom caching: %+v", base)
	}
}
