package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"time"
)

func (m Model) navigationLast() int {
	last := len(m.snapshot.Projects) + len(m.snapshot.Candidates)
	if m.snapshot.DockerFile != "" {
		last++
	}
	return last
}

func (m Model) ids() []model.ServiceID {
	out := []model.ServiceID{}
	if m.selected == 0 {
		return out
	}
	if m.selected > len(m.snapshot.Projects) {
		return out
	}
	p := m.snapshot.Projects[m.selected-1]
	for i, v := range p.Services {
		if m.service < 0 || m.service == i {
			out = append(out, v.ID)
		}
	}
	return out
}

type takeoverController interface {
	Conflicts(context.Context, []model.ServiceID) ([]model.PortConflict, error)
	ResolveConflicts(context.Context, []model.ServiceID, []model.PortConflict, string) error
}
type dockerController interface {
	DockerAction(context.Context, string, []string) error
}
type allController interface {
	AllAction(context.Context, string) error
}

func (m Model) command(action string, ids []model.ServiceID) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 900*time.Second)
		defer cancel()
		var err error
		if (action == "start" || action == "restart" || action == "all-start" || action == "all-restart") && len(m.conflicts) == 0 {
			if c, ok := m.controller.(takeoverController); ok {
				targets, e := c.Conflicts(ctx, ids)
				if e != nil {
					return actionMsg{err: e}
				}
				if len(targets) > 0 {
					return actionMsg{action: action, ids: ids, conflicts: targets}
				}
			}
		}
		if len(m.conflicts) > 0 && action != "quit" {
			if c, ok := m.controller.(takeoverController); ok {
				err = c.ResolveConflicts(ctx, ids, m.conflicts, action)
			} else {
				err = fmt.Errorf("This session does not support port release")
			}
		} else if len(action) > 7 && action[:7] == "docker-" {
			if c, ok := m.controller.(dockerController); ok {
				err = c.DockerAction(ctx, action[7:], m.pendingDocker)
			} else {
				err = fmt.Errorf("This session cannot control Docker")
			}
		} else if len(action) > 4 && action[:4] == "all-" {
			if c, ok := m.controller.(allController); ok {
				err = c.AllAction(ctx, action[4:])
			} else {
				switch action[4:] {
				case "start":
					err = m.controller.Start(ctx, ids)
				case "stop":
					err = m.controller.Stop(ctx, ids)
				case "restart":
					err = m.controller.Restart(ctx, ids)
				}
			}
		} else {
			switch action {
			case "start":
				err = m.controller.Start(ctx, ids)
			case "stop":
				err = m.controller.Stop(ctx, ids)
			case "restart":
				err = m.controller.Restart(ctx, ids)
			case "quit":
				deadline, stop := context.WithTimeout(ctx, 30*time.Second)
				defer stop()
				err = m.controller.Shutdown(deadline)
			}
		}
		return actionMsg{err: err, quit: action == "quit"}
	}
}
func (m Model) key(k string) (tea.Model, tea.Cmd) {
	// Legacy terminals send uppercase text; enhanced protocols may report Shift.
	switch k {
	case "shift+s", "shift+S":
		k = "S"
	case "shift+x", "shift+X":
		k = "X"
	case "shift+r", "shift+R":
		k = "R"
	}
	if k == "q" || k == "ctrl+c" {
		if m.quitting {
			return m, nil
		}
		m.quitting = true
		m.help, m.confirm = false, false
		m.conflicts = nil
		m.status = "Stopping processes started in this session…"
		return m, m.command("quit", nil)
	}
	if m.quitting {
		return m, nil
	}
	if k == "esc" {
		m.help = false
		m.confirm = false
		m.details = false
		m.status = ""
		m.conflicts = nil
		return m, nil
	}
	if m.confirm {
		if k == "pgdown" {
			m.confirmOffset++
			return m, nil
		}
		if k == "pgup" {
			m.confirmOffset = max(0, m.confirmOffset-1)
			return m, nil
		}
		if k == "y" || k == "enter" {
			m.confirm = false
			m.busy = true
			return m, m.command(m.action, m.pending)
		}
		if k == "n" {
			m.confirm = false
			m.status = ""
			m.conflicts = nil
		}
		return m, nil
	}
	if k == "?" {
		m.help = !m.help
		return m, nil
	}
	if m.help {
		return m, nil
	}
	if k == "d" {
		if m.snapshot.DockerFile == "" {
			m.status = "No Compose configuration in project root"
			return m, nil
		}
		m.dockerView = !m.dockerView
		if m.dockerView {
			m.selected = m.navigationLast()
		} else {
			m.selected = 0
		}
		m.service = -1
		m.showList = false
		m.details = false
		m.status = ""
		return m, nil
	}
	if m.dockerView && (k == "left" || k == "h" || k == "right" || k == "l") {
		if k == "right" || k == "l" {
			m.dockerIndex = min(max(0, len(m.snapshot.Docker)-1), m.dockerIndex+1)
		} else {
			m.dockerIndex = max(0, m.dockerIndex-1)
		}
		m.status = ""
		return m, nil
	}
	switch k {
	case "j", "down":
		m.status = ""
		m.selected = min(m.selected+1, m.navigationLast())
		m.dockerView = m.snapshot.DockerFile != "" && m.selected == m.navigationLast()
		m.service = -1
		m.offset = 0
		m.details = false
	case "k", "up":
		m.status = ""
		if m.dockerView {
			m.selected = m.navigationLast()
		}
		m.selected = max(0, m.selected-1)
		m.dockerView = false
		m.service = -1
		m.offset = 0
		m.details = false
	case "home":
		m.status = ""
		m.selected = 0
		m.dockerView = false
		m.service = -1
		m.offset = 0
		m.dashboardOffset = 0
		m.details = false
	case "tab":
		if m.sidebarWidth() == 0 {
			m.showList = !m.showList
		}
	case "i":
		if m.selected > 0 && m.selected <= len(m.snapshot.Projects) {
			m.details = !m.details
		}
	case "right", "l":
		if m.selected > 0 && m.selected <= len(m.snapshot.Projects) {
			m.service++
			m.offset = 0
			m.status = ""
			if m.service >= len(m.snapshot.Projects[m.selected-1].Services) {
				m.service = -1
			}
		}
	case "left", "h":
		if m.selected == 0 || m.selected > len(m.snapshot.Projects) {
			return m, nil
		}
		m.service--
		m.offset = 0
		m.status = ""
		if m.service < -1 {
			m.service = len(m.snapshot.Projects[m.selected-1].Services) - 1
		}
	case "pgup":
		if m.selected == 0 {
			page := m.dashboardPageSize()
			start := min(m.dashboardOffset, max(0, len(m.snapshot.Services)-page))
			m.dashboardOffset = max(0, start-page)
		} else if m.selected <= len(m.snapshot.Projects) {
			m.offset = min(m.offset+max(1, m.bodyHeight()-8), max(0, len(m.controller.Logs().Entries(m.ids()))-1))
		}
	case "pgdown":
		if m.selected == 0 {
			page := m.dashboardPageSize()
			limit := max(0, len(m.snapshot.Services)-page)
			m.dashboardOffset = min(limit, min(m.dashboardOffset, limit)+page)
		} else {
			m.offset = max(0, m.offset-max(1, m.bodyHeight()-8))
		}
	case "end":
		m.offset = 0
		if m.selected == 0 {
			m.dashboardOffset = max(0, len(m.snapshot.Services)-m.dashboardPageSize())
		}
	case "s", "x", "r", "S", "X", "R":
		if m.busy {
			m.status = "Operation in progress…"
			return m, nil
		}
		m.conflicts = nil
		if m.dockerView && (k == "s" || k == "x" || k == "r") {
			if len(m.snapshot.Docker) == 0 {
				m.status = "No available Compose services in project root"
				return m, nil
			}
			m.dockerIndex = min(m.dockerIndex, len(m.snapshot.Docker)-1)
			m.pendingDocker = []string{m.snapshot.Docker[m.dockerIndex].Service}
			m.action = "docker-start"
			if k == "x" {
				m.action = "docker-stop"
			}
			if k == "r" {
				m.action = "docker-restart"
			}
			m.confirm = true
			m.confirmOffset = 0
			m.status = "Docker services: " + m.pendingDocker[0] + " · container data preserved"
			return m, nil
		}
		if m.selected == 0 && (k == "s" || k == "x" || k == "r") {
			m.status = "Select a project; Shift+S start all, Shift+X stop all, Shift+R restart all"
			return m, nil
		}
		ids := m.ids()
		if m.service < 0 {
			ids = defaultTargets(m.snapshot, ids)
		}
		if k == "S" || k == "X" || k == "R" {
			ids = nil
			for _, v := range m.snapshot.Services {
				if v.Spec.Kind != "task" && v.Spec.Kind != "resource" && v.Spec.Control != "observe" {
					ids = append(ids, v.Spec.ID)
				}
			}
		}
		if len(ids) == 0 {
			m.status = "Register project first: stackharbor init --project <directory>"
			return m, nil
		}
		for _, id := range ids {
			for _, v := range m.snapshot.Services {
				if v.Spec.ID == id && v.Spec.Control == "observe" {
					m.status = "External services are observed read-only; start, stop and restart are disabled"
					return m, nil
				}
				if v.Spec.ID == id && v.Spec.Kind == "task" && (k == "x" || k == "r") {
					m.status = "Use s to check/run tasks again; completed results are not rolled back"
					return m, nil
				}
			}
		}
		action := "start"
		if k == "x" || k == "X" {
			action = "stop"
		}
		if k == "r" || k == "R" {
			action = "restart"
		}
		if k == "S" || k == "X" || k == "R" {
			action = "all-" + action
		}
		affected := m.controller.Affected(ids)
		if k == "X" || k == "R" || (action != "start" && action != "all-start" && len(affected) > 0) {
			m.confirm = true
			m.confirmOffset = 0
			m.action = action
			m.pending = ids
			m.status = fmt.Sprintf("Affected dependents: %v", affected)
			if k == "X" || k == "R" {
				m.status = "Scope: all local services + declared Docker dependencies; container data preserved"
				for _, v := range m.snapshot.Services {
					if v.Spec.Version == 2 {
						m.status = "Scope: all managed application services; persistent containers keep running"
						break
					}
				}
			}
			return m, nil
		}
		m.busy = true
		m.status = "Operation in progress…"
		return m, m.command(action, ids)
	case "o":
		if m.busy {
			return m, nil
		}
		for _, id := range m.ids() {
			for _, v := range m.snapshot.Services {
				if v.Spec.ID == id && v.Spec.Open != "" {
					url := v.Spec.Open
					return m, func() tea.Msg { return actionMsg{err: openURL(url)} }
				}
			}
		}
		m.status = "No open URL configured for this service"
	}
	return m, nil
}

func defaultTargets(s model.Snapshot, ids []model.ServiceID) []model.ServiceID {
	out := []model.ServiceID{}
	for _, id := range ids {
		for _, v := range s.Services {
			if v.Spec.ID == id && v.Spec.Kind != "task" && v.Spec.Kind != "resource" && v.Spec.Control != "observe" {
				out = append(out, id)
			}
		}
	}
	if len(out) == 0 {
		return ids
	}
	return out
}
