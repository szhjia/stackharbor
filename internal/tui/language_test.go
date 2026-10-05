package tui

import (
	"strings"
	"testing"
	"unicode"

	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
)

func TestBuiltInScreensUseEnglish(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, screen := range []string{"dashboard", "project", "details", "candidate", "docker", "help", "confirm", "conflicts", "quitting", "tiny"} {
		t.Run(screen, func(t *testing.T) {
			f := designFixture()
			f.snapshot.Candidates = []model.Candidate{{Name: "Pending project", Cwd: "/workspace/pending"}}
			f.snapshot.DockerFile = "compose.yaml"
			f.snapshot.Docker = []model.DockerSnapshot{{Service: "db", Name: "Database", State: "running", Health: "healthy"}}
			m := resized(NewModel(f), 160, 40)
			switch screen {
			case "project", "details":
				m.selected, m.details = 1, screen == "details"
			case "candidate":
				m.selected = len(f.snapshot.Projects) + 1
			case "docker":
				m.dockerView = true
			case "help":
				m.help = true
			case "confirm", "conflicts":
				m.confirm, m.action, m.pending = true, "stop", []model.ServiceID{"admin/dev"}
				if screen == "conflicts" {
					m.action = "start"
					m.conflicts = []model.PortConflict{{Port: 8100, Command: "node", Identity: model.ProcessIdentity{PID: 4321}}}
				}
			case "quitting":
				m, _ = key(m, "q")
			case "tiny":
				m = resized(m, 20, 5)
			}
			view := m.View().Content
			if strings.ContainsFunc(view, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
				t.Fatalf("built-in screen contains Chinese:\n%s", view)
			}
		})
	}
}

func TestEnglishUIKeepsUserNamesAndLogs(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	f := designFixture()
	f.snapshot.Projects[0].Name = "学习工程"
	f.snapshot.Projects[0].Services[0].Key = ""
	f.snapshot.Projects[0].Services[0].Name = "网页服务"
	f.snapshot.Services[0].Spec = f.snapshot.Projects[0].Services[0]
	f.store.Append(logs.Entry{ServiceID: "admin/dev", Text: "用户日志：启动成功"})
	m := resized(NewModel(f), 160, 40)
	m.selected = 1
	view := m.View().Content
	for _, text := range []string{"学习工程", "网页服务", "用户日志：启动成功", "Logs"} {
		if !strings.Contains(view, text) {
			t.Errorf("missing %q in rendered project:\n%s", text, view)
		}
	}
}
