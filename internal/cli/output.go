package cli

import (
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"io"
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
