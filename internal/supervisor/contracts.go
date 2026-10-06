package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"github.com/szhjia/stackharbor/internal/docker"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func newOperationID() string {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		return fmt.Sprint(time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// Caller holds session.mu; conditions are evaluated against current observations.
func dependencySatisfied(e *entry, condition string) bool {
	switch condition {
	case "succeeded":
		return e.state == "succeeded"
	case "available":
		return e.state == "available"
	case "ready":
		return e.state == "running" && e.spec.Ready != nil
	case "started":
		return e.state == "started" || e.state == "running" || e.handle != nil && e.state == "starting"
	default:
		return e.state == "running" || e.state == "started" || e.state == "succeeded" || e.state == "available"
	}
}
func resourceAvailable(spec *model.ResourceSpec, rows []model.DockerSnapshot) (bool, string) {
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.Service == spec.Service {
			return row.State == "running" && (spec.Available == "running" || row.Health == "healthy"), row.ID
		}
	}
	return false, ""
}
func (s *Session) freezeInputs() error {
	paths := append([]string{}, s.workspace.InputFiles...)
	for _, p := range s.workspace.Projects {
		if p.SourceFile != "" {
			paths = append(paths, p.SourceFile)
		}
	}
	for _, n := range s.workspace.Services() {
		if n.SourceFile != "" {
			paths = append(paths, n.SourceFile)
		}
		paths = append(paths, n.InputFiles...)
	}
	for _, path := range paths {
		digest, e := inputDigest(path)
		if e != nil {
			return fmt.Errorf("Unable to freeze configuration input %s", path)
		}
		s.inputDigests[path] = digest
	}
	return nil
}
func (s *Session) checkInputs() error {
	for path, digest := range s.inputDigests {
		now, e := inputDigest(path)
		if e != nil || now != digest {
			return fmt.Errorf("Configuration input changed; reopen the session: %s", path)
		}
	}
	return nil
}
func inputDigest(path string) (string, error) {
	st, e := os.Lstat(path)
	if e != nil {
		return "", e
	}
	if st.Mode().IsRegular() {
		if st.Size() > 4*1024*1024 {
			return "", fmt.Errorf("input exceeds 4 MiB")
		}
		b, e := os.ReadFile(path)
		return fmt.Sprintf("%x", sha256.Sum256(b)), e
	}
	if !st.IsDir() {
		return "", fmt.Errorf("input is not regular")
	}
	files := []string{}
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if p != path && (d.Name() == "__pycache__" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink input unsupported")
		}
		files = append(files, p)
		if len(files) > 1000 {
			return fmt.Errorf("input exceeds 1000 files")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	var rows strings.Builder
	for _, file := range files {
		digest, e := inputDigest(file)
		if e != nil {
			return "", e
		}
		rows.WriteString(file)
		rows.WriteByte(0)
		rows.WriteString(digest)
		rows.WriteByte(0)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rows.String()))), nil
}

