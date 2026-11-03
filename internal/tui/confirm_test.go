package tui

import (
	"fmt"
	"image/color"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/szhjia/stackharbor/internal/model"
)

func confirmationFixture() Model {
	m := resized(NewModel(fixture()), 120, 35)
	m.confirm, m.action = true, "all-start"
	m.snapshot.Services, m.snapshot.Projects = nil, nil
	ids := []model.ServiceID{"one/service/dev", "one/service/api", "two/service/dev", "three/service/dev", "four/service/dev", "five/service/dev"}
	for i, id := range ids {
		project, _, _ := strings.Cut(string(id), "/")
		key := "dev"
		if i == 1 {
			key = "api"
		}
		spec := model.Service{ID: id, ProjectID: project, Key: key, Name: key, Kind: "service", Ports: []model.Port{{Number: 5600 + i}}}
		m.snapshot.Services = append(m.snapshot.Services, model.ServiceSnapshot{Spec: spec})
		found := false
		for j := range m.snapshot.Projects {
			if m.snapshot.Projects[j].ID == project {
				m.snapshot.Projects[j].Services = append(m.snapshot.Projects[j].Services, spec)
				found = true
			}
		}
		if !found {
			m.snapshot.Projects = append(m.snapshot.Projects, model.Project{ID: project, Name: "App " + project, Services: []model.Service{spec}})
		}
	}
	m.pending = append(append([]model.ServiceID{}, ids...), "NOT_PLANNED/service/dev")
	m.planned = &PlannedAction{ID: "retained", Affected: append(append([]model.ServiceID{}, ids...), ids[0])}
	m.status = "Scope: managed application services and selected existing containers; other persistent containers keep running"
	return m
}

func TestConfirmationShowsOneListWithCountNamesPathsAndPorts(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := confirmationFixture()
	view := m.render()
	for _, label := range []string{"Confirm: Start 6 services", "Name", "Path", "Ports", "App one / dev", "App one / api", "App two"} {
		if !strings.Contains(view, label) {
			t.Errorf("missing %q:\n%s", label, view)
		}
	}
	for _, unwanted := range []string{"Selected scope", "Plan scope", "Launch scope", "NOT_PLANNED", m.status} {
		if strings.Contains(view, unwanted) {
			t.Errorf("confirmation contains redundant or unplanned content: %q", unwanted)
		}
	}
	for i, service := range m.snapshot.Services {
		id := string(service.Spec.ID)
		if strings.Count(view, id) != 1 {
			t.Errorf("%s must be displayed exactly once", id)
		}
		for _, row := range strings.Split(view, "\n") {
			if strings.Contains(row, id) && !strings.Contains(row, fmt.Sprint(5600+i)) {
				t.Errorf("path and port do not share their service row: %q", row)
			}
		}
	}
	if strings.Contains(view, "\x1b") {
		t.Fatal("NO_COLOR emitted escape sequences")
	}
}

func TestConfirmationCountsKindsAndPreservesDeclaredNamesAndPorts(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := confirmationFixture()
	m.snapshot.Services[0].Spec.Name = "Web console"
	m.snapshot.Services[0].Spec.Ports = []model.Port{{Number: 8080}, {Number: 8081}}
	m.snapshot.Services[1].Spec.Kind = "task"
	m.snapshot.Services[1].Spec.Ports = nil
	m.snapshot.Services[2].Spec.Kind = "resource"
	m.snapshot.Services[2].Spec.Resource = &model.ResourceSpec{Service: "database"}
	m.snapshot.Docker = []model.DockerSnapshot{{Service: "database", Ports: "127.0.0.1:5433->5432/tcp"}}
	view := m.render()
	for _, label := range []string{"Start 4 services · 1 container · 1 task", "Web console", "8080, 8081", "127.0.0.1:5433->5432/tcp"} {
		if !strings.Contains(view, label) {
			t.Errorf("missing %q:\n%s", label, view)
		}
	}
	for _, row := range strings.Split(view, "\n") {
		if strings.Contains(row, "one/service/api") && !strings.Contains(row, "—") {
			t.Fatal("task with no declared ports must show an empty value", row)
		}
	}
	m.planned.Affected = nil
	if view := m.render(); !strings.Contains(view, "Start 0 services") || strings.Contains(view, "one/service/dev") {
		t.Fatal("empty retained plan fell back to the selection", view)
	}
}

