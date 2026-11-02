package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

type reopenChoice int

const (
	reopenCancel reopenChoice = iota
	reopenConfirm
	reopenLocate
)

// The small inline prompt uses the same cancellable terminal reader as the
// console. Waiting for Enter also keeps a typed "yes" from reaching the new TUI.
type reopenPrompt struct {
	reply    string
	choice   reopenChoice
	finished bool
}

func (m reopenPrompt) Init() tea.Cmd { return nil }
func (m reopenPrompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch k := key.String(); k {
		case "ctrl+c", "esc":
			m.finished = true
			return m, tea.Quit
		case "enter":
			switch strings.ToLower(m.reply) {
			case "y", "yes":
				m.choice = reopenConfirm
			case "f", "focus":
				m.choice = reopenLocate
			}
			m.finished = true
			return m, tea.Quit
		case "backspace", "ctrl+h":
			if len(m.reply) > 0 {
				m.reply = m.reply[:len(m.reply)-1]
			}
		default:
			if len(k) == 1 && (k[0] >= 'a' && k[0] <= 'z' || k[0] >= 'A' && k[0] <= 'Z') && len(m.reply) < 16 {
				m.reply += k
			}
		}
	}
	return m, nil
}
func (m reopenPrompt) View() tea.View {
	if m.finished {
		return tea.NewView("")
	}
	return tea.NewView("Close this session and reopen here? [y/N] (f: locate existing window): " + m.reply)
}

// recoverSession returns true only after the selected owner has successfully
// closed. It never retries a close or substitutes a later workspace owner.
func recoverSession(ctx context.Context, conflict *supervisor.SessionConflict, in io.Reader, out, errOut io.Writer) (bool, int) {
	fmt.Fprintln(errOut, conflict)
	if !terminal(in) || !terminal(out) {
		fmt.Fprintln(errOut, "Session recovery requires an interactive terminal")
		return false, 2
	}
	var choice reopenChoice
	var promptErr error
	interaction := controlInteraction{expected: &conflict.Session, confirm: func(in io.Reader, out io.Writer, _ string) bool {
		p := tea.NewProgram(reopenPrompt{}, tea.WithInput(in), tea.WithOutput(out), tea.WithContext(ctx), tea.WithoutSignalHandler())
		last, err := p.Run()
		promptErr = err
		if err == nil {
			choice = last.(reopenPrompt).choice
		}
		return err == nil && choice == reopenConfirm
	}}
	code := runControlWithInteraction(ctx, "kill", []string{"--root", conflict.Session.Root, "--timeout", "30s"}, in, out, errOut, interaction)
	if promptErr != nil {
		fmt.Fprintln(errOut, promptErr)
		return false, code
	}
	if choice == reopenLocate {
		return false, locateConflict(ctx, conflict, out, errOut)
	}
	return code == 0, code
}
