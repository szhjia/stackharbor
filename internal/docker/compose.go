package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"go.yaml.in/yaml/v3"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	mu         sync.Mutex
	root, File string
	Project    string
	specs      []model.DockerSnapshot
	deps       map[string][]string
	Error      string
	run        func(context.Context, ...string) ([]byte, error)
}

func New(root string) *Manager { return NewScoped(root, "", "") }
func NewScoped(root, file, project string) *Manager {
	m := &Manager{root: root, File: file, Project: project, deps: map[string][]string{}}
	m.run = func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("Docker action failed: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
	if file == "" {
		for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
			p := filepath.Join(root, name)
			if _, err := os.Stat(p); err == nil {
				m.File = p
				break
			}
		}
	}
	if m.File == "" {
		return m
	}
	f, err := os.Open(m.File)
	if err != nil {
		m.Error = err.Error()
		return m
	}
	defer f.Close()
	var config struct {
		Services map[string]struct {
			Image   string    `yaml:"image"`
			Name    string    `yaml:"container_name"`
			Depends yaml.Node `yaml:"depends_on"`
		} `yaml:"services"`
	}
	err = yaml.NewDecoder(io.LimitReader(f, 1024*1024)).Decode(&config)
	if err != nil {
		m.Error = "Unable to read Compose configuration: " + err.Error()
		return m
	}
	for key, v := range config.Services {
		name := v.Name
		if name == "" {
			name = key
		}
		m.specs = append(m.specs, model.DockerSnapshot{Service: key, Name: name, Image: v.Image, State: "unknown"})
		node := &v.Depends
		for node.Kind == yaml.AliasNode && node.Alias != nil {
			node = node.Alias
		}
		switch node.Kind {
		case yaml.SequenceNode:
			for _, n := range node.Content {
				m.deps[key] = append(m.deps[key], n.Value)
			}
		case yaml.MappingNode:
			for i := 0; i < len(node.Content); i += 2 {
				m.deps[key] = append(m.deps[key], node.Content[i].Value)
			}
		}
	}
	sort.Slice(m.specs, func(i, j int) bool { return m.specs[i].Service < m.specs[j].Service })
	return m
}
func (m *Manager) Specs() []model.DockerSnapshot { return append([]model.DockerSnapshot{}, m.specs...) }
func (m *Manager) validate(names []string) error {
	if m.Error != "" {
		return fmt.Errorf("%s", m.Error)
	}
	if m.File == "" {
		return fmt.Errorf("No Compose configuration in project root")
	}
	for _, name := range names {
		found := false
		for _, s := range m.specs {
			if name == s.Service {
				found = true
			}
		}
		if !found || strings.HasPrefix(name, "-") {
			return fmt.Errorf("Unknown Docker service %q", name)
		}
	}
	return nil
}
func (m *Manager) Action(ctx context.Context, action string, names []string) error {
	for !m.mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.validate(names); err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	args := m.args()
	switch action {
	case "start":
		args = append(args, "up", "-d", "--no-deps", "--no-build", "--wait", "--wait-timeout", "60")
	case "stop":
		args = append(args, "stop", "--timeout", "15")
	case "restart":
		stop := append(append([]string{}, args...), "stop", "--timeout", "15")
		stop = append(stop, names...)
		if _, err := m.run(ctx, stop...); err != nil {
			return err
		}
		args = append(args, "up", "-d", "--no-deps", "--no-build", "--wait", "--wait-timeout", "60")
	default:
		return fmt.Errorf("Unknown Docker action")
	}
	args = append(args, names...)
	_, err := m.run(ctx, args...)
	return err
}

// Dependencies are started explicitly in order; a single-container action never starts migrations implicitly.
func (m *Manager) Ensure(ctx context.Context, names []string) error {
	order, err := m.DependencyOrder(names)
	if err != nil {
		return err
	}
	for _, name := range order {
		if err := m.Action(ctx, "start", []string{name}); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) DependencyOrder(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if err := m.validate(names); err != nil {
		return nil, err
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	order := []string{}
	var visit func(string) error
	visit = func(n string) error {
		if visiting[n] {
			return fmt.Errorf("Docker dependency cycle: %s", n)
		}
		if done[n] {
			return nil
		}
		if err := m.validate([]string{n}); err != nil {
			return err
		}
		visiting[n] = true
		for _, dep := range m.deps[n] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[n] = false
		done[n] = true
		order = append(order, n)
		return nil
	}
	for _, n := range names {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return order, nil
}

type container struct {
	ID                           string
	Service, Name, State, Health string
	Publishers                   []struct {
		PublishedPort int
		URL           string
		TargetPort    int
		Protocol      string
	}
}

func parsePS(raw []byte) ([]container, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	rows := []container{}
	if raw[0] == '[' {
		err := json.Unmarshal(raw, &rows)
		return rows, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		var row container
		err := dec.Decode(&row)
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
}
func (m *Manager) Observe(ctx context.Context) ([]model.DockerSnapshot, string) {
	rows := m.Specs()
	if m.File == "" {
		return rows, ""
	}
	if m.Error != "" {
		return rows, m.Error
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	raw, err := m.run(ctx, append(m.args(), "ps", "--all", "--format", "json", "--orphans=false")...)
	if err != nil {
		return rows, err.Error()
	}
	cs, err := parsePS(raw)
	if err != nil {
		return rows, "Unable to parse Docker status: " + err.Error()
	}
	for i := range rows {
		rows[i].State = "absent"
		instances := 0
		for _, c := range cs {
			if c.Service != rows[i].Service {
				continue
			}
			instances++
			rows[i].PublishedEndpoints = nil
			rows[i].Name = c.Name
			rows[i].ID = c.ID
			rows[i].State = c.State
			rows[i].Health = c.Health
			ps := []string{}
			seenPorts := map[string]bool{}
			for _, p := range c.Publishers {
				address := net.ParseIP(p.URL)
				local := p.URL == "" || p.URL == "localhost" || address != nil && (address.IsLoopback() || address.IsUnspecified())
				if p.PublishedPort > 0 && p.Protocol == "tcp" && local {
					rows[i].PublishedEndpoints = append(rows[i].PublishedEndpoints, model.PublishedEndpoint{Host: p.URL, Port: p.PublishedPort})
				}
				label := fmt.Sprintf("%d→%d", p.PublishedPort, p.TargetPort)
				if !seenPorts[label] {
					ps = append(ps, label)
					seenPorts[label] = true
				}
			}
			rows[i].Ports = strings.Join(ps, ", ")
		}
		if instances > 1 {
			rows[i].PublishedEndpoints = nil
		}
	}
	return rows, ""
}

func (m *Manager) args() []string {
	a := []string{"compose", "-f", m.File}
	if m.Project != "" {
		a = append(a, "-p", m.Project)
	}
	return a
}