func TestConfirmationFourColorsStayWithApplication(t *testing.T) {
	old, ok := os.LookupEnv("NO_COLOR")
	os.Unsetenv("NO_COLOR")
	t.Cleanup(func() {
		if ok {
			os.Setenv("NO_COLOR", old)
		}
	})
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	for _, light := range []bool{false, true} {
		m := confirmationFixture()
		m.lightTheme = light
		screen := uv.NewScreenBuffer(m.width, m.height)
		screen.Method = ansi.GraphemeWidth
		view := m.render()
		uv.NewStyledString(view).Draw(screen, uv.Rect(0, 0, m.width, m.height))
		projectColors := map[string]color.Color{}
		unique := map[color.Color]bool{}
		for y, row := range strings.Split(ansi.Strip(view), "\n") {
			x := strings.Index(row, "/service/")
			if x < 0 {
				continue
			}
			start := strings.LastIndex(row[:x], " ") + 1
			project := row[start:x]
			fg := screen.Lines[y][start].Style.Fg
			if fg == nil {
				t.Fatal("path lacks color", row)
			}
			if previous, ok := projectColors[project]; ok && previous != fg {
				t.Errorf("application %s changed color between service rows", project)
			}
			projectColors[project] = fg
			unique[fg] = true
			bg := 0.0
			if light {
				bg = 1
			}
			lum := luminance(fg)
			if ratio := (max(lum, bg) + 0.05) / (min(lum, bg) + 0.05); ratio < 4.5 {
				t.Errorf("path contrast %.2f < 4.5 (light=%t)", ratio, light)
			}
		}
		if len(unique) != 4 || projectColors["one"] != projectColors["five"] {
			t.Fatal("expected four cycling application colors", projectColors)
		}
	}
}

func TestConfirmationWrapsLongPathsAndCanPageThroughAllContent(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := resized(confirmationFixture(), 40, 14)
	long := model.ServiceID(strings.Repeat("应用路径", 12) + "/service/dev")
	m.planned.Affected = append(m.planned.Affected, long)
	m.planned.Affected = append(m.planned.Affected, "LAST_APP/service/dev")
	var paths []string
	for i := 0; i < 100; i++ {
		view := m.render()
		for _, row := range strings.Split(view, "\n") {
			if ansi.StringWidth(row) != m.width || strings.Contains(row, "…") {
				t.Fatalf("wrapped content overflowed or was truncated: %q", row)
			}
		}
		paths = append(paths, view)
		previous := m.confirmOffset
		v, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		m = v.(Model)
		if cmd != nil {
			t.Fatal("paging dispatched an action")
		}
		if m.confirmOffset == previous {
			break
		}
	}
	all := strings.Join(paths, "\n")
	if !strings.Contains(all, "LAST_APP/service/dev") || !strings.Contains(all, "PgUp/PgDn") {
		t.Fatal("last path or navigation hint unreachable", all)
	}
	// Join only the path rows, including continuation rows, across all pages.
	joined := ""
	for _, view := range paths {
		for _, row := range strings.Split(view, "\n") {
			if strings.HasPrefix(row, "    ") {
				joined += strings.TrimSpace(row)
			}
		}
	}
	if !strings.Contains(joined, string(long)) {
		t.Fatal("long CJK path lost text across wrapping/pages", joined)
	}
	// Extra PageDown at the end must not make the first PageUp appear stuck.
	last := m.render()
	v, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	m = v.(Model)
	if m.render() == last {
		t.Fatal("first PageUp did not move away from the final page")
	}
}

func TestConfirmationConflictAndDockerScopesRemainReadable(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, conflicts := range []bool{false, true} {
		m := resized(confirmationFixture(), 60, 18)
		m.action, m.pendingDocker = "docker-stop", []string{"database", "redis"}
		if !conflicts {
			m.planned = &PlannedAction{Affected: []model.ServiceID{"database", "redis"}}
			m.snapshot.Docker = []model.DockerSnapshot{{Service: "database", Name: "Database", Ports: "5432"}, {Service: "redis", Name: "Redis", Ports: "6379"}}
		}
		if conflicts {
			for i := 0; i < 12; i++ {
				m.conflicts = append(m.conflicts, model.PortConflict{Port: 8100 + i, Identity: model.ProcessIdentity{PID: int32(1000 + i)}, Command: "node"})
			}
		}
		all := ""
		for i := 0; i < 30; i++ {
			all += m.render()
			v, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
			next := v.(Model)
			if next.confirmOffset == m.confirmOffset {
				break
			}
			m = next
		}
		if conflicts {
			for i := 0; i < 12; i++ {
				if !strings.Contains(all, fmt.Sprintf("Port %d · PID %d", 8100+i, 1000+i)) {
					t.Fatal("conflicting process was hidden")
				}
			}
			if strings.Contains(all, "scope:") || !strings.Contains(all, "one/service/api") {
				t.Fatal("launch scope unreachable")
			}
		} else if !strings.Contains(all, "Stop 2 containers") || !strings.Contains(all, "Database") || !strings.Contains(all, "Redis") || !strings.Contains(all, "5432") || !strings.Contains(all, "6379") {
			t.Fatal("Docker confirmation lost scope or warning", all)
		}
	}
}
