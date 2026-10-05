package tui

import (
	"github.com/szhjia/stackharbor/internal/model"
	"testing"
)

func TestProjectStatusSeparatesCompletedPrerequisitesFromServices(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		task, api, worker, want string
	}{
		{"all services active", "succeeded", "running", "started", "running"},
		{"services stopped after completed migration", "succeeded", "stopped", "stopped", "stopped"},
		{"one service active", "succeeded", "started", "stopped", "partial"},
		{"migration failed", "failed", "running", "started", "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{snapshot: model.Snapshot{Services: []model.ServiceSnapshot{
				{Spec: model.Service{ID: "app/task/migrate", ProjectID: "app", Kind: "task"}, State: tc.task},
				{Spec: model.Service{ID: "app/service/api", ProjectID: "app", Kind: "service"}, State: tc.api},
				{Spec: model.Service{ID: "app/service/worker", ProjectID: "app", Kind: "service"}, State: tc.worker},
			}}}
			if got := m.aggregateState("app"); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
