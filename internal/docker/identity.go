package docker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Identity struct{ Endpoint, Project string }

// Identity verifies the engine itself; Docker context names and transport paths
// are aliases, not physical identity. Only the digest enters public observations.
func (m *Manager) Identity(ctx context.Context) (Identity, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := m.run(ctx, "info", "--format", "{{json .ID}}")
	if err != nil {
		return Identity{}, fmt.Errorf("Docker daemon identity unavailable")
	}
	var daemon string
	if json.Unmarshal(raw, &daemon) != nil || strings.TrimSpace(daemon) == "" {
		return Identity{}, fmt.Errorf("Docker daemon identity unavailable")
	}
	project := m.Project
	if project == "" {
		raw, err = m.run(ctx, append(m.args(), "config", "--format", "json")...)
		if err != nil {
			return Identity{}, fmt.Errorf("Compose project identity unavailable")
		}
		var config struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &config) != nil {
			return Identity{}, fmt.Errorf("Compose project identity unavailable")
		}
		project = config.Name
	}
	if project == "" {
		return Identity{}, fmt.Errorf("Compose project identity unavailable")
	}
	return Identity{Endpoint: fmt.Sprintf("%x", sha256.Sum256([]byte(daemon))), Project: project}, nil
}
func (m *Manager) LockKeys(ctx context.Context, names []string) ([]string, error) {
	id, err := m.Identity(ctx)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for _, name := range names {
		if name == "" {
			return nil, fmt.Errorf("empty Compose service identity")
		}
		keys = append(keys, fmt.Sprintf("compose:%x", sha256.Sum256([]byte(id.Endpoint+"\x00"+id.Project+"\x00"+name))))
	}
	return keys, nil
}

// Binding captures every selected container instance, including an absent service,
// from a fresh no-trunc status read. It is used only as server-retained plan evidence.
func (m *Manager) Binding(ctx context.Context, names []string) (string, error) {
	identity, err := m.Identity(ctx)
	if err != nil {
		return "", err
	}
	raw, err := m.run(ctx, append(m.args(), "ps", "--all", "--format", "json", "--orphans=false", "--no-trunc")...)
	if err != nil {
		return "", fmt.Errorf("Docker container identity unavailable")
	}
	rows, err := parsePS(raw)
	if err != nil {
		return "", fmt.Errorf("Docker container identity unavailable")
	}
	selected := map[string]bool{}
	for _, name := range names {
		selected[name] = true
	}
	bindings := []string{identity.Endpoint, identity.Project}
	for name := range selected {
		bindings = append(bindings, "service:"+name)
	}
	for _, row := range rows {
		if selected[row.Service] {
			if row.ID == "" {
				return "", fmt.Errorf("Docker container identity unavailable")
			}
			bindings = append(bindings, row.Service+":"+row.ID)
		}
	}
	sort.Strings(bindings)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(bindings, "\x00")))), nil
}
