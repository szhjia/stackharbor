package control

import (
	"encoding/base64"
	"encoding/json"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"io"
	"strings"
	"time"
)

type LogEntry struct {
	Sequence  uint64    `json:"sequence"`
	Time      time.Time `json:"time"`
	ProjectID string    `json:"project_id"`
	ServiceID string    `json:"service_id"`
	Stream    string    `json:"stream"`
	Text      string    `json:"text"`
}

type LogPage struct {
	Entries    []LogEntry `json:"entries"`
	NextCursor uint64     `json:"next_cursor"`
	Gap        bool       `json:"gap"`
	Dropped    uint64     `json:"dropped"`
	Reset      bool       `json:"reset"`
}

// ReadLogs returns up to 500 sanitized entries after a global sequence cursor.
// An empty target reads all targets. A non-positive limit uses the 500 default.
func ReadLogs(store *logs.Store, target model.ServiceID, after uint64, limit int) LogPage {
	reset := after > store.Version()
	if reset {
		after = 0
	}
	page := store.ReadPage(target, after, limit)
	out := LogPage{Entries: []LogEntry{}, NextCursor: page.NextCursor, Gap: page.Gap, Dropped: page.Dropped, Reset: reset}
	for _, entry := range page.Entries {
		out.Entries = append(out.Entries, LogEntry{Sequence: entry.Sequence, Time: entry.Time.UTC(), ProjectID: entry.ProjectID, ServiceID: string(entry.ServiceID), Stream: entry.Stream, Text: entry.Text})
	}
	return out
}

// LogCursor binds a sequence to the exact session generation and target filter.
// It is an opaque transport cursor, not an authorization credential.
type LogCursor struct {
	SessionID string `json:"session_id"`
	Target    string `json:"target"`
	Sequence  uint64 `json:"sequence"`
}
type BoundLogPage struct {
	LogPage
	SessionID string `json:"session_id"`
	Target    string `json:"target"`
	Cursor    string `json:"cursor"`
}

func EncodeLogCursor(session, target string, sequence uint64) string {
	data, _ := json.Marshal(LogCursor{session, target, sequence})
	return base64.RawURLEncoding.EncodeToString(data)
}
func DecodeLogCursor(cursor, session, target string) (uint64, bool, error) {
	if cursor == "" {
		return 0, false, nil
	}
	if len(cursor) > 4096 {
		return 0, false, apiError("invalid_request", "log cursor too long")
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, false, apiError("invalid_request", "invalid log cursor")
	}
	var c LogCursor
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil || c.SessionID == "" || d.Decode(&struct{}{}) != io.EOF {
		return 0, false, apiError("invalid_request", "invalid log cursor")
	}
	if c.SessionID != session || c.Target != target {
		return 0, true, nil
	}
	return c.Sequence, false, nil
}
