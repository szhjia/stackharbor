package sessionhost

import (
	"context"
	"github.com/szhjia/stackharbor/internal/docker"
	"github.com/szhjia/stackharbor/internal/model"
)

func (h *Host) ObservationManagers() []*docker.Manager { return h.session.ObservationManagers() }

// Observe is driven by the shared engine, not by one timer per host.
func (h *Host) Observe(ctx context.Context) {
	h.observationMu.Lock()
	defer h.observationMu.Unlock()
	h.session.Observe(ctx)
}

func (h *Host) OwnershipCounts() (processes, sessionResources int) {
	return h.session.OwnershipCounts()
}

func (h *Host) ReloadEnvironment(w model.Workspace, environment ...[]string) error {
	return h.coordinator.Reconfigure(func() error {
		h.observationMu.Lock()
		defer h.observationMu.Unlock()
		return h.session.ReloadEnvironment(w, environment...)
	})
}
