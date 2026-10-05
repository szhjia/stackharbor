package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
)

func designFixture() *fakeController {
	f := &fakeController{store: logs.NewStore()}
	f.snapshot.Root = "/workspace/easy_study_pipeline"
	for i, name := range []string{"Admin console", "PageSmith backend", "PDForge Studio", "Study app"} {
		id := []string{"admin", "backend", "studio", "study"}[i]
		service := model.Service{ID: model.ServiceID(id + "/dev"), ProjectID: id, Key: "dev", Cwd: "/private/config/" + id, Command: []string{"run-server"}, Ports: []model.Port{{Number: 5600 + i}}}
		status := "free"
		if i == 1 || i == 2 {
			status = "external"
		}
		f.snapshot.Projects = append(f.snapshot.Projects, model.Project{ID: id, Name: name, Services: []model.Service{service}})
		f.snapshot.Services = append(f.snapshot.Services, model.ServiceSnapshot{Spec: service, State: "stopped", Ports: []model.PortObservation{{Port: 5600 + i, Status: status, Listeners: []model.Listener{{PID: 123, Command: "server"}}}}})
	}
	return f
}

func resized(m Model, w, h int) Model {
	v, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return v.(Model)
}

func TestDesignReadableWorkspaceAndContextFooter(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, width := range []int{80, 120} {
		m := resized(NewModel(designFixture()), width, 24)
		view := m.View().Content
		for _, name := range []string{"Admin console", "PageSmith backend", "PDForge Studio", "Study app"} {
			if !strings.Contains(view, name) {
				t.Errorf("%d columns: project name missing: %s\n%s", width, name, view)
			}
		}
		for _, text := range []string{"Dashboard", "Session", "External", "Port owner"} {
			if !strings.Contains(view, text) {
				t.Errorf("missing %q", text)
			}
		}
		lines := strings.Split(view, "\n")
		footer := lines[len(lines)-2]
		if !strings.Contains(footer, "Shift+S") || !(strings.Contains(footer, "Shift+X") || strings.Contains(footer, "Shift+S/X/R")) || !strings.Contains(footer, "q") || strings.Contains(footer, "Start s") || strings.Contains(footer, "Stop x") || strings.Contains(footer, "Restart r") || strings.Contains(footer, "Details i") {
			t.Errorf("incorrect Dashboard commands: %s", footer)
		}
		if strings.Contains(view, "Quitting stops services started in this session") {
			t.Fatal("removed footer copy returned")
		}
	}
}

func TestDesignExternalListenerExplainsEmptyLogs(t *testing.T) {
	m := NewModel(designFixture())
	m.selected = 2
	view := m.View().Content
	if !strings.Contains(view, "External") || strings.Contains(view, "s to start") || !strings.Contains(view, "External process") {
		t.Fatalf("misleading external listener empty state:\n%s", view)
	}
	if strings.Contains(view, "/private/config") || strings.Contains(view, "run-server") || strings.Contains(view, "switch service") {
		t.Fatalf("single-service log view contains secondary configuration or redundant scope:\n%s", view)
	}
	m, _ = key(m, "i")
	view = m.View().Content
	if !strings.Contains(view, "/private/config/backend") || !strings.Contains(view, "run-server") || !strings.Contains(view, "PID 123") {
		t.Fatalf("expanded service detail lost configuration or listener ownership:\n%s", view)
	}
}

func TestDesignNarrowDashboardCanReachLastService(t *testing.T) {
	f := designFixture()
	for i := 0; i < 20; i++ {
		s := model.Service{ID: model.ServiceID(fmt.Sprintf("many/s%d", i)), ProjectID: "many", Key: fmt.Sprint(i)}
		f.snapshot.Services = append(f.snapshot.Services, model.ServiceSnapshot{Spec: s, State: "stopped"})
	}
	f.snapshot.Services[len(f.snapshot.Services)-1].Spec.Name = "LAST_SERVICE"
	m := resized(NewModel(f), 60, 18)
	for i := 0; i < 30; i++ {
		v, _ := m.key("pgdown")
		m = v.(Model)
	}
	if !strings.Contains(m.View().Content, "LAST_SERVICE") {
		t.Fatalf("last service unreachable on narrow Dashboard:\n%s", m.View().Content)
	}
	footer := strings.Split(m.View().Content, "\n")[16]
	if !strings.Contains(footer, "Tab") || !strings.Contains(footer, "q") {
		t.Fatalf("narrow commands hidden: %s", footer)
	}
}

