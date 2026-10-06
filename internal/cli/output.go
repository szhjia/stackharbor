package cli

import (
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/inventory"
	"github.com/szhjia/stackharbor/internal/model"
	"io"
	"time"
)

func WriteDiscovery(out io.Writer, w model.Workspace, jsonMode bool) error {
	if jsonMode {
		if w.Version == 2 {
			type node struct {
				ID       model.ServiceID     `json:"id"`
				Kind     string              `json:"kind"`
				Name     string              `json:"name"`
				Project  string              `json:"project"`
				Cwd      string              `json:"cwd"`
				Requires []model.Requirement `json:"requires"`
				Effect   string              `json:"effect,omitempty"`
				Policy   string              `json:"policy,omitempty"`
			}
			nodes := []node{}
			for _, n := range w.Services() {
				v := node{ID: n.ID, Kind: n.Kind, Name: n.Name, Project: n.ProjectID, Cwd: n.Cwd, Requires: n.Requires}
				if v.Kind == "" {
					v.Kind = "service"
				}
				if n.Task != nil {
					v.Effect = n.Task.Effect
					v.Policy = n.Task.Policy
				}
				nodes = append(nodes, v)
			}
			e := json.NewEncoder(out)
			e.SetIndent("", "  ")
			return e.Encode(struct {
				Schema      int                `json:"schema_version"`
				Root        string             `json:"root"`
				Nodes       []node             `json:"nodes"`
				Diagnostics []model.Diagnostic `json:"diagnostics"`
			}{2, w.Root, nodes, w.Diagnostics})
		}
		type service struct {
			DockerDependsOn []string        `json:"docker_depends_on,omitempty"`
			ID              model.ServiceID `json:"id"`
			Name            string          `json:"name"`
			Cwd             string          `json:"cwd"`
			Command         []string        `json:"command"`
			Ports           []model.Port    `json:"ports"`
		}
		type project struct {
			ID       string    `json:"id"`
			Name     string    `json:"name"`
			Source   string    `json:"source"`
			Services []service `json:"services"`
		}
		ps := []project{}
		for _, p := range w.Projects {
			v := project{ID: p.ID, Name: p.Name, Source: p.SourceFile, Services: []service{}}
			for _, s := range p.Services {
				v.Services = append(v.Services, service{s.DockerDependsOn, s.ID, s.Name, s.Cwd, s.Command, s.Ports})
			}
			ps = append(ps, v)
		}
		e := json.NewEncoder(out)
		e.SetIndent("", "  ")
		return e.Encode(struct {
			Schema      int                `json:"schema_version"`
			Root        string             `json:"root"`
			Projects    []project          `json:"projects"`
			Candidates  []model.Candidate  `json:"candidates"`
			Diagnostics []model.Diagnostic `json:"diagnostics"`
		}{1, w.Root, ps, w.Candidates, w.Diagnostics})
	}
	fmt.Fprintln(out, "Root:", w.Root)
	for _, p := range w.Projects {
		fmt.Fprintf(out, "%s · %s (%d services)\n", p.ID, p.Name, len(p.Services))
	}
	for _, c := range w.Candidates {
		fmt.Fprintf(out, "? %s · Unregistered · %s\n", c.Name, c.Cwd)
	}
	for _, d := range w.Diagnostics {
		fmt.Fprintf(out, "%s: %s [%s] %s\n", d.Severity, d.File, d.Field, d.Message)
	}
	return nil
}

func encodeControl(out io.Writer, value any) int {
	if e := json.NewEncoder(out).Encode(value); e != nil {
		return 1
	}
	return 0
}
func writePlan(out, errOut io.Writer, p control.Plan) int {
	if _, e := fmt.Fprintf(out, "Plan %s: %s targets=%v affected=%v expires=%s\n", p.ID, p.Action, p.Targets, p.Affected, p.ExpiresAt.Format(time.RFC3339Nano)); e != nil {
		return controlFailure(errOut, e)
	}
	for _, warning := range p.Warnings {
		if _, e := fmt.Fprintln(out, "Warning:", warning); e != nil {
			return controlFailure(errOut, e)
		}
	}
	return 0
}
func writeOperationValue(out io.Writer, op control.Operation, jsonMode bool) int {
	if jsonMode {
		return encodeControl(out, op)
	}
	if _, e := fmt.Fprintf(out, "Operation %s: %s %s\n", op.ID, op.Action, op.State); e != nil {
		return 1
	}
	for _, r := range op.Results {
		detail := ""
		if r.Error != nil {
			detail = " — " + r.Error.Message
		}
		if _, e := fmt.Fprintf(out, "  %s: %s%s\n", r.Target, r.State, detail); e != nil {
			return 1
		}
	}
	return 0
}
func writeOperation(out, errOut io.Writer, op control.Operation, jsonMode bool) int {
	if code := writeOperationValue(out, op, jsonMode); code != 0 {
		return code
	}
	if !operationTerminal(op.State) {
		return 3
	}
	if op.Error != nil {
		fmt.Fprintln(errOut, op.Error)
	}
	if op.State == "succeeded" {
		return 0
	}
	return 1
}
func writeSnapshot(out, errOut io.Writer, s control.Snapshot, jsonMode bool) int {
	if jsonMode {
		return encodeControl(out, s)
	}
	fmt.Fprintf(out, "Session %s observed %s\n", s.Identity.SessionID, s.ObservedAt.Format(time.RFC3339Nano))
	for _, n := range s.Nodes {
		if _, e := fmt.Fprintf(out, "%s: %s (%s) %s\n", n.ID, n.State, n.Ownership, n.Reason); e != nil {
			return controlFailure(errOut, e)
		}
	}
	return 0
}
func writeInventory(out io.Writer, inv inventory.Inventory, jsonMode bool) error {
	if jsonMode {
		return json.NewEncoder(out).Encode(inv)
	}
	if inv.Partial {
		if _, e := fmt.Fprintln(out, "Coverage partial: some sessions are unreachable."); e != nil {
			return e
		}
	}
	for _, s := range inv.Sessions {
		if _, e := fmt.Fprintf(out, "%q session=%s available=%t\n", s.Root, s.Identity.SessionID, s.Available); e != nil {
			return e
		}
		if s.Snapshot != nil {
			for _, n := range s.Snapshot.Nodes {
				if _, e := fmt.Fprintf(out, "  %s: %s\n", n.ID, n.State); e != nil {
					return e
				}
			}
		} else if s.Error != nil {
			if _, e := fmt.Fprintln(out, " ", s.Error); e != nil {
				return e
			}
		}
	}
	return nil
}
