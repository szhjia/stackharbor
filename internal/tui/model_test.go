package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/charmbracelet/x/ansi"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"strings"
	"testing"
)

type fakeController struct {
	snapshot model.Snapshot
	store    *logs.Store
	action   string
	ids      []model.ServiceID
	affected []model.ServiceID
}

func (f *fakeController) Start(_ context.Context, ids []model.ServiceID) error {
	f.action = "start"
	f.ids = ids
	return nil
}
func (f *fakeController) Stop(_ context.Context, ids []model.ServiceID) error {
	f.action = "stop"
	f.ids = ids
	return nil
}
func (f *fakeController) Restart(_ context.Context, ids []model.ServiceID) error {
	f.action = "restart"
	f.ids = ids
	return nil
}
func (f *fakeController) Affected([]model.ServiceID) []model.ServiceID { return f.affected }
func (f *fakeController) Snapshot() model.Snapshot                     { return f.snapshot }
func (f *fakeController) Logs() *logs.Store                            { return f.store }
func (f *fakeController) Shutdown(context.Context) error               { f.action = "shutdown"; return nil }
func fixture() *fakeController {
	a := model.Service{ID: "app/web", ProjectID: "app", Name: "网页服务"}
	b := model.Service{ID: "other/api", ProjectID: "other"}
	f := &fakeController{store: logs.NewStore(), snapshot: model.Snapshot{Root: "/项目", Projects: []model.Project{{ID: "app", Name: "学习工程", Services: []model.Service{a}}, {ID: "other", Name: "其他", Services: []model.Service{b}}}, Candidates: []model.Candidate{{ID: "pending", Name: "待注册"}}, Services: []model.ServiceSnapshot{{Spec: a, State: "running"}, {Spec: b, State: "stopped"}}}}
	f.store.Append(logs.Entry{ProjectID: "app", ServiceID: a.ID, Text: "正确日志 中文"})
	f.store.Append(logs.Entry{ProjectID: "other", ServiceID: b.ID, Text: "other-project-secret-marker"})
	return f
}
func key(m Model, k string) (Model, tea.Cmd) {
	v, c := m.Update(tea.KeyPressMsg{Code: []rune(k)[0], Text: k})
	return v.(Model), c
}
func TestDashboardFirstAndProjectLogFilter(t *testing.T) {
	f := fixture()
	m := NewModel(f)
	if !strings.Contains(m.View().Content, "Dashboard") {
		t.Fatal("missing dashboard")
	}
	m, _ = key(m, "j")
	v := m.View().Content
	if !strings.Contains(v, "正确日志") || strings.Contains(v, "other-project-secret-marker") {
		t.Fatal("wrong scope", v)
	}
	m, _ = key(m, "j")
	m, _ = key(m, "j")
	_, c := key(m, "s")
	if c != nil {
		c()
	}
	if f.action != "" {
		t.Fatal("unregistered candidate executed")
	}
}
func TestKeysActionsConfirmAndCancel(t *testing.T) {
	f := fixture()
	m := NewModel(f)
	m, _ = key(m, "j")
	f.affected = []model.ServiceID{"other/api"}
	m, c := key(m, "x")
	if c != nil || !m.confirm {
		t.Fatal("downstream stop not confirmed")
	}
	v, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = v.(Model)
	if m.confirm {
		t.Fatal("escape failed")
	}
	m, c = key(m, "s")
	if c == nil {
		t.Fatal("no start command")
	}
	c()
	if f.action != "start" {
		t.Fatal("start failed")
	}
	m, _ = key(m, "?")
	if !m.help {
		t.Fatal("help absent")
	}
	m, c = key(m, "q")
	if c == nil {
		t.Fatal("quit did not cleanup")
	}
	c()
	if f.action != "shutdown" {
		t.Fatal("quit skipped cleanup")
	}
}
func TestViewFitsCJKResizeNoColorAndTinyTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := NewModel(fixture())
	for _, size := range [][2]int{{120, 35}, {80, 24}, {60, 18}, {20, 5}} {
		v, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = v.(Model)
		s := m.View().Content
		if strings.Contains(s, "\x1b") {
			t.Fatal("NO_COLOR escape")
		}
		if len(strings.Split(s, "\n")) > size[1] {
			t.Fatal("height overflow")
		}
		for _, line := range strings.Split(s, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("width overflow", line)
			}
		}
		if !strings.Contains(m.View().Content, "—") && m.width >= 70 {
			t.Fatal("unknown metric not shown")
		}
	}
}
