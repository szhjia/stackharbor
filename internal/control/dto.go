package control

import (
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"sort"
	"strings"
)

// ProjectSnapshot projects an internal observation, never its configuration.
// The session publisher assigns Revision and may replace ObservedAt with its
// observation timestamp. Process metrics use separate physical samples.
func ProjectSnapshot(identity Identity, snapshot model.Snapshot) Snapshot {
	identity.Capabilities = append([]string{}, identity.Capabilities...)
	out := Snapshot{Identity: identity, ObservedAt: snapshot.ObservedAt.UTC(), Nodes: []Node{}, Processes: []Process{}, Containers: []Container{}, Diagnostics: []Diagnostic{}}
	redact := snapshotRedactor(snapshot)
	processes := map[string]bool{}
	for _, service := range snapshot.Services {
		spec := service.Spec
		kind := spec.Kind
		if kind == "" {
			kind = "service"
		}
		ownership := "session"
		readOnly := spec.Control == "observe" || spec.Resource != nil && spec.Resource.Control == "observe"
		if readOnly {
			ownership = "external"
		}
		node := Node{ID: string(spec.ID), ProjectID: spec.ProjectID, Name: redact(spec.Name), Kind: kind, State: service.State, Reason: redact(service.Reason), Ownership: ownership, AllowedActions: nodeActions(service, readOnly), ResourceRefs: []string{}, Metric: projectMetric(service.Metric), MetricSource: service.MetricSource, MetricError: redact(service.MetricError), Ports: []Port{}}
		node.DependsOn = []string{}
		for _, id := range spec.DependsOn {
			node.DependsOn = appendUnique(node.DependsOn, string(id))
		}
		for _, edge := range spec.Requires {
			node.DependsOn = appendUnique(node.DependsOn, string(edge.Node))
		}
		sort.Strings(node.DependsOn)
		ownedPIDs := map[int32]string{}
		processMetrics := map[model.ProcessIdentity]model.Metric{}
		ownedSet := map[model.ProcessIdentity]bool{}
		for _, p := range service.Owned {
			ownedSet[p] = true
		}
		for _, sample := range service.Processes {
			p := sample.Identity
			processMetrics[p] = sample.Metric
			if p.PID <= 0 || p.CreatedMillis <= 0 {
				continue
			}
			ref := fmt.Sprintf("process:%d:%d", p.PID, p.CreatedMillis)
			ownedPIDs[p.PID] = ref
			owner := "external"
			if ownedSet[p] {
				owner = ownership
			}
			if !processes[ref] {
				created := p.CreatedMillis
				out.Processes = append(out.Processes, Process{ID: ref, PID: p.PID, CreatedUnixMillis: &created, Ownership: owner, Metric: projectMetric(sample.Metric)})
				processes[ref] = true
			}
			node.ResourceRefs = appendUnique(node.ResourceRefs, ref)
		}
		for i, owned := range service.Owned {
			var created *int64
			ref := fmt.Sprintf("process:%s:owned:%d", spec.ID, i)
			if owned.CreatedMillis > 0 {
				value := owned.CreatedMillis
				created = &value
				ref = fmt.Sprintf("process:%d:%d", owned.PID, owned.CreatedMillis)
			}
			ownedPIDs[owned.PID] = ref
			if !processes[ref] {
				out.Processes = append(out.Processes, Process{ID: ref, PID: owned.PID, CreatedUnixMillis: created, Ownership: ownership, Metric: projectMetric(processMetrics[owned])})
				processes[ref] = true
			}
			node.ResourceRefs = appendUnique(node.ResourceRefs, ref)
		}
		for _, port := range service.Ports {
			row := Port{Port: port.Port, Status: port.Status, Reason: redact(port.Reason), ResourceRefs: []string{}}
			for i, listener := range port.Listeners {
				ref := ownedPIDs[listener.PID]
				if ref == "" {
					// A PID without a birth time does not establish a shared process identity.
					ref = fmt.Sprintf("process:%s:port:%d:listener:%d", spec.ID, port.Port, i)
					out.Processes = append(out.Processes, Process{ID: ref, PID: listener.PID, Ownership: "external"})
				}
				row.ResourceRefs = appendUnique(row.ResourceRefs, ref)
				node.ResourceRefs = appendUnique(node.ResourceRefs, ref)
			}
			node.Ports = append(node.Ports, row)
		}
		for _, container := range snapshot.Docker {
			if container.Service == string(spec.ID) && container.ID != "" {
				node.ResourceRefs = appendUnique(node.ResourceRefs, containerRef(container))
			}
		}
		out.Nodes = append(out.Nodes, node)
	}
	for _, container := range snapshot.Docker {
		endpoints := []PublishedEndpoint{}
		for _, endpoint := range container.PublishedEndpoints {
			endpoints = append(endpoints, PublishedEndpoint{Host: endpoint.Host, Port: endpoint.Port})
		}
		ref := ""
		if container.ID != "" {
			ref = containerRef(container)
		}
		out.Containers = append(out.Containers, Container{EndpointIdentity: container.EndpointIdentity, ID: container.ID, ResourceRef: ref, ServiceID: container.Service, Name: redact(container.Name), Image: redact(container.Image), State: container.State, Health: container.Health, Reason: redact(container.Reason), PublishedEndpoints: endpoints, Metric: projectMetric(container.Metric), MetricError: redact(container.MetricError)})
	}
	for _, d := range snapshot.Diagnostics {
		out.Diagnostics = append(out.Diagnostics, Diagnostic{Severity: d.Severity, Code: d.Code, File: redact(d.File), Field: redact(d.Field), Message: redact(d.Message), Line: d.Line, Column: d.Column})
	}
	if snapshot.DockerError != "" {
		out.Diagnostics = append(out.Diagnostics, Diagnostic{Severity: "error", Code: "docker_observation", Message: redact(snapshot.DockerError)})
	}
	return out
}

