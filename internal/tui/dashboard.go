package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/szhjia/stackharbor/internal/model"
)

func memory(v model.Metric) string {
	if !v.Known {
		return "—"
	}
	suffix := ""
	if v.Partial {
		suffix = " (partial)"
	}
	return fmt.Sprintf("%.1f MiB%s", float64(v.RSS)/1048576, suffix)
}
func cpu(v model.Metric) string {
	if v.CPUPercent == nil {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", *v.CPUPercent)
}
func ports(v model.ServiceSnapshot) []model.PortObservation {
	if len(v.Ports) > 0 {
		return v.Ports
	}
	out := []model.PortObservation{}
	for _, p := range v.Spec.Ports {
		out = append(out, model.PortObservation{Port: p.Number, Status: "unknown"})
	}
	return out
}
func (m Model) portLabel(p model.PortObservation) string {
	label := map[string]string{"owned": "Session", "external": "External", "free": "Free", "unknown": "Unknown"}[p.Status]
	if label == "" {
		label = "Unknown"
	}
	text := fmt.Sprintf("%d %s", p.Port, label)
	switch p.Status {
	case "external":
		return m.tone(text, amber)
	case "owned":
		return m.tone(text, green)
	default:
		return m.tone(text, muted)
	}
}
func (m Model) portSummary(v model.ServiceSnapshot) string {
	labels := []string{}
	for _, p := range ports(v) {
		labels = append(labels, m.portLabel(p))
	}
	if len(labels) == 0 {
		return m.tone("—", muted)
	}
	return strings.Join(labels, " · ")
}
func hasExternal(v model.ServiceSnapshot) bool {
	for _, p := range ports(v) {
		if p.Status == "external" {
			return true
		}
	}
	return false
}
func (m Model) serviceName(v model.ServiceSnapshot) string {
	for _, p := range m.snapshot.Projects {
		if p.ID == v.Spec.ProjectID {
			name := plain(p.Name)
			if len(p.Services) > 1 {
				name += " / " + serviceKey(v.Spec)
			}
			return name
		}
	}
	if v.Spec.Name != "" {
		return plain(v.Spec.Name)
	}
	return plain(string(v.Spec.ID))
}
func (m Model) dashboardRows(v model.ServiceSnapshot) []string {
	w := m.contentWidth()
	if w >= 80 {
		return []string{fit(m.serviceName(v), w-67) + "  " + fit(m.stateBadge(v.State), 14) + "  " + fit(m.portSummary(v), 18) + "  " + fit(memory(v.Metric), 20) + "  " + fit(cpu(v.Metric), 7)}
	}
	if w >= 47 {
		return []string{fit(m.serviceName(v), w-36) + "  " + fit(m.stateBadge(v.State), 14) + "  " + fit(m.portSummary(v), 18)}
	}
	return []string{heading(m.serviceName(v)), "  " + m.stateBadge(v.State) + " · " + m.portSummary(v)}
}
func (m Model) dashboardHeading() []string {
	w := m.contentWidth()
	running, failed, total, tasks := 0, 0, 0, 0
	projects := map[string]bool{}
	external := map[int]bool{}
	unknownPorts := false
	for _, v := range m.snapshot.Services {
		if v.Spec.Kind != "task" && v.Spec.Kind != "resource" {
			total++
			projects[v.Spec.ProjectID] = true
		}
		if v.Spec.Kind == "task" {
			tasks++
		}
		if v.State == "running" || v.State == "started" {
			running++
		}
		if v.State == "failed" || v.State == "unready" || v.State == "blocked" || v.State == "unknown" {
			failed++
		}
		for _, p := range ports(v) {
			unknownPorts = unknownPorts || p.Status == "unknown" || p.Status == ""
			if p.Status == "external" {
				external[p.Port] = true
			}
		}
	}
	externalCount := fmt.Sprint(len(external))
	if unknownPorts {
		externalCount = "—"
		if len(external) > 0 {
			externalCount = fmt.Sprintf("%d+ (partial)", len(external))
		}
	}
	out := []string{heading("Dashboard"), "", fmt.Sprintf("%d projects · Session running %d/%d · External %s", len(projects), running, total, externalCount)}
	if ansi.StringWidth(out[2]) > w {
		out[2] = fmt.Sprintf("Session running %d/%d · External %s", running, total, externalCount)
	}
	if tasks > 0 {
		out = append(out, m.tone(fmt.Sprintf("%d prerequisite tasks · dependency order", tasks), muted))
	}
	if m.snapshot.DockerFile != "" {
		count := 0
		for _, d := range m.snapshot.Docker {
			if d.State == "running" {
				count++
			}
		}
		label := fmt.Sprintf("Docker running %d/%d · d containers", count, len(m.snapshot.Docker))
		if m.snapshot.DockerError != "" {
			label = "Docker status unknown · d details"
		}
		out = append(out, m.tone(label, accent))
	}
	for _, v := range m.snapshot.Services {
		if v.MetricSource == "external" {
			out = append(out, m.tone("Metrics include external process trees · read-only", muted))
			break
		}
	}
	if failed > 0 {
		out = append(out, m.tone(fmt.Sprintf("! %d nodes need attention", failed), amber))
	}
	for _, d := range m.snapshot.Diagnostics {
		out = append(out, m.tone(plain(d.Message), amber))
	}
	if len(m.snapshot.Services) == 0 {
		return append(out, "", "No registered services. Run stackharbor init.")
	}
	out = append(out, "")
	if w >= 80 {
		out = append(out, fit("Project", w-67)+"  "+fit("Session", 14)+"  "+fit("Port owner", 18)+"  "+fit("Memory", 20)+"  "+fit("CPU", 7))
	} else if w >= 47 {
		out = append(out, fit("Project", w-36)+"  "+fit("Session", 14)+"  "+fit("Port owner", 18))
	}
	out = append(out, m.tone(strings.Repeat("─", w), muted))
	return out
}
func (m Model) dashboardPageSize() int {
	rowHeight := 1
	if m.contentWidth() < 47 {
		rowHeight = 2
	}
	eventSpace := 0
	if m.bodyHeight() >= 18 {
		eventSpace = 4
	}
	capacity := max(1, (m.bodyHeight()-len(m.dashboardHeading())-eventSpace-1)/rowHeight)
	if len(m.snapshot.Services) > capacity {
		capacity = max(1, capacity-1)
	}
	return capacity
}
func (m Model) dashboard() []string {
	out := m.dashboardHeading()
	if len(m.snapshot.Services) == 0 {
		return out
	}
	capacity := m.dashboardPageSize()
	paged := len(m.snapshot.Services) > capacity
	start := min(max(0, m.dashboardOffset), max(0, len(m.snapshot.Services)-capacity))
	end := min(len(m.snapshot.Services), start+capacity)
	for _, v := range m.snapshot.Services[start:end] {
		out = append(out, m.dashboardRows(v)...)
	}
	if paged {
		out = append(out, m.tone(fmt.Sprintf("%d–%d / %d services · PgUp/PgDn pages", start+1, end, len(m.snapshot.Services)), muted))
	}
	if m.bodyHeight() >= 18 {
		out = append(out, "", m.tone(strings.Repeat("─", m.contentWidth()), muted), heading("Session events"))
		if len(m.snapshot.Events) == 0 {
			out = append(out, m.tone("No events in this session", muted))
		} else {
			remaining := max(1, m.bodyHeight()-len(out))
			for _, event := range m.snapshot.Events[max(0, len(m.snapshot.Events)-remaining):] {
				out = append(out, m.tone(event.Time.Format("15:04:05"), muted)+"  "+plain(string(event.ServiceID))+" · "+plain(event.Message))
			}
		}
	}
	return out
}
