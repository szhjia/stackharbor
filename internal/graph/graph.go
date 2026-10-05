package graph

import (
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"sort"
)

type Graph struct {
	services map[model.ServiceID]model.Service
	order    []model.ServiceID
}

func Build(services []model.Service) (*Graph, []model.Diagnostic) {
	g := &Graph{services: map[model.ServiceID]model.Service{}}
	ds := []model.Diagnostic{}
	ports := map[int]model.ServiceID{}
	for _, s := range services {
		if _, ok := g.services[s.ID]; ok {
			ds = append(ds, model.Error(s.SourceFile, "services", "duplicate service ID"))
		}
		g.services[s.ID] = s
		for _, p := range s.Ports {
			if other, ok := ports[p.Number]; ok {
				ds = append(ds, model.Error(s.SourceFile, "ports", fmt.Sprintf("port %d shared with %s", p.Number, other)))
			}
			ports[p.Number] = s.ID
		}
	}
	ids := []model.ServiceID{}
	for id, s := range g.services {
		ids = append(ids, id)
		for _, dep := range s.DependsOn {
			if _, ok := g.services[dep]; !ok {
				ds = append(ds, model.Error(s.SourceFile, "depends_on", "missing dependency "+string(dep)))
			}
		}
		for _, edge := range s.Requires {
			n, ok := g.services[edge.Node]
			if !ok {
				continue
			}
			valid := edge.Condition == "succeeded" && n.Kind == "task" || edge.Condition == "available" && n.Kind == "resource" || edge.Condition == "started" && (n.Kind == "" || n.Kind == "service") || edge.Condition == "ready" && (n.Kind == "" || n.Kind == "service") && n.Ready != nil
			if !valid {
				ds = append(ds, model.Error(s.SourceFile, "requires", "condition incompatible with "+string(edge.Node)))
			}
			if n.Task != nil && n.Task.Policy == "manual" {
				ds = append(ds, model.Error(s.SourceFile, "requires", "manual task cannot be an automatic dependency"))
			}
		}
	}
	if len(ds) > 0 {
		return nil, ds
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	state := map[model.ServiceID]int{}
	var visit func(model.ServiceID) bool
	visit = func(id model.ServiceID) bool {
		if state[id] == 1 {
			ds = append(ds, model.Error(g.services[id].SourceFile, "depends_on", "dependency cycle at "+string(id)))
			return false
		}
		if state[id] == 2 {
			return true
		}
		state[id] = 1
		deps := append([]model.ServiceID{}, g.services[id].DependsOn...)
		sort.Slice(deps, func(i, j int) bool { return deps[i] < deps[j] })
		for _, d := range deps {
			if !visit(d) {
				return false
			}
		}
		state[id] = 2
		g.order = append(g.order, id)
		return true
	}
	for _, id := range ids {
		if !visit(id) {
			return nil, ds
		}
	}
	return g, nil
}
func (g *Graph) StartOrder(ids []model.ServiceID) []model.ServiceID {
	seen := map[model.ServiceID]bool{}
	var add func(model.ServiceID)
	add = func(id model.ServiceID) {
		if seen[id] {
			return
		}
		if s, ok := g.services[id]; ok {
			seen[id] = true
			for _, d := range s.DependsOn {
				add(d)
			}
		}
	}
	for _, id := range ids {
		add(id)
	}
	out := []model.ServiceID{}
	for _, id := range g.order {
		if seen[id] {
			out = append(out, id)
		}
	}
	return out
}
func (g *Graph) StopOrder(ids []model.ServiceID) []model.ServiceID {
	seen := map[model.ServiceID]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	out := []model.ServiceID{}
	for i := len(g.order) - 1; i >= 0; i-- {
		if seen[g.order[i]] {
			out = append(out, g.order[i])
		}
	}
	return out
}
func (g *Graph) Dependents(ids []model.ServiceID) []model.ServiceID {
	roots := map[model.ServiceID]bool{}
	for _, id := range ids {
		roots[id] = true
	}
	seen := map[model.ServiceID]bool{}
	for _, id := range g.order {
		for _, dep := range g.services[id].DependsOn {
			if roots[dep] || seen[dep] {
				seen[id] = true
			}
		}
	}
	out := []model.ServiceID{}
	for id := range seen {
		if !roots[id] {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
