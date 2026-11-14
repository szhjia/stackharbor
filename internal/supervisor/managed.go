package supervisor

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/docker"
	"github.com/szhjia/stackharbor/internal/model"
	"reflect"
	"slices"
)

func (s *Session) Observe(ctx context.Context) { s.sample(ctx) }

func (s *Session) ObservationManagers() []*docker.Manager {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*docker.Manager{s.docker}
	seen := map[*docker.Manager]bool{s.docker: true}
	for _, manager := range s.resources {
		if !seen[manager] {
			out = append(out, manager)
			seen[manager] = true
		}
	}
	return out
}

// OwnershipCounts uses physical ownership rather than logical readiness. Failed
// or unhealthy session resources can still need cleanup and must keep the
// shared engine alive. Explicitly stopped resources may retain historical IDs.
func (s *Session) OwnershipCounts() (processes, sessionResources int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owned := map[model.ProcessIdentity]bool{}
	for _, e := range s.entries {
		if e.handle != nil {
			for _, identity := range e.handle.Identities() {
				owned[identity] = true
			}
		}
		r := e.spec.Resource
		if r != nil && r.Lifetime == "session" && r.Control != "observe" && e.spec.Control != "observe" &&
			(e.resourceRunning || e.resourceMutation || e.state != "stopped" && len(e.resourceInstances) > 0) {
			sessionResources++
		}
	}
	return len(owned), sessionResources
}

// ReloadEnvironment only changes future launches. Changes to topology or input
// files require explicitly closing/reopening the workspace instead. Providing a
// base environment also refreshes legacy Docker and adopted-container managers;
// without one, their previously frozen base environment remains authoritative.
func (s *Session) ReloadEnvironment(w model.Workspace, environment ...[]string) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("session closed")
	}
	if len(environment) > 1 {
		return fmt.Errorf("at most one base environment is accepted")
	}
	if err := s.checkInputs(); err != nil {
		return err
	}
	nodes := w.Services()
	if w.Root != s.workspace.Root || len(nodes) != len(s.entries) {
		return fmt.Errorf("workspace topology changed; reopen the session")
	}
	for _, n := range nodes {
		e := s.entries[n.ID]
		if e == nil || !reflect.DeepEqual(n.Command, e.spec.Command) || n.Cwd != e.spec.Cwd || !reflect.DeepEqual(n.DependsOn, e.spec.DependsOn) {
			return fmt.Errorf("workspace topology changed; reopen the session")
		}
	}
	// Stage every manager first. A rejected refresh leaves both process and
	// Docker environments unchanged, including managers shared by resource nodes.
	updates := map[*docker.Manager][]string{}
	protected := map[*docker.Manager]bool{}
	if len(environment) == 1 {
		updates[s.docker] = append([]string{}, environment[0]...)
	}
	for _, row := range s.dockerRows {
		if row.ID != "" {
			protected[s.docker] = true
		}
	}
	for _, n := range nodes {
		if m := s.resources[n.ID]; m != nil {
			_, env := n.Resource.ComposeEvidence()
			if env == nil && len(environment) == 1 {
				env = append([]string{}, environment[0]...)
			}
			if env != nil {
				if previous, exists := updates[m]; exists && !slices.Equal(previous, env) {
					return fmt.Errorf("resource manager environments disagree; reopen the session")
				}
				updates[m] = append([]string{}, env...)
			}
			e := s.entries[n.ID]
			protected[m] = protected[m] || e.resourceRunning || e.resourceMutation || len(e.resourceInstances) > 0
		}
	}
	for manager, env := range updates {
		if manager != nil {
			if err := manager.ValidateEnvironment(env, protected[manager]); err != nil {
				return err
			}
		}
	}
	for manager, env := range updates {
		if manager != nil {
			manager.SetEnvironment(env)
		}
	}
	for _, n := range nodes {
		s.entries[n.ID].spec.Env = n.Env
		s.entries[n.ID].spec.EnvFrozen = true
	}
	for i := range s.workspace.Projects {
		for j := range s.workspace.Projects[i].Services {
			old := &s.workspace.Projects[i].Services[j]
			old.Env = s.entries[old.ID].spec.Env
			old.EnvFrozen = true
		}
	}
	return nil
}
