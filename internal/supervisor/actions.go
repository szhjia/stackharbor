package supervisor

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/process"
)

func (s *Session) Conflicts(ctx context.Context, ids []model.ServiceID) ([]model.PortConflict, error) {
	s.mu.Lock()
	ports := []model.Port{}
	owned := []model.ProcessIdentity{}
	for _, id := range s.graph.StartOrder(ids) {
		if e := s.entries[id]; e != nil && e.spec.Control != "observe" {
			ports = append(ports, e.spec.Ports...)
		}
	}
	for _, e := range s.entries {
		if e.handle != nil {
			owned = append(owned, e.handle.Identities()...)
		}
	}
	s.mu.Unlock()
	return observe.FindConflicts(ctx, s.ports, process.NewReader(), ports, owned)
}
func (s *Session) ResolveConflicts(ctx context.Context, ids []model.ServiceID, targets []model.PortConflict, action string) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("Session closed")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	if err := observe.ReleaseConflicts(ctx, s.ports, process.NewReader(), targets); err != nil {
		return err
	}
	if action == "all-start" || action == "all-restart" {
		return s.AllAction(ctx, action[4:])
	}
	if action == "restart" {
		return s.Restart(ctx, ids)
	}
	return s.Start(ctx, ids)
}
func (s *Session) DockerAction(ctx context.Context, action string, names []string) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("Session closed")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	if len(s.resources) > 0 {
		ids := []model.ServiceID{}
		for _, name := range names {
			id := model.ServiceID(name)
			if s.resources[id] == nil {
				return fmt.Errorf("Unregistered resource %s", name)
			}
			ids = append(ids, id)
		}
		switch action {
		case "start":
			return s.Start(ctx, ids)
		case "stop":
			return s.Stop(ctx, ids)
		case "restart":
			return s.Restart(ctx, ids)
		default:
			return fmt.Errorf("Unknown resource action")
		}
	}
	if action != "start" && action != "stop" && action != "restart" {
		return fmt.Errorf("Unknown Docker action")
	}
	if _, err := s.docker.DependencyOrder(names); err != nil {
		return err
	}
	active := []model.ServiceID{}
	if action != "start" {
		s.mu.Lock()
		var scopeErr error
		active, _, scopeErr = s.legacyDockerScopeLocked(names, "stop")
		s.mu.Unlock()
		if scopeErr != nil {
			return scopeErr
		}
		if err := s.Stop(ctx, active); err != nil {
			return err
		}
	}
	if err := s.docker.Action(ctx, action, names); err != nil {
		return err
	}
	if action == "restart" {
		if err := s.docker.Ensure(ctx, names); err != nil {
			return err
		}
		if err := s.Start(ctx, active); err != nil {
			return err
		}
	}
	rows, err := s.docker.Observe(ctx)
	s.mu.Lock()
	s.dockerRows, s.dockerError = rows, err
	s.event("", "docker", "Docker "+action+": "+fmt.Sprint(names))
	s.mu.Unlock()
	return nil
}
func (s *Session) AllAction(ctx context.Context, action string) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("Session closed")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	if action != "start" && action != "stop" && action != "restart" {
		return fmt.Errorf("Unknown global service action")
	}
	s.mu.Lock()
	ids, active, _, order, err := s.allActionScopeLocked(action)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if action == "start" {
		return s.Start(ctx, ids)
	}
	if err = s.Stop(ctx, active); err != nil {
		return err
	}
	if s.workspace.Version == 2 {
		if action == "restart" {
			return s.Start(ctx, active)
		}
		return nil
	}
	for i := len(order) - 1; i >= 0; i-- {
		if err = s.docker.Action(ctx, "stop", []string{order[i]}); err != nil {
			return err
		}
	}
	if len(order) > 0 {
		rows, observeErr := s.docker.Observe(ctx)
		s.mu.Lock()
		s.dockerRows, s.dockerError = rows, observeErr
		s.event("", "docker", "Docker stop: "+fmt.Sprint(order))
		s.mu.Unlock()
	}
	if action == "restart" {
		return s.Start(ctx, active)
	}
	return nil
}

// Caller holds session.mu. Plan and execution share both the graph effects and
// v1 Docker consumers; all_actions only selects direct global targets.
func (s *Session) allActionScopeLocked(action string) (ids, active, relevant []model.ServiceID, dockerOrder []string, err error) {
	deps := []string{}
	for _, spec := range s.workspace.Services() {
		if !spec.InAllActions() {
			continue
		}
		e := s.entries[spec.ID]
		if action == "restart" && s.workspace.Version == 2 && e.handle == nil && e.state != "waiting" && e.state != "starting" && !(spec.Resource != nil && (e.state == "available" || e.resourceRunning)) {
			continue
		}
		ids = append(ids, spec.ID)
		deps = append(deps, spec.DockerDependsOn...)
	}
	dockerOrder, err = s.docker.DependencyOrder(deps)
	if err != nil {
		return
	}
	active = append([]model.ServiceID{}, ids...)
	if action == "start" {
		relevant = s.graph.StartOrder(ids)
		return
	}
	active = append(active, s.affectedLocked(ids)...)
	relevant = append(append([]model.ServiceID{}, ids...), s.graph.Dependents(ids)...)
	if s.workspace.Version != 2 && len(s.resources) == 0 {
		var consumers, potential []model.ServiceID
		consumers, potential, err = s.legacyDockerScopeLocked(dockerOrder, "stop")
		if err != nil {
			return
		}
		active = append(active, consumers...)
		relevant = append(relevant, potential...)
	}
	active = uniqueServiceIDs(active)
	if action == "restart" {
		relevant = append(relevant, s.graph.StartOrder(active)...)
	}
	relevant = uniqueServiceIDs(relevant)
	return
}
