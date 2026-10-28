package docker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
)

// NewAdopted controls containers that already exist in a named Compose project.
// It never invokes Compose, creates a container, or follows a replacement ID.
func NewAdopted(root, project string, services []string) *Manager {
	m := NewScoped(root, "", project)
	m.File = "docker:existing"
	m.adopted = true
	m.Error = ""
	m.pinned = map[string]string{}
	m.specs = nil
	for _, service := range services {
		m.specs = append(m.specs, model.DockerSnapshot{Service: service, Name: service, State: "unknown"})
	}
	return m
}

type adoptedContainer struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status string `json:"Status"`
		Health *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

var fullContainerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (m *Manager) adoptedContainers(ctx context.Context, services []string) (map[string]adoptedContainer, error) {
	wanted := map[string]bool{}
	for _, service := range services {
		if service == "" || strings.HasPrefix(service, "-") {
			return nil, errors.New("Invalid container service")
		}
		wanted[service] = true
	}
	raw, err := m.run(ctx, "ps", "--all", "--no-trunc", "--filter", "label=com.docker.compose.project="+m.Project, "--format", `{{.ID}} {{.Label "com.docker.compose.service"}}`)
	if err != nil {
		return nil, errors.New("Docker container identity unavailable")
	}
	ids := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !fullContainerID.MatchString(fields[0]) {
			return nil, errors.New("Docker returned an invalid container identity")
		}
		service := fields[1]
		if !wanted[service] {
			continue
		}
		if ids[service] != "" {
			return nil, fmt.Errorf("Expected exactly one existing container for %s/%s; found multiple", m.Project, service)
		}
		ids[service] = fields[0]
	}
	args := []string{"inspect"}
	for _, service := range services {
		id := ids[service]
		if id == "" {
			return nil, fmt.Errorf("Expected exactly one existing container for %s/%s; found none", m.Project, service)
		}
		args = append(args, id)
	}
	raw, err = m.run(ctx, args...)
	if err != nil {
		return nil, errors.New("Docker container inspection failed")
	}
	var inspected []adoptedContainer
	if json.Unmarshal(raw, &inspected) != nil || len(inspected) != len(services) {
		return nil, errors.New("Docker container inspection incomplete")
	}
	rows := map[string]adoptedContainer{}
	for _, row := range inspected {
		service := row.Config.Labels["com.docker.compose.service"]
		if row.Config.Labels["com.docker.compose.project"] != m.Project || ids[service] != row.ID || rows[service].ID != "" {
			return nil, errors.New("Docker container labels or identity changed")
		}
		rows[service] = row
	}
	return rows, nil
}

// Pin is called when a session opens. A missing or scaled service is not
// adoptable: neither the desired instance nor its lifecycle is unambiguous.
func (m *Manager) Pin(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	id, err := m.Identity(ctx)
	if err != nil {
		return err
	}
	services := make([]string, 0, len(m.specs))
	for _, spec := range m.specs {
		services = append(services, spec.Service)
	}
	rows, err := m.adoptedContainers(ctx, services)
	if err != nil {
		return err
	}
	pinned := map[string]string{}
	for _, service := range services {
		pinned[service] = rows[service].ID
	}
	m.endpoint = id.Endpoint
	m.pinned = pinned
	return nil
}

func (m *Manager) verifyAdopted(ctx context.Context, services []string) (map[string]adoptedContainer, error) {
	id, err := m.Identity(ctx)
	if err != nil || id.Endpoint != m.endpoint || m.endpoint == "" {
		return nil, errors.New("Docker daemon identity changed; reopen the session")
	}
	rows, err := m.adoptedContainers(ctx, services)
	if err != nil {
		return nil, err
	}
	for _, service := range services {
		pinned := m.pinned[service]
		if pinned == "" {
			return nil, fmt.Errorf("Container %s was not pinned when the session opened", service)
		}
		if rows[service].ID != pinned {
			return nil, fmt.Errorf("Container %s identity changed; reopen the session", service)
		}
	}
	return rows, nil
}

func (m *Manager) observeAdopted(ctx context.Context) ([]model.DockerSnapshot, string) {
	services := make([]string, 0, len(m.specs))
	for _, spec := range m.specs {
		services = append(services, spec.Service)
	}
	rows, err := m.verifyAdopted(ctx, services)
	if err != nil {
		return m.Specs(), err.Error()
	}
	out := m.Specs()
	for i := range out {
		row := rows[out[i].Service]
		out[i].ID = row.ID
		out[i].Name = strings.TrimPrefix(row.Name, "/")
		out[i].Image = row.Config.Image
		out[i].State = row.State.Status
		if row.State.Health != nil {
			out[i].Health = row.State.Health.Status
		}
		out[i].EndpointIdentity = m.endpoint
		out[i].ComposeProject = m.Project
	}
	return out, ""
}

func (m *Manager) actionAdopted(ctx context.Context, action string, services []string) error {
	if action != "start" && action != "stop" && action != "restart" {
		return errors.New("Unknown Docker action")
	}
	if len(services) == 0 {
		return nil
	}
	keys, err := m.LockKeys(ctx, services)
	if err != nil {
		return err
	}
	locked, release, err := control.WithResourceLocks(ctx, control.ResourceLockNamespace, keys)
	if err != nil {
		return err
	}
	defer release()
	ctx = locked
	rows, err := m.verifyAdopted(ctx, services)
	if err != nil {
		return err
	}
	for _, service := range services {
		args := []string{"container", action}
		if action != "start" {
			args = append(args, "--time", "15")
		}
		args = append(args, rows[service].ID)
		if _, err := m.run(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) bindingAdopted(ctx context.Context, services []string) (string, error) {
	rows, err := m.verifyAdopted(ctx, services)
	if err != nil {
		return "", err
	}
	parts := []string{m.endpoint, m.Project}
	for _, service := range services {
		row := rows[service]
		parts = append(parts, service+":"+row.ID+":"+row.State.Status)
		if row.State.Health != nil {
			parts = append(parts, service+":health:"+row.State.Health.Status)
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "\x00")))), nil
}

// Keep adoption checks bounded even if the Docker daemon becomes unresponsive.
func (m *Manager) adoptedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Second)
}
