package docker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (m *Manager) observationIdentity(ctx context.Context) (Identity, error) {
	if m.observationCache(ctx) == nil {
		return m.Identity(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := m.observationRun(ctx, "info", "--format", "{{json .ID}}")
	var daemon string
	if err != nil || json.Unmarshal(raw, &daemon) != nil || strings.TrimSpace(daemon) == "" {
		return Identity{}, errors.New("Docker daemon identity unavailable")
	}
	project := m.Project
	if project == "" {
		raw, err = m.observationRun(ctx, append(m.args(), "config", "--format", "json")...)
		var config struct {
			Name string `json:"name"`
		}
		if err != nil || json.Unmarshal(raw, &config) != nil {
			return Identity{}, errors.New("Compose project identity unavailable")
		}
		project = config.Name
	}
	if project == "" {
		return Identity{}, errors.New("Compose project identity unavailable")
	}
	return Identity{Endpoint: fmt.Sprintf("%x", sha256.Sum256([]byte(daemon))), Project: project}, nil
}
func (m *Manager) verifyAdoptedObservation(ctx context.Context, services []string) (map[string]adoptedContainer, error) {
	if m.observationCache(ctx) == nil {
		return m.verifyAdopted(ctx, services)
	}
	identity, err := m.observationIdentity(ctx)
	if err != nil || identity.Endpoint != m.endpoint || m.endpoint == "" {
		return nil, errors.New("Docker daemon identity changed; reopen the session")
	}
	rows, err := m.adoptedContainersWith(ctx, services, m.observationRun)
	if err != nil {
		return nil, err
	}
	for _, service := range services {
		if pinned := m.pinned[service]; pinned == "" || rows[service].ID != pinned {
			return nil, fmt.Errorf("Container %s identity changed; reopen the session", service)
		}
	}
	return rows, nil
}