func TestDesignAllStatesFitTerminalAndPreserveLogs(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	f := designFixture()
	f.snapshot.Projects[0].Name = "学习工程中文长名称服务工作区"
	for i := 0; i < 60; i++ {
		f.store.Append(logs.Entry{ServiceID: "admin/dev", Text: fmt.Sprintf("LOG_%02d 中文", i)})
	}
	for _, size := range [][2]int{{120, 35}, {100, 24}, {80, 24}, {60, 18}, {30, 8}, {20, 5}} {
		for _, state := range []string{"dashboard", "logs", "details", "help", "confirm"} {
			m := resized(NewModel(f), size[0], size[1])
			switch state {
			case "logs", "details":
				m.selected = 1
				if state == "details" {
					m, _ = key(m, "i")
				}
			case "help":
				m.help = true
			case "confirm":
				m.confirm, m.action, m.pending = true, "stop", []model.ServiceID{"admin/dev"}
			}
			view := m.View().Content
			lines := strings.Split(view, "\n")
			if len(lines) > size[1] || strings.Contains(view, "\x1b") {
				t.Fatalf("invalid %s at %v", state, size)
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("overflow %s at %v: %s", state, size, line)
				}
			}
			if state == "logs" && size[1] >= 18 && !strings.Contains(view, "LOG_59") {
				t.Fatalf("latest log clipped at %v:\n%s", size, view)
			}
		}
	}
}

func TestDesignLogHistoryAndServiceScope(t *testing.T) {
	f := designFixture()
	second := model.Service{ID: "admin/worker", ProjectID: "admin", Key: "worker"}
	f.snapshot.Projects[0].Services = append(f.snapshot.Projects[0].Services, second)
	f.snapshot.Services = append(f.snapshot.Services, model.ServiceSnapshot{Spec: second, State: "running"})
	for i := 0; i < 50; i++ {
		f.store.Append(logs.Entry{ServiceID: "admin/dev", Text: fmt.Sprintf("DEV_%02d", i)})
	}
	f.store.Append(logs.Entry{ServiceID: second.ID, Text: "WORKER_ONLY"})
	m := NewModel(f)
	m.selected, m.service = 1, 0
	for i := 0; i < 20; i++ {
		v, _ := m.key("pgup")
		m = v.(Model)
	}
	view := m.View().Content
	if !strings.Contains(view, "DEV_00") || strings.Contains(view, "WORKER_ONLY") || !strings.Contains(view, "Log history") {
		t.Fatalf("history lost scoped oldest entries:\n%s", view)
	}
	v, _ := m.key("right")
	m = v.(Model)
	view = m.View().Content
	if !strings.Contains(view, "WORKER_ONLY") || strings.Contains(view, "DEV_00") || m.offset != 0 {
		t.Fatalf("service switch retained wrong logs or offset:\n%s", view)
	}
}

func TestDesignDashboardPageUpMovesImmediatelyFromEnd(t *testing.T) {
	f := designFixture()
	for i := 0; i < 20; i++ {
		f.snapshot.Services = append(f.snapshot.Services, model.ServiceSnapshot{Spec: model.Service{ID: model.ServiceID(fmt.Sprintf("extra/%d", i)), Name: fmt.Sprintf("EXTRA_%02d", i)}, State: "stopped"})
	}
	m := resized(NewModel(f), 60, 18)
	v, _ := m.key("end")
	m = v.(Model)
	last := m.View().Content
	v, _ = m.key("pgup")
	m = v.(Model)
	if m.View().Content == last || strings.Contains(m.View().Content, "EXTRA_19") {
		t.Fatal("first PageUp from final page did not move")
	}
}

func TestDesignUnknownPortCountIsNotZero(t *testing.T) {
	f := designFixture()
	for i := range f.snapshot.Services {
		f.snapshot.Services[i].Ports[0].Status = "unknown"
	}
	m := NewModel(f)
	if !strings.Contains(m.View().Content, "External —") {
		t.Fatal("unavailable observations were counted as zero")
	}
	m.selected = 1
	if !strings.Contains(m.View().Content, "port status unknown") || strings.Contains(m.View().Content, "s to start") {
		t.Fatal("unknown listener empty state makes an unsupported assumption")
	}
	f.snapshot.Services[0].Ports[0].Status = "external"
	m = NewModel(f)
	if !strings.Contains(m.View().Content, "1+ (partial)") {
		t.Fatal("partially observed count lacks a partial marker")
	}
}

func TestDesignQuitDismissesOverlayAndRetainsShutdown(t *testing.T) {
	for _, overlay := range []string{"help", "confirm"} {
		f := designFixture()
		m := NewModel(f)
		m.help = overlay == "help"
		m.confirm = overlay == "confirm"
		m, cmd := key(m, "q")
		if cmd == nil || m.help || m.confirm || !strings.Contains(m.View().Content, "Quitting") {
			t.Fatal("quitting retained a stale overlay")
		}
		cmd()
		if f.action != "shutdown" {
			t.Fatal("display change bypassed session cleanup")
		}
	}
}
