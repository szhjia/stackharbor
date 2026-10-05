package tui

import (
	"strings"
	"testing"

	"github.com/szhjia/stackharbor/internal/model"
)

func TestDashboardExternalListenerDoesNotClaimStopped(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := resized(NewModel(designFixture()), 180, 30)
	v := m.snapshot.Services[1]
	row := strings.Join(m.dashboardRows(v), "\n")
	if strings.Contains(row, "Stopped") || !strings.Contains(row, "Listening") {
		t.Fatalf("external listener misrepresented as stopped: %s", row)
	}
	v.Ports = []model.PortObservation{{Port: 5601, Status: "free"}}
	if row = strings.Join(m.dashboardRows(v), "\n"); !strings.Contains(row, "Stopped") {
		t.Fatal("free port must not be reported running", row)
	}
}

func TestReadyExternalServiceIsVisibleAcrossDashboardSidebarAndDetails(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	f := designFixture()
	f.snapshot.Services[1].ObservedState = "running"
	m := resized(NewModel(f), 180, 30)
	if row := strings.Join(m.dashboardRows(f.snapshot.Services[1]), "\n"); !strings.Contains(row, "Running ext") || strings.Contains(row, "Stopped") {
		t.Fatal(row)
	}
	if sidebar := m.projectStatus("backend"); !strings.Contains(sidebar, "Running ext") {
		t.Fatal(sidebar)
	}
	if heading := strings.Join(m.dashboardHeading(), "\n"); !strings.Contains(heading, "Running 1/4") || !strings.Contains(heading, "Session 0") {
		t.Fatal(heading)
	}
	m.selected = 2
	if detail := strings.Join(m.project(), "\n"); !strings.Contains(detail, "Running ext") {
		t.Fatal(detail)
	}
	f.snapshot.Services[1].ObservedState = "unready"
	m.snapshot = f.snapshot
	if row := strings.Join(m.dashboardRows(f.snapshot.Services[1]), "\n"); !strings.Contains(row, "Unready ext") {
		t.Fatal(row)
	}
}

func TestActivatedObserverIsNeverCountedAsSessionOwned(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	f := designFixture()
	f.snapshot.Services[1].Spec.Control = "observe"
	f.snapshot.Services[1].State = "running"
	m := resized(NewModel(f), 180, 30)
	if heading := strings.Join(m.dashboardHeading(), "\n"); !strings.Contains(heading, "Running 1/4") || !strings.Contains(heading, "Session 0") {
		t.Fatal(heading)
	}
	if row := strings.Join(m.dashboardRows(f.snapshot.Services[1]), "\n"); !strings.Contains(row, "Running ext") {
		t.Fatal(row)
	}
	if sidebar := m.projectStatus("backend"); !strings.Contains(sidebar, "Running ext") {
		t.Fatal(sidebar)
	}
}
