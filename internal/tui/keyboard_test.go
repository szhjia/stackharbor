package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/szhjia/stackharbor/internal/model"
)

type allKeysController struct{ *fakeController }

func (f *allKeysController) AllAction(_ context.Context, action string) error {
	f.action = "all-" + action
	return nil
}

func TestShiftActionsUseAllScopeAndKeepConfirmation(t *testing.T) {
	for _, page := range []string{"overview", "project", "docker"} {
		for _, action := range []struct {
			letter rune
			name   string
		}{{'S', "start"}, {'X', "stop"}, {'R', "restart"}} {
			for _, input := range []struct {
				name string
				msg  tea.KeyPressMsg
			}{
				{"legacy", tea.KeyPressMsg{Code: action.letter, Text: string(action.letter)}},
				{"shift-text", tea.KeyPressMsg{Code: action.letter + ('a' - 'A'), Text: string(action.letter), Mod: tea.ModShift}},
				{"shift-code", tea.KeyPressMsg{Code: action.letter + ('a' - 'A'), Mod: tea.ModShift}},
			} {
				t.Run(page+"/"+action.name+"/"+input.name, func(t *testing.T) {
					f := &allKeysController{fixture()}
					for _, spec := range []model.Service{
						{ID: "app/task", Kind: "task"},
						{ID: "app/db", Kind: "resource"},
						{ID: "external/web", Control: "observe"},
					} {
						f.snapshot.Services = append(f.snapshot.Services, model.ServiceSnapshot{Spec: spec})
					}
					m := NewModel(f)
					if page == "project" {
						m.selected = 1
					}
					m.dockerView = page == "docker"
					v, cmd := m.Update(input.msg)
					m = v.(Model)
					if action.name != "start" {
						if !m.confirm || cmd != nil || m.action != "all-"+action.name {
							t.Fatalf("missing global confirmation: action=%s confirm=%v cmd=%v", m.action, m.confirm, cmd != nil)
						}
						if !reflect.DeepEqual(m.pending, []model.ServiceID{"app/web", "other/api"}) {
							t.Fatalf("wrong global targets: %v", m.pending)
						}
						m, cmd = key(m, "y")
					}
					if cmd == nil {
						t.Fatal("missing global command")
					}
					cmd()
					if f.action != "all-"+action.name {
						t.Fatalf("wrong action: %s", f.action)
					}
				})
			}
		}
	}
}

func TestLocalActionsKeepSelectedScopeAndOldAllKeysAreInactive(t *testing.T) {
	for _, input := range []struct{ key, action string }{{"s", "start"}, {"x", "stop"}, {"r", "restart"}, {"a", ""}, {"A", ""}} {
		t.Run(input.key, func(t *testing.T) {
			f := fixture()
			m := NewModel(f)
			m.selected = 1
			m, cmd := key(m, input.key)
			if cmd != nil {
				cmd()
			}
			if m.confirm || f.action != input.action {
				t.Fatalf("unexpected action: %s confirm=%v", f.action, m.confirm)
			}
			if input.action != "" && !reflect.DeepEqual(f.ids, []model.ServiceID{"app/web"}) {
				t.Fatalf("local shortcut changed scope: %v", f.ids)
			}
		})
	}
}

func TestShiftStopAndRestartCanBeCancelled(t *testing.T) {
	for _, input := range []string{"X", "R"} {
		f := &allKeysController{fixture()}
		m, cmd := key(NewModel(f), input)
		if !m.confirm || cmd != nil {
			t.Fatal("global action skipped confirmation")
		}
		v, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		if v.(Model).confirm || cmd != nil || f.action != "" {
			t.Fatal("cancel executed a global action")
		}
	}
}

func TestShiftShortcutsDiscoverableAcrossPagesAndWidths(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, width := range []int{80, 120, 180} {
		for _, page := range []string{"overview", "project", "docker"} {
			f := fixture()
			f.snapshot.DockerFile = "compose.yaml"
			m := resized(NewModel(f), width, 24)
			m.selected = 1
			if page == "overview" {
				m.selected = 0
			}
			m.dockerView = page == "docker"
			footer := m.footer()
			if !strings.Contains(footer, "Shift+S/X/R") {
				for _, input := range []string{"Shift+S", "Shift+X", "Shift+R"} {
					if !strings.Contains(footer, input) {
						t.Fatalf("%s at %d columns: missing %s: %s", page, width, input, footer)
					}
				}
			}
		}
	}
	m := NewModel(fixture())
	m.help = true
	for _, input := range []string{"Shift+S", "Shift+X", "Shift+R"} {
		if !strings.Contains(m.render(), input) {
			t.Fatalf("help missing %s", input)
		}
	}
}
