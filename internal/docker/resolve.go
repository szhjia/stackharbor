package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
	"os/exec"
	"sort"
	"time"
)

type ComposeDependency struct {
	Condition string `json:"condition"`
	Required  bool   `json:"required"`
	Restart   bool   `json:"restart"`
}
type ComposeService struct {
	Name, Image    string
	HasHealthcheck bool
	Dependencies   map[string]ComposeDependency
}
type ResolvedCompose struct {
	Digest   string
	Services map[string]ComposeService
}
type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Len() int      { return b.buffer.Len() }
func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }
func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.limit - b.Len()
	if len(p) > left {
		b.exceeded = true
		p = p[:left]
	}
	b.buffer.Write(p)
	return n, nil
}
func checkComposeSyntax(files []string) error {
	for _, file := range files {
		f, e := os.Open(file)
		if e != nil {
			return errors.New("Cannot read Compose input")
		}
		b, e := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
		f.Close()
		if e != nil || len(b) > 8*1024*1024 {
			return errors.New("Compose input exceeds limit")
		}
		var doc struct {
			Include  yaml.Node `yaml:"include"`
			Services map[string]struct {
				Profiles yaml.Node `yaml:"profiles"`
				Extends  yaml.Node `yaml:"extends"`
			} `yaml:"services"`
		}
		if yaml.Unmarshal(b, &doc) != nil {
			return errors.New("Invalid Compose YAML")
		}
		if doc.Include.Kind != 0 {
			return errors.New("Compose include/profiles/extends not yet supported")
		}
		for _, s := range doc.Services {
			if s.Profiles.Kind != 0 || s.Extends.Kind != 0 {
				return errors.New("Compose include/profiles/extends not yet supported")
			}
		}
	}
	return nil
}
func Resolve(ctx context.Context, s ComposeScope, env []string) (*ResolvedCompose, error) {
	if len(s.Files) == 0 {
		return nil, errors.New("Compose files required")
	}
	if e := checkComposeSyntax(s.Files); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", append(s.Args(), "config", "--format", "json")...)
	cmd.Dir = s.ProjectDirectory
	cmd.Env = append([]string{}, env...)
	cmd.WaitDelay = time.Second
	out := &limitedBuffer{limit: 8 * 1024 * 1024}
	stderr := &limitedBuffer{limit: 64 * 1024}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if e := cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Compose configuration resolution timed out or cancelled")
		}
		if errors.Is(e, exec.ErrNotFound) {
			return nil, errors.New("Docker CLI unavailable; Compose plugin required")
		}
		return nil, errors.New("Compose configuration resolution failed; check plugin, input files and environment")
	}
	if out.exceeded || stderr.exceeded {
		return nil, errors.New("Compose configuration output exceeds limit")
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(out.Bytes(), &raw) != nil || raw == nil {
		return nil, errors.New("Invalid Compose configuration JSON")
	}
	var doc struct {
		Services map[string]struct {
			Name    string                     `json:"container_name"`
			Image   string                     `json:"image"`
			Depends map[string]json.RawMessage `json:"depends_on"`
			Health  struct {
				Disable bool     `json:"disable"`
				Test    []string `json:"test"`
			} `json:"healthcheck"`
		} `json:"services"`
	}
	if json.Unmarshal(out.Bytes(), &doc) != nil || len(doc.Services) == 0 {
		return nil, errors.New("Compose configuration contains no valid services")
	}
	var full any
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	decoder.UseNumber()
	if decoder.Decode(&full) != nil {
		return nil, errors.New("Invalid Compose configuration JSON")
	}
	canonical, _ := json.Marshal(full)
	r := &ResolvedCompose{Digest: fmt.Sprintf("%x", sha256.Sum256(canonical)), Services: map[string]ComposeService{}}
	for name, s := range doc.Services {
		v := ComposeService{Name: s.Name, Image: s.Image, HasHealthcheck: !s.Health.Disable && len(s.Health.Test) > 0 && s.Health.Test[0] != "NONE", Dependencies: map[string]ComposeDependency{}}
		for dep, b := range s.Depends {
			edge := ComposeDependency{Condition: "service_started", Required: true}
			d := json.NewDecoder(bytes.NewReader(b))
			d.DisallowUnknownFields()
			if d.Decode(&edge) != nil {
				return nil, errors.New("Invalid Compose dependency contract")
			}
			v.Dependencies[dep] = edge
		}
		r.Services[name] = v
	}
	return r, nil
}
func NewResolved(s ComposeScope, env []string, r *ResolvedCompose) *Manager {
	s.Files = append([]string{}, s.Files...)
	s.EnvFiles = append([]string{}, s.EnvFiles...)
	m := &Manager{root: s.ProjectDirectory, Project: s.Project, scope: &s, env: append([]string{}, env...), resolved: r, deps: map[string][]string{}}
	if len(s.Files) > 0 {
		m.File = s.Files[0]
	}
	m.inputDigests = map[string][32]byte{}
	for _, file := range append(append([]string{}, s.Files...), s.EnvFiles...) {
		digest, err := composeInputDigest(file)
		if err != nil {
			m.Error = "Cannot freeze Compose input"
		} else {
			m.inputDigests[file] = digest
		}
	}
	m.run = func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Dir = s.ProjectDirectory
		cmd.Env = append([]string{}, m.env...)
		cmd.WaitDelay = time.Second
		out := &limitedBuffer{limit: 8 * 1024 * 1024}
		stderr := &limitedBuffer{limit: 64 * 1024}
		cmd.Stdout = out
		cmd.Stderr = stderr
		if e := cmd.Run(); e != nil {
			return nil, errors.New("Docker action failed; verify Docker readiness and configuration")
		}
		if out.exceeded || stderr.exceeded {
			return nil, errors.New("Docker output exceeds limit")
		}
		return out.Bytes(), nil
	}
	for name, v := range r.Services {
		display := v.Name
		if display == "" {
			display = name
		}
		m.specs = append(m.specs, model.DockerSnapshot{Service: name, Name: display, Image: v.Image, State: "unknown"})
		for dep := range v.Dependencies {
			m.deps[name] = append(m.deps[name], dep)
		}
		sort.Strings(m.deps[name])
	}
	sort.Slice(m.specs, func(i, j int) bool { return m.specs[i].Service < m.specs[j].Service })
	return m
}
func (m *Manager) ScopeKey() string {
	if m.scope != nil {
		return m.scope.Key()
	}
	return m.File + "\x00" + m.Project
}
func (m *Manager) CheckResolved(ctx context.Context) error {
	if m.scope == nil {
		return nil
	}
	if m.Error != "" {
		return errors.New(m.Error)
	}
	for file, digest := range m.inputDigests {
		now, err := composeInputDigest(file)
		if err != nil || now != digest {
			return errors.New("Compose input changed; reopen the session")
		}
	}
	r, e := Resolve(ctx, *m.scope, m.env)
	if e != nil {
		return e
	}
	if r.Digest != m.resolved.Digest {
		return errors.New("Compose configuration changed; reopen the session")
	}
	return nil
}
func (m *Manager) ConfigDigest() string {
	if m.resolved == nil {
		return ""
	}
	return m.resolved.Digest
}

func composeInputDigest(file string) ([32]byte, error) {
	f, e := os.Open(file)
	if e != nil {
		return [32]byte{}, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	if e != nil || len(b) > 8*1024*1024 {
		return [32]byte{}, errors.New("Cannot read bounded Compose input")
	}
	return sha256.Sum256(b), nil
}
