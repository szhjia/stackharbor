package web

import (
	"context"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"sort"
	"time"
)

type retainedClose struct {
	Info        supervisor.SessionInfo
	At          time.Time
	OperationID string
	Terminal    bool
}

// Called under g.mu. Terminal private lookup metadata follows the journal's
// 24h/1000-record bounds. Pending closes are exempt from count eviction.
func (g *Gateway) pruneCloses(now time.Time) {
	type entry struct {
		key string
		at  time.Time
	}
	var terminals []entry
	for key, record := range g.closes {
		if !record.Terminal && record.OperationID != "" {
			if op, err := sessionhost.ReadOperationCompletion(record.Info.CacheDir, record.Info.Root, record.OperationID); err == nil {
				record.Terminal = true
				record.At = op.UpdatedAt
				g.closes[key] = record
			} else if now.Sub(record.At) >= 24*time.Hour {
				// An unavailable journal is no longer recoverable after its retention TTL.
				// Keep a still registered exact session: it may have a live operation.
				infos, _ := (registrySource{g.options.Namespace}).ListSessions(context.Background())
				alive := false
				for _, info := range infos {
					if info.SessionID == record.Info.SessionID {
						alive = true
						break
					}
				}
				if !alive {
					delete(g.closes, key)
					continue
				}
			}
		}
		if record.Terminal {
			if now.Sub(record.At) >= 24*time.Hour {
				delete(g.closes, key)
				continue
			}
			terminals = append(terminals, entry{key, record.At})
		}
	}
	sort.Slice(terminals, func(i, j int) bool { return terminals[i].at.Before(terminals[j].at) })
	for _, entry := range terminals[:max(0, len(terminals)-1000)] {
		delete(g.closes, entry.key)
	}
}
