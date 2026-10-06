package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/model"
)

func (m Model) pageInset() int { return min(2, max(0, (m.width-1)/2)) }
func (m Model) verticalInset() int {
	if m.height >= 4 {
		return 1
	}
	return 0
}
func (m Model) groupStatus(p model.Project) string {
	ids := map[model.ServiceID]bool{}
	for _, n := range p.Services {
		ids[n.ID] = true
	}
	subset := []model.ServiceSnapshot{}
	for _, v := range m.snapshot.Services {
		if ids[v.Spec.ID] {
			subset = append(subset, v)
		}
	}
	m.snapshot.Services = subset
	return m.projectStatus(p.ID)
}
func (m Model) pageWidth() int { return max(1, m.width-2*m.pageInset()) }
func (m Model) frame(lines []string) string {
	inset := strings.Repeat(" ", m.pageInset())
	out := []string{}
	if m.verticalInset() > 0 {
		out = append(out, strings.Repeat(" ", m.width))
	}
	for _, line := range lines[:min(len(lines), m.height-2*m.verticalInset())] {
		out = append(out, inset+fit(line, m.pageWidth())+inset)
	}
	if m.verticalInset() > 0 {
		out = append(out, strings.Repeat(" ", m.width))
	}
	return strings.Join(out, "\n")
}
func (m Model) bodyHeight() int {
	h := max(1, m.height-4-2*m.verticalInset())
	if m.status != "" && !m.confirm && !m.quitting {
		h = max(1, h-1)
	}
	return h
}
func (m Model) sidebarWidth() int {
	if m.pageWidth() < 76 {
		return 0
	}
	if m.pageWidth() < 96 {
		return 24
	}
	return 28
}
func (m Model) contentWidth() int {
	if w := m.sidebarWidth(); w > 0 {
		return m.pageWidth() - w - 3
	}
	return m.pageWidth()
}
func (m Model) header() string {
	brand := heading("StackHarbor")
	root := plain(filepath.Base(m.snapshot.Root))
	version := m.tone("v"+buildinfo.Version, muted)
	if m.pageWidth() >= 80 {
		root = fit(root, min(ansi.StringWidth(root), m.pageWidth()-ansi.StringWidth(brand)-ansi.StringWidth(version)-4))
		start := max(ansi.StringWidth(brand)+2, (m.pageWidth()-ansi.StringWidth(root))/2)
		return fit(brand, start) + fit(root, m.pageWidth()-start-ansi.StringWidth(version)) + version
	}
	return fit(brand+"  "+root, m.pageWidth())
}
func (m Model) sidebar(width, height int) []string {
	outerWidth := width
	inset := min(2, max(0, (width-1)/2))
	width -= 2 * inset
	verticalInset := 0
	if height >= 6 {
		verticalInset = 1
	}
	rows := []string{}
	focusEnd := 0
	add := func(index int, name, status string) {
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		label := "  " + name
		if (index == m.selected && !m.dockerView) || (index == -1 && m.dockerView) {
			label = "› " + name
		}
		if (index == m.selected && !m.dockerView) || (index == -1 && m.dockerView) {
			rows = append(rows, m.selectedRow(label, width))
		} else {
			rows = append(rows, heading(fit(label, width)))
		}
		if status != "" {
			line := "  " + status
			if (index == m.selected && !m.dockerView) || (index == -1 && m.dockerView) {
				line = m.selectedRow(line, width)
			}
			rows = append(rows, line)
		}
		if (index == m.selected && !m.dockerView) || (index == -1 && m.dockerView) {
			focusEnd = len(rows) - 1
		}
	}
	add(0, "Dashboard", "")
	for i, p := range m.snapshot.Projects {
		add(i+1, plain(p.Name), m.groupStatus(p))
		if i+1 == m.selected && m.service >= 0 && !m.dockerView {
			for j, n := range p.Services {
				label := "  " + serviceKey(n)
				state := "stopped"
				for _, v := range m.snapshot.Services {
					if v.Spec.ID == n.ID {
						state = v.State
					}
				}
				if j == m.service {
					rows = append(rows, m.selectedRow("› "+label, width))
					focusEnd = len(rows) - 1
				} else {
					rows = append(rows, m.tone(fit(label+" · "+stateLabel(state), width), muted))
				}
			}
		}
	}
	for i, c := range m.snapshot.Candidates {
		add(len(m.snapshot.Projects)+i+1, plain(c.Name), m.tone("- Unregistered", amber))
	}
	if m.snapshot.DockerFile != "" {
		add(-1, "Docker containers", m.tone(fmt.Sprintf("%d containers · ←/→ select", len(m.snapshot.Docker)), muted))
	}
	capacity := max(0, height-2*verticalInset)
	scrolling := len(rows) > capacity
	if scrolling && capacity > 0 {
		capacity--
	}
	start := max(0, focusEnd-capacity+1)
	end := min(len(rows), start+capacity)
	out := append([]string{}, rows[start:end]...)
	if scrolling {
		out = append(out, m.tone("↑/↓ more · Home dashboard", muted))
	}
	painted := make([]string, height)
	padding := strings.Repeat(" ", inset)
	for y := range painted {
		line := ""
		if i := y - verticalInset; i >= 0 && i < len(out) && y < height-verticalInset {
			line = padding + fit(out[i], width) + padding
		}
		painted[y] = m.sidebarBackground(line, outerWidth)
	}
	return painted
}

