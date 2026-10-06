package web

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/inventory"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"strings"
	"testing"
	"time"
)

type timingSource struct {
	lists, snapshots int
	infos            []supervisor.SessionInfo
	fail             string
	now              time.Time
}

func (s *timingSource) ListSessions(context.Context) ([]supervisor.SessionInfo, error) {
	s.lists++
	return s.infos, nil
}
func (s *timingSource) Connect(_ context.Context, i supervisor.SessionInfo) (inventory.SnapshotClient, error) {
	return timingClient{s, i.SessionID}, nil
}

type timingClient struct {
	s  *timingSource
	id string
}

func (c timingClient) Close() error { return nil }
func (c timingClient) Snapshot(context.Context) (control.Snapshot, error) {
	c.s.snapshots++
	if c.id == c.s.fail {
		return control.Snapshot{}, errors.New("offline")
	}
	return control.Snapshot{Identity: control.Identity{SessionID: c.id}, ObservedAt: c.s.now}, nil
}
func TestDiscoveryAndStaleTiming(t *testing.T) {
	now := time.Now().UTC()
	s := &timingSource{now: now, infos: []supervisor.SessionInfo{{SessionID: "a"}}}
	live := newLiveInventory(s, nil, nil)
	live.now = func() time.Time { return now }
	// Use one worker to keep deterministic fake counters race-free.
	live.collector = inventory.New(live.source, inventory.Options{Concurrency: 1})
	live.refresh(context.Background())
	s.infos = append(s.infos, supervisor.SessionInfo{SessionID: "b"})
	now = now.Add(time.Second)
	s.now = now
	live.refresh(context.Background())
	if s.lists != 1 || s.snapshots != 2 || len(live.snapshot().Sessions) != 1 {
		t.Fatalf("1s discovery/status: %d %d", s.lists, s.snapshots)
	}
	now = now.Add(time.Second)
	s.now = now
	live.refresh(context.Background())
	if s.lists != 2 || len(live.snapshot().Sessions) != 2 {
		t.Fatal("2s discovery missing")
	}
	s.fail = "a"
	now = now.Add(5 * time.Second)
	s.now = now
	live.refresh(context.Background())
	rows := live.snapshot().Sessions
	if rows[0].Stale || rows[0].Available || rows[0].Snapshot == nil || rows[1].Snapshot == nil {
		t.Fatalf("failure retention/stale boundary: %#v", rows)
	}
	now = now.Add(time.Nanosecond)
	s.now = now
	live.refresh(context.Background())
	rows = live.snapshot().Sessions
	if !rows[0].Stale || !rows[1].Available || !live.snapshot().Partial {
		t.Fatal("stale after5s or partial missing")
	}
	if !rows[0].Snapshot.ObservedAt.Equal(now.Add(-5*time.Second - time.Nanosecond)) {
		t.Fatal("retained observation timestamp refreshed")
	}
}
func nextEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("subscription closed")
		}
		return e
	case <-time.After(time.Second):
		t.Fatal("missing event")
	}
	return Event{}
}
func TestSSEReconnectResetAndSlowConsumer(t *testing.T) {
	h := NewEventHub()
	defer h.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := h.Publish("inventory", map[string]int{"revision": 1})
	h.Publish("inventory", map[string]int{"revision": 2})
	ch, err := h.Subscribe(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e := nextEvent(t, ch); e.Name != "inventory" || e.ID == first.ID {
		t.Fatalf("replay %#v", e)
	}
	restarted := NewEventHub()
	defer restarted.Close()
	ch, _ = restarted.Subscribe(ctx, first.ID)
	if e := nextEvent(t, ch); e.Name != "reset" {
		t.Fatal("restart cursor accepted")
	}
	for i := 0; i < 513; i++ {
		h.Publish("inventory", i)
	}
	ch, _ = h.Subscribe(ctx, first.ID)
	if e := nextEvent(t, ch); e.Name != "reset" {
		t.Fatal("expired history accepted")
	}
	h.deliveryTimeout = 10 * time.Millisecond
	slow, _ := h.Subscribe(ctx, h.Cursor())
	if cap(slow) > 64 {
		t.Fatal("unbounded subscriber")
	}
	for i := 0; i < 100; i++ {
		h.Publish("inventory", i)
	}
	time.Sleep(40 * time.Millisecond)
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-slow:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("slow client not disconnected")
		}
	}
}
func TestLogCursorAcrossSessionRestart(t *testing.T) {
	cursor := encodeLogCursor("old", "app/api", 42)
	if n, reset, err := decodeLogCursor(cursor, "old", "app/api"); err != nil || reset || n != 42 {
		t.Fatalf("cursor %d %v %v", n, reset, err)
	}
	if n, reset, err := decodeLogCursor(cursor, "new", "app/api"); err != nil || !reset || n != 0 {
		t.Fatal("old cursor used for new session")
	}
	if _, reset, _ := decodeLogCursor(cursor, "old", "app/other"); !reset {
		t.Fatal("cross-target cursor accepted")
	}
	if _, _, err := decodeLogCursor("garbage", "old", "app/api"); err == nil {
		t.Fatal("malformed cursor accepted")
	}
}

