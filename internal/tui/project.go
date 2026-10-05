package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/szhjia/stackharbor/internal/model"
)

func serviceKey(s model.Service) string {
	if s.Key != "" {
		return plain(s.Key)
	}
	if s.Name != "" {
		return plain(s.Name)
	}
	return plain(string(s.ID))
}
func containsService(ids []model.ServiceID, id string) bool {
	for _, v := range ids {
		if string(v) == id {
			return true
		}
	}
	return false
}
func (m Model) project() []string {
	if m.selected > len(m.snapshot.Projects) {
		c := m.snapshot.Candidates[m.selected-len(m.snapshot.Projects)-1]
		return []string{heading(plain(c.Name)), m.tone("- Unregistered", amber), "", plain(c.Cwd), "Suggested command: " + plain(strings.Join(c.SuggestedCommand, " ")), "", "Run stackharbor init --project <directory>", "Review the YAML, then reopen the session."}
	}
	p := m.snapshot.Projects[m.selected-1]
	ids := m.ids()
	title := plain(p.Name)
	if len(ids) == 1 {
		for _, s := range p.Services {
			if s.ID == ids[0] {
				title += " / " + serviceKey(s)
			}
		}
	}
	out := []string{heading(title), ""}
	metadata := []string{}
	external, active, unknownPorts := false, false, false
	for _, v := range m.snapshot.Services {
		if !containsService(ids, string(v.Spec.ID)) {
			continue
		}
		external = external || hasExternal(v)
		for _, port := range ports(v) {
			unknownPorts = unknownPorts || port.Status == "unknown" || port.Status == ""
		}
		active = active || (v.State != "stopped" && v.State != "failed")
		summary := m.stateBadge(v.State)
		if v.Spec.Kind != "task" && v.Spec.Kind != "resource" {
			summary += "   " + m.portSummary(v)
		}
		if len(ids) > 1 {
			summary = serviceKey(v.Spec) + " · " + summary
		}
		resources := "Memory " + memory(v.Metric) + " · CPU " + cpu(v.Metric)
		if v.Spec.Kind == "resource" || v.Spec.Kind == "task" && len(v.Owned) == 0 {
			resources = ""
		}
		if ansi.StringWidth(summary)+ansi.StringWidth(resources)+3 <= m.contentWidth() {
			summary += "   " + resources
			metadata = append(metadata, summary)
		} else {
			metadata = append(metadata, summary)
			if v.Metric.Known || v.Metric.CPUPercent != nil {
				metadata = append(metadata, m.tone(resources, muted))
			}
		}
		if v.MetricSource == "external" {
			metadata = append(metadata, m.tone("Metrics from external process trees · read-only", muted))
		}
		if v.MetricSource == "forwarder" {
			metadata = append(metadata, m.tone("Docker / VM forwarding · excluded from project metrics", muted))
		}
		if v.Reason != "" {
			metadata = append(metadata, m.tone(plain(v.Reason), amber))
		}
		for _, dep := range v.Spec.DependsOn {
			for _, other := range m.snapshot.Services {
				if other.Spec.ID == dep && other.State != "running" && other.State != "started" && other.State != "succeeded" && other.State != "available" {
					metadata = append(metadata, m.tone("Dependency "+plain(string(dep))+" · "+stateLabel(other.State), amber))
				}
			}
		}
		for _, dep := range v.Spec.DockerDependsOn {
			label := "Unknown"
			for _, d := range m.snapshot.Docker {
				if d.Service == dep {
					label = dockerState(d.State, d.Health)
				}
			}
			metadata = append(metadata, m.tone("Docker dependency "+plain(dep)+" · "+label+" · d manage", accent))
		}
		if (v.State == "running" || v.State == "started") && v.Spec.Ready == nil && len(ids) == 1 {
			metadata = append(metadata, m.tone("Process alive · no readiness probe", muted))
		}
		if m.details {
			for _, port := range ports(v) {
				for _, listener := range port.Listeners {
					metadata = append(metadata, m.tone(fmt.Sprintf("PID %d · %s", listener.PID, plain(listener.Command)), muted))
				}
				if port.Reason != "" {
					metadata = append(metadata, plain(port.Reason))
				}
			}
			if v.Spec.SourceFile != "" {
				metadata = append(metadata, m.tone("Config "+plain(v.Spec.SourceFile), muted))
			}
			metadata = append(metadata, m.tone("cwd "+plain(v.Spec.Cwd), muted), m.tone("$ "+plain(strings.Join(v.Spec.Command, " ")), muted))
		}
	}
	limit := max(1, m.bodyHeight()/3)
	if m.details {
		limit = max(1, m.bodyHeight()/2)
	}
	if len(metadata) > limit {
		out = append(out, metadata[:max(0, limit-1)]...)
		out = append(out, m.tone("More: ←/→ select service · i details", muted))
	} else {
		out = append(out, metadata...)
	}
	if len(p.Services) > 1 {
		scope := "Project services"
		if m.service >= 0 {
			scope = serviceKey(p.Services[m.service])
		}
		out = append(out, m.tone("Scope [ "+scope+" ] · ←/→ switch node", accent))
	}
	logLabel := "Logs · live"
	entries := m.controller.Logs().Entries(ids)
	if len(ids) == 0 {
		entries = nil
	}
	count := max(1, m.bodyHeight()-len(out)-4)
	offset := min(m.offset, max(0, len(entries)-count))
	if offset > 0 {
		logLabel = "Logs · history"
	}
	dropped := uint64(0)
	for _, id := range ids {
		dropped += m.controller.Logs().Dropped(id)
	}
	if dropped > 0 {
		logLabel += fmt.Sprintf(" · %d older lines dropped", dropped)
	}
	out = append(out, "", heading(logLabel), m.tone(strings.Repeat("─", m.contentWidth()), muted))
	count = max(1, m.bodyHeight()-len(out)-1)
	end := max(0, len(entries)-offset)
	start := max(0, end-count)
	for _, v := range entries[start:end] {
		stream := ""
		if v.Stream != "" {
			stream = " " + plain(v.Stream)
		}
		out = append(out, m.tone(v.Time.Format("15:04:05"), muted)+"  "+m.tone("["+plain(string(v.ServiceID))+stream+"]", accent)+"  "+plain(v.Text))
	}
	if len(entries) == 0 {
		switch {
		case external:
			out = append(out, m.tone("External process holds port · s / r to review and release", amber))
		case active:
			out = append(out, m.tone("Waiting for session output…", muted))
		case unknownPorts:
			out = append(out, m.tone("No session logs · port status unknown", muted))
		default:
			out = append(out, m.tone("Not started in this session · s to start selected scope", muted))
		}
	}
	for len(out) < m.bodyHeight()-1 {
		out = append(out, "")
	}
	history := "PgUp/PgDn history · End latest"
	if offset > 0 {
		history = fmt.Sprintf("Log history · %d lines behind · End latest", offset)
	}
	out = append(out, m.tone(history, muted))
	return out
}

