package tui

import (
	"fmt"
	"strings"
)

func (m Model) dockerPanel() []string {
	out := []string{heading("Docker containers"), m.tone("↑/↓ navigate · ←/→ containers · s/x/r control", accent), ""}
	if m.snapshot.DockerFile == "" {
		return append(out, "No Compose configuration in project root")
	}
	if m.snapshot.DockerError != "" {
		out = append(out, m.tone(plain(m.snapshot.DockerError), amber))
	}
	out = append(out, m.tone(strings.Repeat("─", m.contentWidth()), muted))
	rowHeight := 3
	for _, row := range m.snapshot.Docker {
		if row.MetricError != "" {
			rowHeight = 4
		}
	}
	capacity := max(1, (m.bodyHeight()-len(out)-3)/rowHeight)
	start := max(0, m.dockerIndex-capacity+1)
	end := min(len(m.snapshot.Docker), start+capacity)
	for i := start; i < end; i++ {
		c := m.snapshot.Docker[i]
		state := dockerState(c.State, c.Health)
		name := plain(c.Service + " · " + c.Name)
		if i == m.dockerIndex {
			name = m.selectedRow("› "+name, m.contentWidth())
		} else {
			name = "  " + name
		}
		out = append(out, name, m.tone("  "+state+" · "+plain(c.Ports), muted))
		out = append(out, "  Memory "+memory(c.Metric)+" · CPU "+cpu(c.Metric))
		if c.MetricError != "" {
			out = append(out, m.tone("  "+plain(c.MetricError), amber))
		}
	}
	if len(m.snapshot.Docker) > capacity {
		out = append(out, m.tone(fmt.Sprintf("%d–%d / %d · ←/→ select", start+1, end, len(m.snapshot.Docker)), muted))
	}
	return append(out, "", m.tone("Actions affect selected container · global actions include declared dependencies", muted))
}

func dockerState(state, health string) string {
	label := map[string]string{"unknown": "Unknown", "absent": "Not created", "running": "Running", "exited": "Stopped", "created": "Created", "restarting": "Restarting", "paused": "Paused", "dead": "Failed"}[state]
	if label == "" {
		label = state
	}
	h := map[string]string{"healthy": "Healthy", "unhealthy": "Unhealthy", "starting": "Checking"}[health]
	if h != "" {
		label += " · " + h
	}
	return label
}
