package tui

import (
	"strings"
	"testing"

	"github.com/szhjia/stackharbor/internal/model"
)

func TestDockerUsesSharedVerticalNavigationAndHorizontalSelection(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, width := range []int{60, 120} {
		f := fixture()
		f.snapshot.Candidates = []model.Candidate{{Name: "Pending", Cwd: "/workspace/pending"}}
		f.snapshot.DockerFile = "compose.yaml"
		f.snapshot.Docker = []model.DockerSnapshot{{Service: "db", State: "running"}, {Service: "redis", State: "running"}}
		m := resized(NewModel(f), width, 30)
		lastProject := len(f.snapshot.Projects) + len(f.snapshot.Candidates)
		for i := 0; i <= lastProject; i++ {
			m, _ = key(m, "down")
		}
		if !m.dockerView || !strings.Contains(m.render(), "Docker containers") {
			t.Fatal("Docker cannot be reached with down")
		}
		m, _ = key(m, "right")
		if m.dockerIndex != 1 {
			t.Fatal("right did not select redis")
		}
		m, cmd := key(m, "x")
		if cmd != nil || !m.confirm || len(m.pendingDocker) != 1 || m.pendingDocker[0] != "redis" {
			t.Fatal("container stop must confirm the selected scope")
		}
		m, _ = key(m, "esc")
		m, _ = key(m, "left")
		if m.dockerIndex != 0 {
			t.Fatal("left did not select db")
		}
		m, _ = key(m, "up")
		if m.dockerView || m.selected != lastProject {
			t.Fatal("up must leave Docker for the preceding navigation item")
		}
		m, _ = key(m, "d")
		if !m.dockerView {
			t.Fatal("optional direct shortcut stopped working")
		}
		m, _ = key(m, "home")
		if m.dockerView || m.selected != 0 {
			t.Fatal("Home must return to dashboard")
		}
	}
}

func TestDockerNavigationWithoutContainersOrCompose(t *testing.T) {
	for _, compose := range []bool{false, true} {
		f := fixture()
		if compose {
			f.snapshot.DockerFile = "compose.yaml"
		}
		m := NewModel(f)
		for i := 0; i < 10; i++ {
			m, _ = key(m, "down")
		}
		if m.dockerView != compose {
			t.Fatal("navigation must include Docker only when configured")
		}
		m, _ = key(m, "right")
		m, _ = key(m, "left")
		if compose {
			m, cmd := key(m, "s")
			if cmd != nil || m.confirm {
				t.Fatal("empty Docker view must not dispatch an action")
			}
		}
	}
}
