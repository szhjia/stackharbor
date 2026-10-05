package graph

import (
	"github.com/szhjia/stackharbor/internal/model"
	"testing"
)

func TestExplicitConditionAndSharedTaskLayers(t *testing.T) {
	task := model.Service{ID: "b/task/migrate", Kind: "task", ProjectID: "b", Task: &model.TaskSpec{Policy: "always"}}
	api := model.Service{ID: "b/service/api", Kind: "service", ProjectID: "b", Ready: &model.ReadyProbe{TCP: "127.0.0.1:9999"}, DependsOn: []model.ServiceID{task.ID}, Requires: []model.Requirement{{Node: task.ID, Condition: "succeeded"}}}
	a := model.Service{ID: "a/service/web", ProjectID: "a", DependsOn: []model.ServiceID{api.ID}, Requires: []model.Requirement{{Node: api.ID, Condition: "ready"}}}
	w := model.Workspace{Projects: []model.Project{{ID: "a", Services: []model.Service{a}}, {ID: "b", Services: []model.Service{task, api}}}}
	g, ds := Build(w.Services())
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	p, e := g.Plan(w, "start", []model.ServiceID{a.ID, api.ID})
	if e != nil || len(p.OrderedLayers) != 3 || len(p.Actions) != 3 {
		t.Fatal(p, e)
	}
	if p.OrderedLayers[0][0] != task.ID || p.Edges[0].From != task.ID {
		t.Fatal("dependency graph lost", p)
	}
	nav := g.Navigation(w)
	if nav[0].ID != "b" || nav[len(nav)-1].ID != "a" {
		t.Fatal("alphabetical navigation", nav)
	}
	api.Requires[0].Condition = "ready"
	if _, ds = Build([]model.Service{task, api}); len(ds) == 0 {
		t.Fatal("task readiness condition accepted")
	}
}
