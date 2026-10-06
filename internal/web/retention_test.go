package web

import (
	"fmt"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"testing"
	"time"
)

func TestCloseMetadataRetentionBoundAndInflight(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{})
	now := time.Now()
	for i := 0; i < 1002; i++ {
		g.closes[fmt.Sprint(i)] = retainedClose{Info: supervisor.SessionInfo{}, At: now.Add(time.Duration(i) * time.Second), Terminal: true}
	}
	g.closes["pending"] = retainedClose{At: now.Add(-25 * time.Hour)}
	g.pruneCloses(now)
	if len(g.closes) != 1001 {
		t.Fatal("terminal count unbounded or pending dropped", len(g.closes))
	}
	if _, ok := g.closes["pending"]; !ok {
		t.Fatal("inflight evidence dropped")
	}
	g.closes["expired"] = retainedClose{At: now.Add(-24 * time.Hour), Terminal: true}
	g.pruneCloses(now)
	if _, ok := g.closes["expired"]; ok {
		t.Fatal("expired metadata retained")
	}
}
