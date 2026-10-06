package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const eventHistory = 512
const subscriberBuffer = 64
const streamWriteTimeout = 5 * time.Second
const heartbeatInterval = 15 * time.Second

// Event IDs bind an ordered invalidation to one gateway generation.
type Event struct {
	ID   string          `json:"id"`
	Name string          `json:"event"`
	Data json.RawMessage `json:"data"`
}
type eventSubscriber struct {
	wake   chan struct{}
	cancel context.CancelFunc
}
type EventHub struct {
	mu              sync.Mutex
	generation      string
	sequence        uint64
	history         []Event
	subscribers     map[*eventSubscriber]struct{}
	closed          bool
	deliveryTimeout time.Duration
}

func NewEventHub() *EventHub {
	generation, err := randomToken()
	if err != nil {
		panic(err)
	}
	return &EventHub{generation: generation, subscribers: map[*eventSubscriber]struct{}{}, deliveryTimeout: streamWriteTimeout}
}
func (h *EventHub) cursorLocked() string {
	return h.generation + ":" + strconv.FormatUint(h.sequence, 10)
}
func (h *EventHub) Cursor() string { h.mu.Lock(); defer h.mu.Unlock(); return h.cursorLocked() }
func (h *EventHub) Publish(name string, data any) Event {
	payload, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return Event{}
	}
	h.sequence++
	e := Event{h.cursorLocked(), name, payload}
	h.history = append(h.history, e)
	if len(h.history) > eventHistory {
		h.history = append([]Event{}, h.history[len(h.history)-eventHistory:]...)
	}
	for s := range h.subscribers {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return e
}
func (h *EventHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for s := range h.subscribers {
		s.cancel()
	}
}
func (h *EventHub) afterLocked(after string) (uint64, string) {
	if after == "" {
		return h.sequence, "bootstrap"
	}
	parts := strings.Split(after, ":")
	if len(parts) != 2 || parts[0] != h.generation {
		return h.sequence, "generation_changed"
	}
	n, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || n > h.sequence {
		return h.sequence, "invalid_cursor"
	}
	if h.sequence-n > uint64(len(h.history)) {
		return h.sequence, "history_gap"
	}
	return n, ""
}
func (h *EventHub) resetLocked(reason string) Event {
	data, _ := json.Marshal(map[string]string{"reason": reason})
	return Event{h.cursorLocked(), "reset", data}
}
func (h *EventHub) Subscribe(ctx context.Context, after string) (<-chan Event, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, apiError("unavailable", "gateway stream closed")
	}
	if len(h.subscribers) >= 128 {
		h.mu.Unlock()
		return nil, apiError("queue_full", "event subscriber capacity reached")
	}
	next, reason := h.afterLocked(after)
	var initial *Event
	if reason != "" {
		e := h.resetLocked(reason)
		initial = &e
	}
	child, cancel := context.WithCancel(ctx)
	s := &eventSubscriber{make(chan struct{}, 1), cancel}
	h.subscribers[s] = struct{}{}
	timeout := h.deliveryTimeout
	h.mu.Unlock()
	out := make(chan Event, subscriberBuffer)
	go func() {
		defer close(out)
		defer cancel()
		defer func() { h.mu.Lock(); delete(h.subscribers, s); h.mu.Unlock() }()
		send := func(e Event) bool {
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			select {
			case out <- e:
				return true
			case <-child.Done():
				return false
			case <-timer.C:
				return false
			}
		}
		if initial != nil && !send(*initial) {
			return
		}

		for {
			// Read one retained event at a time. A subscriber owns only its
			// 64-slot delivery queue plus the current event, never a copied
			// 512-event replay backlog.
			h.mu.Lock()
			var event Event
			pending := false
			if h.sequence-next > uint64(len(h.history)) {
				event = h.resetLocked("history_gap")
				next = h.sequence
				pending = true
			} else if next < h.sequence {
				event = h.history[len(h.history)-int(h.sequence-next)]
				next++
				pending = true
			}
			h.mu.Unlock()
			if pending {
				if !send(event) {
					return
				}
				continue
			}
			select {
			case <-child.Done():
				return
			case <-s.wake:
			}
		}
	}()
	return out, nil
}
func streamEvent(w http.ResponseWriter, e Event) error {
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil && err != http.ErrNotSupported {
		return err
	}
	if e.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", e.ID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Name, e.Data); err != nil {
		return err
	}
	return controller.Flush()
}
func (g *Gateway) serveEvents(w http.ResponseWriter, r *http.Request) {
	filter, err := parseEventQuery(r)
	if err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	after := r.Header.Get("Last-Event-ID")
	if after == "" {
		after = filter.after
	}
	ch, err := g.hub.Subscribe(ctx, after)
	if err != nil {
		fail(w, err)
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		fail(w, apiError("unavailable", "streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	defer controller.SetWriteDeadline(time.Time{})
	if err = controller.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil && err != http.ErrNotSupported {
		return
	}
	if _, err = fmt.Fprint(w, "retry: 1000\n\n"); err != nil {
		return
	}
	if err = controller.Flush(); err != nil {
		return
	}
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	var logs <-chan Event
	if filter.session != "" {
		logs = g.streamLogs(ctx, filter)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if streamEvent(w, e) != nil {
				return
			}
		case e, ok := <-logs:
			if !ok {
				logs = nil
				continue
			}
			if streamEvent(w, e) != nil {
				return
			}
		case <-heartbeat.C:
			if controller.SetWriteDeadline(time.Now().Add(streamWriteTimeout)) != nil {
				return
			}
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
		}
	}
}