func TestEventReplayLargerThanSubscriberBuffer(t *testing.T) {
	h := NewEventHub()
	defer h.Close()
	first := h.Publish("inventory", 0)
	for i := 0; i < 130; i++ {
		h.Publish("inventory", i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := h.Subscribe(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 130; i++ {
		if e := nextEvent(t, ch); e.Name != "inventory" {
			t.Fatalf("bounded replay reset early %#v", e)
		}
	}
}

func TestInitialInventoryCollectionFailureUnavailableUntilRecovery(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{})
	defer g.hub.Close()
	cookie, _ := authenticated(t, g)
	failed := true
	live := newLiveInventory(registrySource{}, func(context.Context) (inventory.Inventory, error) {
		if failed {
			return inventory.Inventory{}, errors.New("secret cache path and credential")
		}
		return inventory.Inventory{Sessions: []inventory.Session{}, Resources: []inventory.Resource{}}, nil
	}, nil)
	g.live = live
	live.refresh(context.Background())
	w := request(g, "GET", "/api/v1/inventory", "", g.host, "", cookie, "")
	if w.Code != 503 {
		t.Fatalf("initial discovery failure became healthy empty %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("raw discovery error leaked")
	}
	failed = false
	live.refresh(context.Background())
	w = request(g, "GET", "/api/v1/inventory", "", g.host, "", cookie, "")
	if w.Code != 200 || data[inventory.Inventory](t, w.Body.Bytes()).Partial {
		t.Fatalf("initial failure recovery %d %s", w.Code, w.Body.String())
	}
}
func TestEmptyInventoryCollectionFailureAndRecovery(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{})
	defer g.hub.Close()
	cookie, _ := authenticated(t, g)
	failed := false
	live := newLiveInventory(registrySource{}, func(context.Context) (inventory.Inventory, error) {
		if failed {
			return inventory.Inventory{}, errors.New("secret cache path and credential")
		}
		return inventory.Inventory{Sessions: []inventory.Session{}, Resources: []inventory.Resource{}}, nil
	}, nil)
	g.live = live
	live.refresh(context.Background())
	failed = true
	for i := 0; i < 2; i++ {
		live.refresh(context.Background())
		w := request(g, "GET", "/api/v1/inventory", "", g.host, "", cookie, "")
		inv := data[inventory.Inventory](t, w.Body.Bytes())
		if w.Code != 200 || !inv.Partial || !inv.Totals.Processes.Partial || !inv.Totals.Containers.Partial {
			t.Fatalf("empty outage became healthy %d %s", w.Code, w.Body.String())
		}
		var body struct {
			Data struct {
				Error *control.APIError `json:"collection_error"`
			}
		}
		json.Unmarshal(w.Body.Bytes(), &body)
		if body.Data.Error == nil || body.Data.Error.Code != "unavailable" || body.Data.Error.Message == "" || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("outage provenance/error unsafe: %s", w.Body.String())
		}
	}
	failed = false
	live.refresh(context.Background())
	w := request(g, "GET", "/api/v1/inventory", "", g.host, "", cookie, "")
	inv := data[inventory.Inventory](t, w.Body.Bytes())
	if w.Code != 200 || inv.Partial || inv.Totals.Processes.Partial || inv.Totals.Containers.Partial || strings.Contains(w.Body.String(), "collection_error") {
		t.Fatalf("outage not cleared on recovery %d %s", w.Code, w.Body.String())
	}
}
