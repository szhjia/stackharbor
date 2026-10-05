package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/szhjia/stackharbor/internal/model"
)

func TestResourceProjectDisplaysContainerMetrics(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	f := fixture()
	f.snapshot.Services[0].Spec.Kind = "resource"
	f.snapshot.Services[0].State = "available"
	f.snapshot.Services[0].MetricSource = "docker"
	cpu := 125.5
	f.snapshot.Services[0].Metric = model.Metric{Known: true, RSS: 128 * 1048576, CPUPercent: &cpu}
	for _, width := range []int{80, 180} {
		m := resized(NewModel(f), width, 30)
		m.selected = 1
		view := m.render()
		if !strings.Contains(view, "128.0 MiB") || !strings.Contains(view, "125.5%") {
			t.Fatalf("container metrics hidden at width %d:\n%s", width, view)
		}
	}
}

func TestDockerDashboardRefreshesLatestMetrics(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	f := fixture()
	f.snapshot.Services[0].MetricSource = "docker"
	cpu := 1.0
	f.snapshot.Services[0].Metric = model.Metric{Known: true, RSS: 128 * 1048576, CPUPercent: &cpu}
	m := resized(NewModel(f), 180, 30)
	if !strings.Contains(m.render(), "128.0 MiB") {
		t.Fatal("initial container usage missing")
	}
	cpu = 5
	f.snapshot.Services[0].Metric = model.Metric{Known: true, RSS: 256 * 1048576, CPUPercent: &cpu}
	updated, _ := m.Update(tickMsg(time.Now()))
	view := updated.(Model).render()
	if !strings.Contains(view, "256.0 MiB") || !strings.Contains(view, "5.0%") || strings.Contains(view, "128.0 MiB") {
		t.Fatal("UI tick did not replace container metrics", view)
	}
}

func TestDockerPanelShowsMetricsAndExplainsFailure(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, failed := range []bool{false, true} {
		f := fixture()
		f.snapshot.DockerFile = "compose.yaml"
		cpu := 0.0
		row := model.DockerSnapshot{Service: "db", Name: "wordverse-db-1", State: "running", Health: "healthy", Metric: model.Metric{Known: true, RSS: 128 * 1048576, CPUPercent: &cpu}}
		if failed {
			row.Metric = model.Metric{}
			row.MetricError = "Docker metrics unavailable: engine timeout"
		}
		f.snapshot.Docker = []model.DockerSnapshot{row}
		for _, width := range []int{60, 80, 180} {
			m := resized(NewModel(f), width, 30)
			m.dockerView = true
			view := m.render()
			if !strings.Contains(view, "Running") || !strings.Contains(view, "Healthy") {
				t.Fatal("metric failure hid valid container status", view)
			}
			if failed {
				if !strings.Contains(view, "Memory —") || !strings.Contains(view, "CPU —") || !strings.Contains(view, "metrics unavailable") {
					t.Fatal("failure is not explained", view)
				}
			} else if !strings.Contains(view, "128.0 MiB") || !strings.Contains(view, "0.0%") {
				t.Fatal("metrics or known zero hidden", view)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatal("Docker metric row overflows", line)
				}
			}
		}
	}
}
