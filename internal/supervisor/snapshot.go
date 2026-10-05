package supervisor

import (
	"encoding/json"
	"github.com/szhjia/stackharbor/internal/model"
)

func (s *Session) Snapshot() model.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := model.Snapshot{Docker: s.dockerRows, DockerFile: s.docker.File, DockerError: s.dockerError, Root: s.workspace.Root, Projects: s.workspace.Projects, Candidates: s.workspace.Candidates, Diagnostics: s.workspace.Diagnostics, Tool: s.tool, Events: s.events}
	if len(s.resources) > 0 {
		for _, n := range s.workspace.Services() {
			if n.Resource != nil {
				out.DockerFile = n.Resource.File
				break
			}
		}
	}
	for _, spec := range s.workspace.Services() {
		e := s.entries[spec.ID]
		v := model.ServiceSnapshot{Spec: e.spec, State: e.state, Reason: e.reason, ExitCode: e.exit, Metric: e.metric, MetricSource: e.metricSource, MetricError: e.metricError, Ports: e.ports, ReadinessChecked: e.checked}
		if e.handle != nil {
			v.Owned = e.handle.Identities()
		}
		out.Services = append(out.Services, v)
	}
	raw, _ := json.Marshal(out)
	var copy model.Snapshot
	_ = json.Unmarshal(raw, &copy)
	return copy
}
