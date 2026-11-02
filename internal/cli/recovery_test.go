package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

func TestReopenPromptChoices(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		key  rune
		want reopenChoice
	}{
		{name: "yes", text: "yes", key: tea.KeyEnter, want: reopenConfirm},
		{name: "uppercase", text: "Y", key: tea.KeyEnter, want: reopenConfirm},
		{name: "locate", text: "f", key: tea.KeyEnter, want: reopenLocate},
		{name: "default preserves", key: tea.KeyEnter, want: reopenCancel},
		{name: "no", text: "no", key: tea.KeyEnter, want: reopenCancel},
		{name: "invalid preserves", text: "maybe", key: tea.KeyEnter, want: reopenCancel},
		{name: "escape", text: "yes", key: tea.KeyEscape, want: reopenCancel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := reopenPrompt{}
			for _, r := range tc.text {
				next, cmd := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
				m = next.(reopenPrompt)
				if cmd != nil || m.finished {
					t.Fatal("typing a reply confirmed before Enter")
				}
			}
			next, cmd := m.Update(tea.KeyPressMsg{Code: tc.key})
			m = next.(reopenPrompt)
			if !m.finished || m.choice != tc.want || cmd == nil {
				t.Fatalf("choice=%v finished=%v command=%v", m.choice, m.finished, cmd != nil)
			}
		})
	}
}

func TestRecoveryRequiresTerminalAndPreservesOwner(t *testing.T) {
	h := controlHost(t, false)
	_, err := supervisor.Acquire(h.SessionInfo().Root)
	var conflict *supervisor.SessionConflict
	if !errors.As(err, &conflict) {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if reopen, code := recoverSession(context.Background(), conflict, strings.NewReader("yes\n"), &out, &errs); reopen || code != 2 {
		t.Fatalf("reopen=%v code=%d output=%s %s", reopen, code, &out, &errs)
	}
	if len(h.Coordinator().Operations()) != 0 {
		t.Fatal("nonterminal recovery closed the owner")
	}
}

func TestRecoveryRejectsChangedOwnerBeforeConfirmation(t *testing.T) {
	for _, field := range []string{"pid", "birth", "session", "socket", "protocol"} {
		t.Run(field, func(t *testing.T) {
			h := controlHost(t, false)
			expected := h.SessionInfo()
			switch field {
			case "pid":
				expected.PID++
			case "birth":
				expected.ProcessCreatedMillis++
			case "session":
				expected.SessionID = "previous-session"
			case "socket":
				expected.SocketPath += ".old"
			case "protocol":
				expected.ProtocolVersion++
			}
			confirmed := false
			interaction := controlInteraction{expected: &expected, confirm: func(io.Reader, io.Writer, string) bool {
				confirmed = true
				return true
			}}
			var out, errs bytes.Buffer
			code := runControlWithInteraction(context.Background(), "kill", []string{"--root", h.SessionInfo().Root, "--yes"}, strings.NewReader(""), &out, &errs, interaction)
			if code != 2 || !strings.Contains(errs.String(), "Workspace session changed") || confirmed || len(h.Coordinator().Operations()) != 0 {
				t.Fatalf("code=%d confirmed=%v output=%s %s", code, confirmed, &out, &errs)
			}
		})
	}
}
