package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"io"
	"time"
)

type Controller interface {
	Start(context.Context, []model.ServiceID) error
	Stop(context.Context, []model.ServiceID) error
	Restart(context.Context, []model.ServiceID) error
	Affected([]model.ServiceID) []model.ServiceID
	Snapshot() model.Snapshot
	Logs() *logs.Store
	Shutdown(context.Context) error
}
type Model struct {
	controller                                 Controller
	snapshot                                   model.Snapshot
	width, height, selected, service, offset   int
	dashboardOffset                            int
	confirmOffset                              int
	dockerView                                 bool
	lightTheme                                 bool
	dockerIndex                                int
	busy                                       bool
	pendingDocker                              []string
	conflicts                                  []model.PortConflict
	help, confirm, quitting, showList, details bool
	action, status                             string
	pending                                    []model.ServiceID
	err                                        error
}
type tickMsg time.Time
type actionMsg struct {
	err       error
	quit      bool
	action    string
	ids       []model.ServiceID
	conflicts []model.PortConflict
}

func NewModel(c Controller) Model {
	return Model{controller: c, snapshot: c.Snapshot(), width: 80, height: 24, service: -1}
}
func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}
func (m Model) Init() tea.Cmd { return tea.Batch(tick(), tea.RequestBackgroundColor) }
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.BackgroundColorMsg:
		m.lightTheme = !v.IsDark()
	case tea.WindowSizeMsg:
		m.width = max(1, v.Width)
		m.height = max(1, v.Height)
	case tickMsg:
		m.snapshot = m.controller.Snapshot()
		return m, tick()
	case actionMsg:
		m.busy = false
		m.snapshot = m.controller.Snapshot()
		if len(v.conflicts) > 0 && !m.quitting {
			m.confirm = true
			m.confirmOffset = 0
			m.action = v.action
			m.pending = v.ids
			m.conflicts = v.conflicts
			m.status = "Confirm port release, then start selected scope"
			return m, nil
		}
		m.conflicts = nil
		if m.quitting && !v.quit {
			return m, nil
		}
		if v.quit {
			m.err = v.err
		}
		if v.err != nil {
			m.status = v.err.Error()
		} else {
			m.status = "Operation complete"
		}
		if v.quit {
			return m, tea.Quit
		}
	case tea.KeyPressMsg:
		return m.key(v.String())
	}
	return m, nil
}
func (m Model) View() tea.View { v := tea.NewView(m.render()); v.AltScreen = true; return v }
func Run(ctx context.Context, c Controller, in io.Reader, out io.Writer) error {
	p := tea.NewProgram(NewModel(c), tea.WithInput(in), tea.WithOutput(out), tea.WithContext(ctx), tea.WithoutSignalHandler())
	last, err := p.Run()
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cleanup := c.Shutdown(shutdown)
	if cleanup != nil {
		return cleanup
	}
	if v, ok := last.(Model); ok && v.quitting && v.err != nil {
		return v.err
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}
