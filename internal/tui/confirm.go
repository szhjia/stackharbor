package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/szhjia/stackharbor/internal/model"
)

type confirmationItem struct {
	id, name, ports, project, kind string
}

// The retained plan is the single source for the list the user authorizes.
func (m Model) confirmationItems() []confirmationItem {
	ids := m.pending
	if m.planned != nil {
		ids = m.planned.Affected
	} else if strings.HasPrefix(m.action, "docker-") {
		ids = nil
		for _, id := range m.pendingDocker {
			ids = append(ids, model.ServiceID(id))
		}
	} else if m.action == "stop" || m.action == "restart" {
		ids = append(append([]model.ServiceID{}, ids...), m.controller.Affected(ids)...)
	}
	items := []confirmationItem{}
	seen := map[model.ServiceID]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		item := confirmationItem{id: plain(string(id)), name: "—", ports: "—", kind: "service"}
		if strings.HasPrefix(m.action, "docker-") {
			for _, requested := range m.pendingDocker {
				if string(id) == requested {
					item.kind = "resource"
				}
			}
		}
		item.project, _, _ = strings.Cut(string(id), "/")
		dockerService := string(id)
		for _, v := range m.snapshot.Services {
			if v.Spec.ID != id {
				continue
			}
			item.name = m.serviceName(v)
			if v.Spec.Name != "" && v.Spec.Name != v.Spec.Key {
				item.name = plain(v.Spec.Name)
				for _, project := range m.snapshot.Projects {
					if project.ID == v.Spec.ProjectID && project.Name != "" && project.Name != v.Spec.Name {
						item.name = plain(project.Name) + " / " + item.name
						break
					}
				}
			}
			if v.Spec.ProjectID != "" {
				item.project = v.Spec.ProjectID
			}
			if v.Spec.Kind == "task" || v.Spec.Kind == "resource" {
				item.kind = v.Spec.Kind
			}
			numbers := []string{}
			for _, port := range v.Spec.Ports {
				numbers = append(numbers, fmt.Sprint(port.Number))
			}
			if len(numbers) > 0 {
				item.ports = strings.Join(numbers, ", ")
			}
			if v.Spec.Resource != nil {
				dockerService = v.Spec.Resource.Service
			}
			break
		}
		for _, d := range m.snapshot.Docker {
			if d.Service != dockerService || (item.kind != "resource" && item.name != "—") {
				continue
			}
			item.kind = "resource"
			if item.name == "—" {
				item.name = plain(d.Name)
				if item.name == "" {
					item.name = plain(d.Service)
				}
			}
			if d.Ports != "" {
				item.ports = plain(d.Ports)
			}
			break
		}
		items = append(items, item)
	}
	return items
}

func (m Model) confirmationContent() (string, []string) {
	items := m.confirmationItems()
	action := strings.TrimPrefix(strings.TrimPrefix(m.action, "all-"), "docker-")
	verb := map[string]string{"start": "Start", "stop": "Stop", "restart": "Restart"}[action]
	counts := map[string]int{}
	for _, item := range items {
		counts[item.kind]++
	}
	parts := []string{}
	for _, kind := range []string{"service", "resource", "task"} {
		if n := counts[kind]; n > 0 {
			name := map[string]string{"service": "service", "resource": "container", "task": "task"}[kind]
			if n != 1 {
				name += "s"
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, name))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "0 services")
	}
	title := "Confirm: " + verb + " " + strings.Join(parts, " · ")
	lines := []string{}
	prose := func(text string) {
		lines = append(lines, strings.Split(ansi.Wrap(plain(text), m.pageWidth(), ""), "\n")...)
	}
	if len(m.conflicts) > 0 {
		title = "Release conflicting ports · " + verb + " " + strings.Join(parts, " · ")
		prose("These processes will be terminated before starting selected services:")
		for _, c := range m.conflicts {
			prose(fmt.Sprintf("Port %d · PID %d · %s", c.Port, c.Identity.PID, c.Command))
		}
		lines = append(lines, "")
	}
	nameWidth, pathWidth, portWidth := 4, 4, 5
	for _, item := range items {
		nameWidth = max(nameWidth, ansi.StringWidth(item.name))
		pathWidth = max(pathWidth, ansi.StringWidth(item.id))
		portWidth = max(portWidth, ansi.StringWidth(item.ports))
	}
	table := nameWidth+pathWidth+portWidth+4 <= m.pageWidth()
	if table {
		lines = append(lines, m.tone(fit("Name", nameWidth)+"  "+fit("Path", pathWidth)+"  Ports", muted))
	}
	colors := [...]string{accent, green, amber, purple}
	assigned := map[string]string{}
	for i, item := range items {
		color, ok := assigned[item.project]
		if !ok {
			color = colors[len(assigned)%len(colors)]
			assigned[item.project] = color
		}
		if table {
			lines = append(lines, fit(item.name, nameWidth)+"  "+m.tone(fit(item.id, pathWidth), color)+"  "+item.ports)
			continue
		}
		if i > 0 {
			lines = append(lines, "")
		}
		prose(item.name + " · Ports " + item.ports)
		for _, row := range strings.Split(ansi.Hardwrap(item.id, max(1, m.pageWidth()-2), true), "\n") {
			lines = append(lines, "  "+m.tone(row, color))
		}
	}
	return title, lines
}

func (m Model) confirmationPageSize(rows int) int {
	height := m.bodyHeight()
	header := 2
	if height < 4 {
		header = max(0, height-2)
	}
	if header+rows <= height {
		return max(1, rows)
	}
	return max(1, height-header-1) // reserve the page indicator
}

func (m Model) confirmation() []string {
	title, body := m.confirmationContent()
	height := m.bodyHeight()
	lines := []string{}
	if height >= 3 {
		lines = append(lines, heading(title))
	}
	if height >= 4 {
		lines = append(lines, "")
	}
	capacity := m.confirmationPageSize(len(body))
	page := min(max(0, m.confirmOffset), max(0, (len(body)-1)/capacity))
	start := page * capacity
	end := min(len(body), start+capacity)
	lines = append(lines, body[start:end]...)
	if len(body) > capacity {
		lines = append(lines, m.tone(fmt.Sprintf("Page %d/%d · PgUp/PgDn", page+1, (len(body)+capacity-1)/capacity), muted))
	}
	return lines
}
