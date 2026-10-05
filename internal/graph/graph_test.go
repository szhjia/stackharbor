package graph

import (
	"github.com/szhjia/stackharbor/internal/model"
	"slices"
	"testing"
)

func svc(id string, deps ...model.ServiceID) model.Service {
	return model.Service{ID: model.ServiceID(id), DependsOn: deps}
}
func TestStableDiamondStartAndReverseStop(t *testing.T) {
	g, ds := Build([]model.Service{svc("app/web", "app/api", "app/worker"), svc("app/worker", "app/db"), svc("app/api", "app/db"), svc("app/db")})
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	want := []model.ServiceID{"app/db", "app/api", "app/worker", "app/web"}
	if !slices.Equal(g.StartOrder([]model.ServiceID{"app/web"}), want) {
		t.Fatal("wrong start order")
	}
	slices.Reverse(want)
	if !slices.Equal(g.StopOrder(want), want) {
		t.Fatal("wrong stop order")
	}
	if !slices.Equal(g.Dependents([]model.ServiceID{"app/db"}), []model.ServiceID{"app/api", "app/web", "app/worker"}) {
		t.Fatal("wrong dependent set")
	}
}
func TestGraphRejectsCycleMissingAndDuplicatePorts(t *testing.T) {
	a := svc("app/a")
	a.Ports = []model.Port{{Name: "http", Number: 8080}}
	b := svc("app/b")
	b.Ports = a.Ports
	for _, ss := range [][]model.Service{{svc("app/a", "app/b"), svc("app/b", "app/a")}, {svc("app/a", "missing/b")}, {a, b}, {a, a}} {
		g, ds := Build(ss)
		if len(ds) == 0 || g != nil {
			t.Fatal("invalid graph accepted")
		}
	}
}
