package tui

import (
	"fmt"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"strings"
	"testing"
)

func TestReviewDashboardScopedKeysDoNothing(t *testing.T) {
	for _, k := range []string{"s", "x", "r"} {
		f := fixture()
		m := NewModel(f)
		next, c := key(m, k)
		if c != nil {
			c()
		}
		if f.action != "" {
			t.Errorf("Dashboard %s dispatched %s for %v, confirmation=%v", k, f.action, f.ids, next.confirm)
		}
	}
}

func TestReviewMultiServiceLogsVisible(t *testing.T) {
	f := fixture()
	f.snapshot.Projects = f.snapshot.Projects[:1]
	f.snapshot.Projects[0].Services = nil
	f.snapshot.Services = nil
	for i := 0; i < 4; i++ {
		s := model.Service{ID: model.ServiceID(fmt.Sprintf("app/s%d", i)), Key: fmt.Sprint(i), ProjectID: "app", Command: []string{"server"}}
		f.snapshot.Projects[0].Services = append(f.snapshot.Projects[0].Services, s)
		f.snapshot.Services = append(f.snapshot.Services, model.ServiceSnapshot{Spec: s, State: "running", Ports: []model.PortObservation{{Port: 8100, Status: "owned"}}})
		f.store.Append(logs.Entry{ServiceID: s.ID, Text: "VISIBLE_LOG_MARKER"})
	}
	m := NewModel(f)
	m.selected = 1
	m.service = 0
	if !strings.Contains(m.View().Content, "8100") || !strings.Contains(m.View().Content, "VISIBLE_LOG_MARKER") {
		t.Fatalf("selected service log is clipped on 80x24 terminal:\n%s", m.View().Content)
	}
}
