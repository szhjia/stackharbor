package supervisor

import (
	"context"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/docker"
	"github.com/szhjia/stackharbor/internal/model"
	"sort"
	"strings"
)

// Compute the complete resource closure before taking any resource locks. Runtime
// recursion then borrows subsets; it never upgrades a held subset out of order.
func (s *Session) resourceEvidence(ctx context.Context, req control.PlanRequest, relevant []model.ServiceID) ([]string, []string, error) {
	groups := map[*docker.Manager]map[string]bool{}
	add := func(m *docker.Manager, names []string) {
		if len(names) == 0 {
			return
		}
		if groups[m] == nil {
			groups[m] = map[string]bool{}
		}
		for _, name := range names {
			groups[m][name] = true
		}
	}
	action := strings.TrimPrefix(req.Action, "release-")
	launch := strings.HasSuffix(action, "start") || strings.HasSuffix(action, "restart")
	if action == "release" || action == "close" {
		return nil, nil, nil
	}
	for _, id := range relevant {
		e := s.entries[id]
		if e == nil {
			continue
		}
		if e.spec.Resource != nil {
			add(s.resources[id], []string{e.spec.Resource.Service})
		}
		if launch {
			names, err := s.docker.DependencyOrder(e.spec.DockerDependsOn)
			if err != nil {
				return nil, nil, err
			}
			add(s.docker, names)
		}
	}
	if len(s.resources) == 0 && strings.HasPrefix(action, "docker-") {
		names := req.Targets
		if action == "docker-start" || action == "docker-restart" {
			var err error
			names, err = s.docker.DependencyOrder(names)
			if err != nil {
				return nil, nil, err
			}
		}
		add(s.docker, names)
	}
	if len(s.resources) == 0 && strings.HasPrefix(action, "all-") {
		names := []string{}
		for _, spec := range s.workspace.Services() {
			names = append(names, spec.DockerDependsOn...)
		}
		order, err := s.docker.DependencyOrder(names)
		if err != nil {
			return nil, nil, err
		}
		add(s.docker, order)
	}
	keys, bindings := []string{}, []string{}
	for manager, selected := range groups {
		if err := manager.CheckResolved(ctx); err != nil {
			return nil, nil, controlError("plan_conflict", err.Error())
		}
		if digest := manager.ConfigDigest(); digest != "" {
			bindings = append(bindings, manager.ScopeKey()+":"+digest)
		}
		names := []string{}
		for name := range selected {
			names = append(names, name)
		}
		sort.Strings(names)
		part, err := manager.LockKeys(ctx, names)
		if err != nil {
			return nil, nil, controlError("identity_conflict", "Docker endpoint identity cannot be verified; no resource changes allowed")
		}
		binding, err := manager.Binding(ctx, names)
		if err != nil {
			return nil, nil, controlError("identity_conflict", "Docker container identities cannot be verified; request a new plan")
		}
		keys = append(keys, part...)
		bindings = append(bindings, binding)
	}
	sort.Strings(keys)
	sort.Strings(bindings)
	return keys, bindings, nil
}
