package web

import (
	"context"
	"encoding/json"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"net/http"
	"net/url"
	"time"
)

func encodeLogCursor(s, t string, n uint64) string         { return control.EncodeLogCursor(s, t, n) }
func decodeLogCursor(c, s, t string) (uint64, bool, error) { return control.DecodeLogCursor(c, s, t) }

type eventFilter struct{ after, session, target, cursor string }

func parseEventQuery(r *http.Request) (eventFilter, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return eventFilter{}, apiError("invalid_request", "invalid event query")
	}
	for k, v := range q {
		if len(v) != 1 || (k != "after" && k != "session_id" && k != "target" && k != "log_after") {
			return eventFilter{}, apiError("invalid_request", "invalid event query")
		}
	}
	f := eventFilter{q.Get("after"), q.Get("session_id"), q.Get("target"), q.Get("log_after")}
	if len(f.after) > 128 || len(f.session) > 256 || len(f.target) > 256 || len(f.cursor) > 4096 {
		return f, apiError("invalid_request", "event query too long")
	}
	if f.session == "" && (f.target != "" || f.cursor != "") {
		return f, apiError("invalid_request", "log filter requires session_id")
	}
	if f.session != "" {
		if _, _, err := decodeLogCursor(f.cursor, f.session, f.target); err != nil {
			return f, err
		}
	}
	return f, nil
}
func browserLogsQuery(u *url.URL, sid string) (string, string, int, error) {
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", "", 0, apiError("invalid_request", "invalid logs query")
	}
	cursor, has := q["cursor"]
	if has && len(cursor) != 1 {
		return "", "", 0, apiError("invalid_request", "invalid logs query")
	}
	if has && q.Has("after") {
		return "", "", 0, apiError("invalid_request", "use cursor or after")
	}
	q.Del("cursor")
	copy := *u
	copy.RawQuery = q.Encode()
	target, after, limit, err := logsQuery(&copy)
	if err != nil {
		return "", "", 0, err
	}
	if has {
		if _, _, err := decodeLogCursor(cursor[0], sid, target); err != nil {
			return "", "", 0, err
		}
		return target, cursor[0], limit, nil
	}
	return target, encodeLogCursor(sid, target, after), limit, nil
}

// Each log stream polls only its explicit session/filter. Canceling the stream
// cancels pending RPCs and its bounded queue, never the session's log store.
func (g *Gateway) streamLogs(ctx context.Context, f eventFilter) <-chan Event {
	out := make(chan Event, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(statusInterval)
		defer ticker.Stop()
		cursor := f.cursor
		send := func(name string, data any) bool {
			bytes, _ := json.Marshal(data)
			select {
			case out <- Event{Name: name, Data: bytes}:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for {
			request, cancel := context.WithTimeout(ctx, 2*time.Second)
			info, err := g.find(request, f.session)
			var page control.BoundLogPage
			if err == nil {
				var client *sessionapi.Client
				client, err = sessionapi.Connect(request, info)
				if err == nil {
					page, err = client.LogsAfter(request, f.target, cursor, 500)
					client.Close()
				}
			}
			cancel()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				if !send("session_unavailable", map[string]any{"session_id": f.session, "error": apiError("unavailable", "Log session unavailable; rediscover sessions")}) {
					return
				}
			} else {
				if page.Reset {
					if !send("reset", map[string]string{"reason": "log_cursor_changed", "session_id": f.session, "target": f.target}) {
						return
					}
				}
				if page.Gap {
					if !send("gap", page) {
						return
					}
				}
				if len(page.Entries) > 0 || cursor == "" || page.Reset || page.Gap {
					if !send("log", page) {
						return
					}
				}
				prior := cursor
				cursor = page.Cursor
				// Drain a retained backlog in bounded pages, without waiting one second per
				// page; never loop if a faulty endpoint did not advance its cursor.
				if len(page.Entries) == 500 && prior != cursor {
					continue
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return out
}
