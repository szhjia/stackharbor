package graph

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"sort"
)

type Plan struct {
	SchemaVersion int                 `json:"schema_version"`
	ConfigDigest  string              `json:"config_digest"`
	Action        string              `json:"action"`
	Targets       []model.ServiceID   `json:"targets"`
	OrderedLayers [][]model.ServiceID `json:"ordered_layers"`
	Edges         []PlannedEdge       `json:"edges"`
	Actions       []PlannedNode       `json:"actions"`
	Unresolved    []string            `json:"unresolved_observations"`
}
type PlannedNode struct {
	Control   string              `json:"control,omitempty"`
	Operation string              `json:"operation"`
	ID        model.ServiceID     `json:"id"`
	Kind      string              `json:"kind"`
	Name      string              `json:"name"`
	Requires  []model.Requirement `json:"requires"`
	Effect    string              `json:"effect,omitempty"`
	Policy    string              `json:"policy,omitempty"`
}

func (g *Graph) Plan(w model.Workspace, action string, ids []model.ServiceID) (Plan, error) {
	p := Plan{SchemaVersion: 2, Action: action, Targets: ids, Edges: []PlannedEdge{}, Actions: []PlannedNode{}, OrderedLayers: [][]model.ServiceID{}, Unresolved: []string{}}
	if action != "start" && action != "stop" && action != "restart" {
		return p, fmt.Errorf("unknown plan action")
	}
	for _, id := range ids {
		if _, ok := g.services[id]; !ok {
			return p, fmt.Errorf("unknown target %s", id)
		}
	}
	// Hash configuration including the frozen context without serializing it into the public plan.
	b, _ := json.Marshal(w)
	p.ConfigDigest = fmt.Sprintf("%x", sha256.Sum256(b))
	order := g.StartOrder(ids)
	if action != "start" {
		all := append(append([]model.ServiceID{}, ids...), g.Dependents(ids)...)
		order = g.StopOrder(all)
		p.Unresolved = append(p.Unresolved, "Stop/restart effects must be checked against the running set at execution time")
	}
	depth := map[model.ServiceID]int{}
	for _, id := range g.order {
		n := g.services[id]
		for _, dep := range n.DependsOn {
			if depth[id] < depth[dep]+1 {
				depth[id] = depth[dep] + 1
			}
		}
	}
	if action == "start" {
		sort.SliceStable(order, func(i, j int) bool {
			if depth[order[i]] != depth[order[j]] {
				return depth[order[i]] < depth[order[j]]
			}
			if g.services[order[i]].Kind == "resource" && g.services[order[j]].Kind != "resource" {
				return true
			}
			if g.services[order[j]].Kind == "resource" && g.services[order[i]].Kind != "resource" {
				return false
			}
			return order[i] < order[j]
		})
	}
	last := -1
	for _, id := range order {
		n := g.services[id]
		kind := n.Kind
		if kind == "" {
			kind = "service"
		}
		v := PlannedNode{ID: id, Kind: kind, Name: n.Name, Requires: n.Requires}
		v.Control = n.Control
		v.Operation = action
		if n.Control == "observe" || n.Resource != nil && n.Resource.Control == "observe" {
			v.Operation = "observe"
		}
		if n.Task != nil {
			v.Operation = "run"
			if n.Task.Policy == "when-needed" {
				v.Operation = "check/run/verify"
			}
			if action == "stop" {
				v.Operation = "retain-result"
			}
		}
		if n.Task != nil {
			v.Effect = n.Task.Effect
			v.Policy = n.Task.Policy
			if n.Task.Policy == "when-needed" {
				p.Unresolved = append(p.Unresolved, string(id)+": state must be verified by running check while holding the lock")
			}
		}
		p.Actions = append(p.Actions, v)
		if depth[id] != last {
			p.OrderedLayers = append(p.OrderedLayers, []model.ServiceID{})
			last = depth[id]
		}
		p.OrderedLayers[len(p.OrderedLayers)-1] = append(p.OrderedLayers[len(p.OrderedLayers)-1], id)
		for _, edge := range n.Requires {
			p.Edges = append(p.Edges, PlannedEdge{From: edge.Node, To: id, Condition: edge.Condition})
		}
	}
	return p, nil
}

// Preserve a project group where possible; split interleaved project stages for a legal node DAG.
func (g *Graph) Navigation(w model.Workspace) []model.Project {
	ids := []model.ServiceID{}
	for id := range g.services {
		ids = append(ids, id)
	}
	plan, _ := g.Plan(w, "start", ids)
	defs := map[string]model.Project{}
	for _, p := range w.Projects {
		defs[p.ID] = p
	}
	out := []model.Project{}
	seen := map[string]int{}
	for _, layer := range plan.OrderedLayers {
		for _, id := range layer {
			n := g.services[id]
			if len(out) == 0 || out[len(out)-1].ID != n.ProjectID {
				p := defs[n.ProjectID]
				p.Services = nil
				seen[p.ID]++
				if seen[p.ID] > 1 {
					p.Name += fmt.Sprintf(" · stage %d", seen[p.ID])
				}
				out = append(out, p)
			}
			out[len(out)-1].Services = append(out[len(out)-1].Services, n)
		}
	}
	return out
}

type PlannedEdge struct {
	From      model.ServiceID `json:"from"`
	To        model.ServiceID `json:"to"`
	Condition string          `json:"condition"`
}