func (m Model) aggregateState(project string) string {
	priority := map[string]int{"stopped": 0, "running": 1, "started": 1, "unavailable": 6, "unhealthy": 6, "waiting": 2, "starting": 3, "stopping": 4, "unready": 5, "failed": 8, "blocked": 7, "unknown": 9, "succeeded": 1, "available": 1, "checking": 3, "verifying": 3, "running-task": 3}
	best := "stopped"
	run, total := 0, 0
	for _, v := range m.snapshot.Services {
		if v.Spec.ProjectID == project {
			if v.Spec.Kind == "service" || v.Spec.Kind == "" {
				total++
				if v.State == "running" || v.State == "started" {
					run++
				}
			}
			if priority[v.State] > priority[best] {
				best = v.State
			}
		}
	}
	// Completed prerequisites do not replace the current service status.
	if total > 0 && priority[best] <= 1 {
		if run == total {
			return "running"
		}
		if run > 0 {
			return "partial"
		}
		return "stopped"
	}
	return best
}
func (m Model) aggregate(project string) string {
	state := m.aggregateState(project)
	if state == "partial" {
		return "Partly running"
	}
	return stateLabel(state)
}
func (m Model) projectStatus(project string) string {
	state := m.aggregateState(project)
	if state == "stopped" {
		for _, v := range m.snapshot.Services {
			if v.Spec.ProjectID == project {
				for _, p := range ports(v) {
					if p.Status == "external" {
						return m.tone(fmt.Sprintf("◐ External :%d", p.Port), amber)
					}
				}
			}
		}
		return m.tone("- Not started", muted)
	}
	if state == "running" {
		return m.tone("● Session running", green)
	}
	if state == "partial" {
		return m.tone("◐ Partly running", amber)
	}
	return m.stateBadge(state)
}
