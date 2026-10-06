package control

import (
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"strings"
	"testing"
	"time"
)

func TestReadLogsCursorAndGap(t *testing.T) {
	store := logs.NewStore()
	for i := 0; i < 2002; i++ {
		store.Append(logs.Entry{ServiceID: "app/web", Text: "line"})
	}
	page := ReadLogs(store, "app/web", 1, 501)
	if !page.Gap || page.Dropped != 2 || len(page.Entries) != 500 || page.Entries[0].Sequence != 3 || page.NextCursor != 502 {
		t.Fatalf("bad truncated page: %+v", page)
	}
	page = ReadLogs(store, "app/web", 2, 1)
	if page.Gap {
		t.Fatal("already consumed eviction reported as missing")
	}
	store = logs.NewStore()
	store.Append(logs.Entry{ServiceID: "app/web", Text: "first"})
	store.Append(logs.Entry{ServiceID: "app/other", Text: "other"})
	store.Append(logs.Entry{ServiceID: "app/web", Text: "second"})
	store.Append(logs.Entry{ServiceID: "app/other", Text: "other"})
	page = ReadLogs(store, "app/web", 1, 500)
	if page.Gap || page.Dropped != 0 || len(page.Entries) != 1 || page.Entries[0].Sequence != 3 || page.NextCursor != 4 {
		t.Fatalf("filter sequence skip treated as loss: %+v", page)
	}
	empty := ReadLogs(store, "app/missing", 0, 500)
	if empty.Gap || len(empty.Entries) != 0 || empty.NextCursor != 4 {
		t.Fatalf("empty target page: %+v", empty)
	}
	for i := 0; i < 2001; i++ {
		store.Append(logs.Entry{ServiceID: "app/other", Text: "other"})
	}
	if got := ReadLogs(store, "app/web", 0, 500); got.Gap || got.Dropped != 0 {
		t.Fatal("other target eviction caused gap")
	}
	if got := ReadLogs(store, "", 0, 500); !got.Gap || got.Dropped == 0 {
		t.Fatal("global read missed eviction")
	}
}

func TestReadLogsProjectsSafeWireFields(t *testing.T) {
	store := logs.NewStore()
	store.Append(logs.Entry{ServiceID: "app/web", Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("offset", 3600)), Text: "<script>text</script>\x1b[31m"})
	page := ReadLogs(store, "app/web", 0, 0)
	if len(page.Entries) != 1 || page.Entries[0].Time.Location() != time.UTC || page.Entries[0].Text != "<script>text</script>" {
		t.Fatalf("bad log projection: %+v", page)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"entries"`, `"next_cursor"`, `"service_id"`, `"sequence"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("missing wire key %s: %s", key, raw)
		}
	}
}

func TestReadLogsByteAndGlobalEvictions(t *testing.T) {
	t.Run("service byte budget", func(t *testing.T) {
		store := logs.NewStore()
		for i := 0; i < 140; i++ {
			store.Append(logs.Entry{ServiceID: "app/web", Text: strings.Repeat("x", 16384)})
		}
		page := ReadLogs(store, "app/web", 0, 500)
		if !page.Gap || page.Dropped == 0 || len(page.Entries) >= 140 {
			t.Fatal("byte eviction not recorded")
		}
		if got := ReadLogs(store, "app/web", page.Entries[0].Sequence-1, 500); got.Gap {
			t.Fatal("consumed byte evictions still produce gap")
		}
	})
	t.Run("global budget", func(t *testing.T) {
		store := logs.NewStore()
		store.Append(logs.Entry{ServiceID: "app/old", Text: strings.Repeat("x", 16384)})
		for i := 0; i < 1100; i++ {
			store.Append(logs.Entry{ServiceID: model.ServiceID(fmt.Sprintf("app/%d", i%20)), Text: strings.Repeat("x", 16384)})
		}
		old := ReadLogs(store, "app/old", 0, 500)
		if !old.Gap || old.Dropped != 1 || len(old.Entries) != 0 || old.NextCursor != 1101 {
			t.Fatalf("global eviction missing: %+v", old)
		}
		if got := ReadLogs(store, "app/old", 1, 500); got.Gap {
			t.Fatal("already consumed global eviction reported as loss")
		}
	})
}

func TestLogFutureCursorResets(t *testing.T) {
	store := logs.NewStore()
	store.Append(logs.Entry{ServiceID: "app/web", Text: "first"})
	page := ReadLogs(store, "app/web", 99, 500)
	if !page.Reset || page.NextCursor != 1 || len(page.Entries) != 1 {
		t.Fatalf("future cursor pinned stream %+v", page)
	}
}