type shortcut struct{ key, label string }

func (m Model) shortcuts(items []shortcut, compact bool) string {
	parts := []string{}
	for i := 0; i < len(items); i++ {
		item := items[i]
		if compact {
			if item.key == "Shift+S" && i+2 < len(items) && items[i+1].key == "Shift+X" && items[i+2].key == "Shift+R" {
				item = shortcut{"Shift+S/X/R", "All"}
				i += 2
			}
			switch item.label {
			case "Projects/content":
				item.label = "Switch"
			}
		}
		pair := item.label + " " + m.keycap(item.key)
		parts = append(parts, pair)
	}
	gap := "   "
	if compact {
		gap = "  "
	}
	return strings.Join(parts, gap)
}
func (m Model) footer() string {
	items := []shortcut{}
	switch {
	case m.quitting:
		return fit(m.tone("Quitting…", muted), m.pageWidth())
	case m.confirm:
		items = []shortcut{{"y/Enter", "Confirm"}, {"n/Esc", "Cancel"}, {"q", "Quit"}}
	case m.help:
		items = []shortcut{{"Esc/?", "Back"}, {"q", "Quit"}}
	default:
		items = append(items, shortcut{"↑/↓", "Select"})
		if m.sidebarWidth() == 0 {
			items = append(items, shortcut{"Tab", "Projects/content"})
		}
		if m.dockerView {
			items = append(items, shortcut{"←/→", "Container"})
			items = append(items, shortcut{"s", "Start"}, shortcut{"x", "Stop"}, shortcut{"r", "Restart"})
		} else if m.selected > 0 && m.selected <= len(m.snapshot.Projects) {
			active, canStart, open := false, false, false
			taskOnly := len(m.ids()) == 1
			readonly := false
			for _, v := range m.snapshot.Services {
				if containsService(m.ids(), string(v.Spec.ID)) {
					taskOnly = taskOnly && v.Spec.Kind == "task"
					readonly = readonly || v.Spec.Control == "observe"
				}
			}
			ids := m.ids()
			for _, v := range m.snapshot.Services {
				if !containsService(ids, string(v.Spec.ID)) {
					continue
				}
				if (v.State == "stopped" || v.State == "failed" || v.State == "blocked" || v.State == "unknown") && len(v.Owned) == 0 {
					canStart = true
				} else {
					active = true
				}
				open = open || v.Spec.Open != ""
			}
			if taskOnly {
				items = append(items, shortcut{"s", "Check/run"})
			} else if canStart && !readonly {
				items = append(items, shortcut{"s", "Start"})
			}
			if active && !taskOnly && !readonly {
				items = append(items, shortcut{"x", "Stop"})
			}
			if !taskOnly && !readonly {
				items = append(items, shortcut{"r", "Restart"})
			}
			if open {
				items = append(items, shortcut{"o", "Open"})
			}
			items = append(items, shortcut{"i", "Details"})
		}
		items = append(items, shortcut{"Shift+S", "Start all"}, shortcut{"Shift+X", "Stop all"}, shortcut{"Shift+R", "Restart all"})
		if m.snapshot.DockerFile != "" && !m.dockerView {
			items = append(items, shortcut{"d", "Docker"})
		}
		items = append(items, shortcut{"?", "Help"}, shortcut{"q", "Quit"})
	}
	left := m.shortcuts(items, false)
	if ansi.StringWidth(left) > m.pageWidth() {
		left = m.shortcuts(items, true)
	}
	if ansi.StringWidth(left) > m.pageWidth() && !m.help && !m.confirm {
		essential := []shortcut{}
		for _, item := range items {
			if item.key == "↑/↓" || item.key == "i" || item.key == "o" {
				continue
			}
			essential = append(essential, item)
		}
		left = m.shortcuts(essential, true)
	}
	if ansi.StringWidth(left) > m.pageWidth() {
		// Keep navigation, scope switching and exit discoverable in tiny viewports.
		minimal := []shortcut{}
		if m.help {
			minimal = append(minimal, shortcut{"Esc", "Back"})
		} else if m.confirm {
			minimal = append(minimal, shortcut{"y", "Confirm"}, shortcut{"Esc", "Cancel"})
		} else {
			if m.sidebarWidth() == 0 {
				minimal = append(minimal, shortcut{"Tab", "Switch"})
			}
			minimal = append(minimal, shortcut{"?", "Help"})
		}
		minimal = append(minimal, shortcut{"q", "Quit"})
		left = m.shortcuts(minimal, true)
	}
	metrics := m.tone("StackHarbor memory "+memory(m.snapshot.Tool)+" · CPU "+cpu(m.snapshot.Tool), muted)
	if m.pageWidth() >= 100 && ansi.StringWidth(left)+ansi.StringWidth(metrics)+3 <= m.pageWidth() {
		return fit(left, m.pageWidth()-ansi.StringWidth(metrics)) + metrics
	}
	return fit(left, m.pageWidth())
}
func (m Model) render() string {
	if m.pageWidth() < 30 || m.height < 8 {
		return m.frame([]string{fit("StackHarbor", m.pageWidth()), fit("Resize · q quit", m.pageWidth())})
	}
	height := m.bodyHeight()
	lines := []string{}
	switch {
	case m.help:
		lines = []string{heading("Keyboard shortcuts"), "", "↑/↓ j/k select project or Docker · Home dashboard", "←/→ h/l select task, service or container · i details", "PageUp/PageDown log history or dashboard pages · End latest", "s start or check task · x stop · r restart service", "Failed tasks block dependents; retry checks prerequisites again", "Port conflicts: review PID and confirm release before starting", "Project metrics: service process trees; footer: StackHarbor", "Shift+S start all · Shift+X stop all", "Shift+R restart running set · Shift applies actions globally", "Docker: ←/→ containers · s/x/r controls (confirm) · d quick access", "o open service URL · Tab projects/content on narrow screens", "? / Esc back · q / Ctrl-C quit"}
	case m.confirm:
		label := map[string]string{"start": "Start", "stop": "Stop", "restart": "Restart", "all-start": "Start all", "all-stop": "Stop all", "all-restart": "Restart all", "docker-start": "Start Docker", "docker-stop": "Stop Docker", "docker-restart": "Restart Docker"}[m.action]
		lines = []string{heading("Confirm: " + label), "", "Selected scope: " + plain(fmt.Sprint(m.pending)), plain(m.status)}
		if strings.HasPrefix(m.action, "all-") {
			deps := []string{}
			seen := map[string]bool{}
			for _, v := range m.snapshot.Services {
				for _, dep := range v.Spec.DockerDependsOn {
					if !seen[dep] {
						seen[dep] = true
						deps = append(deps, dep)
					}
				}
			}
			if len(deps) > 0 {
				lines = append(lines, "Docker dependencies: "+plain(strings.Join(deps, ", ")))
			}
		}
		if strings.HasPrefix(m.action, "docker-") {
			lines = append(lines, "Stopping a dependency container first stops its local dependents in this session.")
			lines[2] = "Docker services: " + plain(strings.Join(m.pendingDocker, ", "))
		}
		if len(m.conflicts) > 0 {
			lines = []string{heading("Release conflicting ports · " + label), "", "These processes will be terminated before starting selected services:"}
			capacity := max(1, height-5)
			if m.planned != nil {
				lines = append(lines, "Launch scope: "+plain(fmt.Sprint(m.planned.Affected)))
				capacity = max(1, height-6)
			}
			page := min(m.confirmOffset, (len(m.conflicts)-1)/capacity)
			start := page * capacity
			end := min(len(m.conflicts), start+capacity)
			for _, c := range m.conflicts[start:end] {
				lines = append(lines, plain(fmt.Sprintf("Port %d · PID %d · %s", c.Port, c.Identity.PID, c.Command)))
			}
			lines = append(lines, "", fmt.Sprintf("%d–%d / %d · PgUp/PgDn view · y confirm", start+1, end, len(m.conflicts)))
		}
	case m.quitting:
		lines = []string{heading("Quitting"), "", plain(m.status), "Waiting up to 30 seconds."}
	default:
		right := m.dashboard()
		if m.dockerView {
			right = m.dockerPanel()
		} else if m.selected > 0 {
			right = m.project()
		}
		width := m.sidebarWidth()
		if width == 0 {
			lines = right
			if m.showList {
				lines = m.sidebar(m.pageWidth(), height)
			}
		} else {
			left := m.sidebar(width, height)
			for i := 0; i < height; i++ {
				a, b := "", ""
				if i < len(left) {
					a = left[i]
				}
				if i < len(right) {
					b = right[i]
				}
				lines = append(lines, a+" "+m.tone("│", muted)+" "+fit(b, m.contentWidth()))
			}
		}
	}
	out := []string{fit(m.header(), m.pageWidth()), m.tone(strings.Repeat("─", m.pageWidth()), muted)}
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		out = append(out, fit(line, m.pageWidth()))
	}
	if m.status != "" && !m.confirm && !m.quitting {
		out = append(out, fit(plain(m.status), m.pageWidth()))
	}
	out = append(out, m.tone(strings.Repeat("─", m.pageWidth()), muted), m.footer())
	return m.frame(out)
}