func projectMetric(metric model.Metric) Metric {
	out := Metric{Known: metric.Known, Partial: metric.Partial}
	if !metric.SampledAt.IsZero() {
		at := metric.SampledAt.UTC()
		out.SampledAt = &at
	}
	if metric.CPUPercent != nil {
		cpu := *metric.CPUPercent
		out.CPUPercent = &cpu
	}
	if !metric.Known {
		return out
	}
	rss := metric.RSS
	uptime := metric.Uptime.Milliseconds()
	out.RSSBytes = &rss
	out.UptimeMillis = &uptime
	return out
}

func nodeActions(service model.ServiceSnapshot, readOnly bool) []string {
	if readOnly {
		return []string{}
	}
	if service.Spec.Kind == "resource" {
		return []string{"start", "stop", "restart"}
	}
	release := false
	for _, port := range service.Ports {
		release = release || port.Status == "external"
	}
	withRelease := func(actions []string) []string {
		if release {
			return append(actions, "release")
		}
		return actions
	}
	if service.Spec.Kind == "task" {
		switch service.State {
		case "waiting", "starting", "checking", "running-task", "verifying", "stopping":
			return withRelease([]string{"stop"})
		default:
			return withRelease([]string{"start"})
		}
	}
	actions := []string{"start", "restart"}
	if len(service.Owned) > 0 || service.State == "waiting" || service.State == "starting" {
		actions = append(actions, "stop")
	}
	return withRelease(actions)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// Scrub known configuration values from diagnostic text as well as withholding
// the configuration itself. This is not a claim to detect arbitrary secrets.
func snapshotRedactor(snapshot model.Snapshot) func(string) string {
	values := map[string]bool{}
	addCommand := func(command []string) {
		if len(command) > 1 {
			values[strings.Join(command, " ")] = true
			for _, arg := range command[1:] {
				if arg != "" {
					values[arg] = true
				}
				// An error may echo just the value of a configured sensitive
				// option, rather than the complete --option=value argument.
				option, value, assigned := strings.Cut(arg, "=")
				if !assigned || value == "" || !strings.HasPrefix(option, "-") {
					continue
				}
				option = strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(option))
				for _, sensitive := range []string{"password", "passwd", "secret", "token", "credential", "apikey", "accesskey", "privatekey"} {
					if strings.Contains(option, sensitive) {
						values[value] = true
						break
					}
				}
			}
		}
	}
	addSpec := func(spec model.Service) {
		for _, value := range spec.Env {
			if value != "" {
				values[value] = true
			}
		}
		addCommand(spec.Command)
		if spec.Identity != nil {
			addCommand(spec.Identity.Command)
		}
		if spec.Task != nil {
			if spec.Task.Check != nil {
				addCommand(spec.Task.Check.Command)
			}
			if spec.Task.Verify != nil {
				addCommand(spec.Task.Verify.Command)
			}
		}
	}
	for _, service := range snapshot.Services {
		addSpec(service.Spec)
	}
	for _, project := range snapshot.Projects {
		for _, spec := range project.Services {
			addSpec(spec)
		}
	}
	for _, candidate := range snapshot.Candidates {
		addCommand(candidate.SuggestedCommand)
	}
	secrets := make([]string, 0, len(values))
	for value := range values {
		secrets = append(secrets, value)
	}
	sort.Slice(secrets, func(i, j int) bool {
		if len(secrets[i]) == len(secrets[j]) {
			return secrets[i] < secrets[j]
		}
		return len(secrets[i]) > len(secrets[j])
	})
	return func(text string) string {
		for _, secret := range secrets {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
		return text
	}
}

func containerRef(c model.DockerSnapshot) string {
	if c.EndpointIdentity != "" {
		return "container:" + c.EndpointIdentity + ":" + c.ID
	}
	return "container:" + c.ID
}