func (s *Session) sampleContracts(ctx context.Context, gens map[model.ServiceID]uint64, ports map[model.ServiceID][]model.PortObservation, owned map[model.ServiceID][]model.ProcessIdentity) map[model.ServiceID]model.DockerSnapshot {
	type result struct {
		available        bool
		identity, reason string
	}
	results := map[model.ServiceID]result{}
	cache := map[string][]model.DockerSnapshot{}
	reasons := map[string]string{}
	services := map[string]map[string]bool{}
	managers := map[string]*docker.Manager{}
	for id, m := range s.resources {
		key := m.File + "\x00" + m.Project
		managers[key] = m
		if services[key] == nil {
			services[key] = map[string]bool{}
		}
		services[key][s.entries[id].spec.Resource.Service] = true
	}
	specs := map[model.ServiceID]model.Service{}
	for _, spec := range s.workspace.Services() {
		specs[spec.ID] = spec
	}
	allRows := []model.DockerSnapshot{}
	for key, m := range managers {
		cache[key], reasons[key] = m.Observe(ctx)
		if reasons[key] == "" {
			allRows = append(allRows, cache[key]...)
		}
	}
	external := externalDockerServices(specs, ports, owned, allRows)
	for key, m := range managers {
		selected := []model.DockerSnapshot{}
		for _, row := range cache[key] {
			include := services[key][row.Service]
			for _, match := range external {
				include = include || match.ID == row.ID
			}
			if include {
				selected = append(selected, row)
			}
		}
		cache[key] = m.SampleMetrics(ctx, selected)
		for _, row := range cache[key] {
			for id, match := range external {
				if match.ID == row.ID {
					external[id] = row
				}
			}
		}
	}
	for id, m := range s.resources {
		key := m.File + "\x00" + m.Project
		n := s.entries[id].spec
		available, identity := resourceAvailable(n.Resource, cache[key])
		reason := reasons[key]
		if !available && reason == "" {
			reason = "Container not running, not healthy yet, or health unknown"
		}
		results[id] = result{available, identity, reason}
	}
	observed := map[model.ServiceID][]model.ProcessIdentity{}
	observedErrors := map[model.ServiceID]error{}
	for id, e := range s.entries {
		if e.spec.Control != "observe" {
			continue
		}
		ids, err := observedIdentity(ctx, s.ports, e.spec)
		if err == nil {
			err = observe.CheckHealth(ctx, *e.spec.Ready)
		}
		observed[id] = ids
		observedErrors[id] = err
	}
	if ctx.Err() != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.resources) > 0 {
		s.dockerRows = nil
		s.dockerError = ""
		for _, n := range s.workspace.Services() {
			if n.Resource == nil {
				continue
			}
			key := n.Resource.File + "\x00" + n.Resource.Project
			for _, row := range cache[key] {
				if row.Service == n.Resource.Service {
					row.Service = string(n.ID)
					row.Name = n.Name
					s.dockerRows = append(s.dockerRows, row)
				}
			}
			if reasons[key] != "" {
				s.dockerError = reasons[key]
			}
		}
	}
	for id, r := range results {
		e := s.entries[id]
		manager := s.resources[id]
		key := manager.File + "\x00" + manager.Project
		if e.gen == gens[id] && reasons[key] == "" {
			instances, running, known := physicalResourceObservation(e.spec.Resource.Service, cache[key])
			// Running evidence remains meaningful even if daemon/container identity is unknown.
			e.resourceRunning = e.resourceRunning || running
			if known {
				if e.resourceInstances == nil {
					e.resourceInstances = instances
				}
				e.resourceRunning = running
			}
		}

		if e.gen != gens[id] || e.state == "waiting" || e.state == "starting" || e.state == "stopping" {
			continue
		}
		state, reason := "available", "Resource available"
		if !r.available {
			state = "unavailable"
			reason = r.reason
		}
		if r.available && e.resourceIdentity != "" && r.identity != e.resourceIdentity {
			state = "unknown"
			reason = "Container instance changed; check dependencies again"
		}
		if e.state == "unknown" && r.available {
			state = "unknown"
			reason = e.reason
		}
		if e.resourceIdentity == "" {
			e.resourceIdentity = r.identity
		}
		if e.state != state {
			e.state = state
			e.reason = reason
			s.event(id, state, reason)
		}
	}
	for id, ids := range observed {
		e := s.entries[id]
		if e.gen != gens[id] || e.state == "stopped" || e.state == "waiting" || e.state == "starting" {
			continue
		}
		err := observedErrors[id]
		if err == nil && fmt.Sprint(ids) != fmt.Sprint(e.observed) {
			err = fmt.Errorf("External process identity changed; verification required")
		}
		state, reason := "running", "External service · read-only dependency"
		if err != nil {
			state = "unready"
			reason = err.Error()
		}
		if e.state != state {
			e.state = state
			e.reason = reason
			s.event(id, state, reason)
		}
	}
	return external
}

// Physical instances are retained independently from the logical readiness
// selection. Empty non-nil is a verified absence; nil means identity unknown.
func physicalResourceObservation(service string, rows []model.DockerSnapshot) ([]string, bool, bool) {
	instances := []string{}
	running := false
	matched := false
	known := true
	for _, row := range rows {
		if row.Service != service {
			continue
		}
		matched = true
		running = running || row.State == "running" || row.State == "restarting" || row.State == "paused"
		if row.ID == "" {
			if row.State != "absent" {
				known = false
			}
			continue
		}
		if row.EndpointIdentity == "" {
			known = false
			continue
		}
		instances = append(instances, row.EndpointIdentity+":"+row.ID)
	}
	if !matched || !known {
		return nil, running, false
	}
	sort.Strings(instances)
	unique := instances[:0]
	for _, id := range instances {
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	return unique, running, true
}
func sameResourceInstances(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (s *Session) rememberResourceObservation(id model.ServiceID, gen uint64, service string, rows []model.DockerSnapshot, reason string, replace bool) {
	if reason != "" {
		return
	}
	instances, running, known := physicalResourceObservation(service, rows)
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[id]; e.gen == gen {
		e.resourceRunning = e.resourceRunning || running
		if !known {
			return
		}
		if replace || e.resourceInstances == nil {
			e.resourceInstances = instances
		}
		e.resourceRunning = running
		if replace {
			e.resourceMutation = false
		}
	}
}
