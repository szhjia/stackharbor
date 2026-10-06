package tui

import (
	"context"
	"github.com/szhjia/stackharbor/internal/model"
	"testing"
)

type plannedFake struct {
	*fakeController
	plans, executions int
	token             string
}

func (f *plannedFake) PlanAction(_ context.Context, action string, ids []model.ServiceID, docker []string) (PlannedAction, error) {
	f.plans++
	return PlannedAction{ID: "original-preview", Affected: []model.ServiceID{"app/web", "other/api"}}, nil
}
func (f *plannedFake) ExecuteAction(_ context.Context, p PlannedAction) error {
	f.executions++
	f.token = p.ID
	return nil
}
func TestConfirmationRetainsPreviewPlan(t *testing.T) {
	f := &plannedFake{fakeController: fixture()}
	f.affected = []model.ServiceID{"other/api"}
	m := NewModel(f)
	m, _ = key(m, "j")
	m, cmd := key(m, "x")
	if cmd == nil {
		t.Fatal("confirmation was not planned")
	}
	v, _ := m.Update(cmd())
	m = v.(Model)
	if !m.confirm {
		t.Fatal("confirmation absent")
	}
	m, cmd = key(m, "y")
	if cmd == nil {
		t.Fatal("confirmation did not execute")
	}
	cmd()
	if f.plans != 1 || f.executions != 1 || f.token != "original-preview" {
		t.Fatalf("fresh authorization after confirm: %+v", f)
	}
}

func TestDockerAndAllConfirmationsRetainPreview(t *testing.T) {
	for _, action := range []string{"docker-stop", "all-stop", "all-restart"} {
		t.Run(action, func(t *testing.T) {
			f := &plannedFake{fakeController: fixture()}
			m := NewModel(f)
			m.busy = true
			v, _ := m.Update(m.preview(action, []model.ServiceID{"app/web"}, []string{"db"}, true, "Confirm")())
			m = v.(Model)
			if !m.confirm {
				t.Fatal("missing confirmation")
			}
			m, cmd := key(m, "y")
			cmd()
			if f.plans != 1 || f.executions != 1 || f.token != "original-preview" {
				t.Fatal("confirmation replanned", f)
			}
		})
	}
}
