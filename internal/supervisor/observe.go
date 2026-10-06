package supervisor

import (
	"context"
	"github.com/szhjia/stackharbor/internal/model"
	"time"
)

func (s *Session) observeLoop() {
	defer s.workers.Done()
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
			s.sample(s.ctx)
		}
	}
}
func (s *Session) sample(ctx context.Context) {
	s.mu.Lock()
	owned := map[model.ServiceID][]model.ProcessIdentity{}
	gens := map[model.ServiceID]uint64{}
	specs := map[model.ServiceID]model.Service{}
	for id, e := range s.entries {
		gens[id] = e.gen
		specs[id] = e.spec
		if e.handle != nil {
			owned[id] = e.handle.Identities()
		}
	}
	s.mu.Unlock()
	ports := map[model.ServiceID][]model.PortObservation{}
	allDeclared := []model.Port{}
	allOwned := []model.ProcessIdentity{}
	for id, spec := range specs {
		allDeclared = append(allDeclared, spec.Ports...)
		allOwned = append(allOwned, owned[id]...)
	}
	observations := s.ports.Observe(ctx, allDeclared, allOwned)
	for id, spec := range specs {
		declared := map[int]bool{}
		pids := map[int32]bool{}
		for _, p := range spec.Ports {
			declared[p.Number] = true
		}
		for _, p := range owned[id] {
			pids[p.PID] = true
		}
		for _, obs := range observations {
			visible := declared[obs.Port]
			for _, listener := range obs.Listeners {
				if pids[listener.PID] {
					visible = true
				}
			}
			if !visible {
				continue
			}
			if obs.Status == "owned" {
				for _, listener := range obs.Listeners {
					if !pids[listener.PID] {
						obs.Status = "external"
					}
				}
			}
			ports[id] = append(ports[id], obs)
		}
	}
	metrics := map[model.ServiceID]model.Metric{}
	tool := model.Metric{}
	sources := map[model.ServiceID]string{}
	physical := map[model.ServiceID][]model.ProcessSample{}
	if s.sampler != nil {
		metrics, tool, sources, physical = s.sampler.SampleServicesDetailed(ctx, owned, ports, s.toolID)
	}
	if ctx.Err() != nil {
		return
	}

	dockerRows, dockerError := s.docker.Observe(ctx)
	if len(s.resources) == 0 {
		dockerRows = s.docker.SampleMetrics(ctx, dockerRows)
	}
	externalDocker := s.sampleContracts(ctx, gens, ports, owned)
	if len(s.resources) == 0 && dockerError == "" {
		externalDocker = externalDockerServices(specs, ports, owned, dockerRows)
	}
	observedStates, observedReasons := observeExternalServices(ctx, specs, ports, owned)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.resources) == 0 {
		s.dockerRows, s.dockerError = dockerRows, dockerError
	}
	s.tool = tool
	s.observedAt = time.Now().UTC()
	resourceMetrics := map[model.ServiceID]model.DockerSnapshot{}
	for _, row := range s.dockerRows {
		resourceMetrics[model.ServiceID(row.Service)] = row
	}
	for id, e := range s.entries {
		if e.gen == gens[id] {
			e.metric = metrics[id]
			e.processSamples = physical[id]
			e.metricSource = sources[id]
			e.metricError = ""
			e.observedState, e.observedReason = observedStates[id], observedReasons[id]
			if row, ok := externalDocker[id]; ok {
				e.metric, e.metricError = row.Metric, row.MetricError
				e.metricSource = "docker-external"
			}
			if e.spec.Resource != nil {
				row := resourceMetrics[id]
				e.metric, e.metricError = row.Metric, row.MetricError
				e.metricSource = "docker"
			}
			e.ports = ports[id]
		}
	}
}
