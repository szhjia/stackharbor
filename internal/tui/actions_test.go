package tui

import (
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/szhjia/stackharbor/internal/model"
	"strings"
	"testing"
)

func TestGlobalControlsVisibleOnProjectAndRestartAvailableWhenStopped(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := NewModel(fixture())
	m.width = 180
	m.selected = 2
	for _, label := range []string{"Start all", "Stop all", "Restart all", "Restart"} {
		if !strings.Contains(m.footer(), label) {
			t.Errorf("missing %s: %s", label, m.footer())
		}
	}
	if strings.Contains(m.render(), "RSS") {
		t.Fatal("technical RSS label remains")
	}
}

type takeoverFake struct {
	*fakeController
	released bool
}

func (f *takeoverFake) Conflicts(context.Context, []model.ServiceID) ([]model.PortConflict, error) {
	return []model.PortConflict{{Port: 8100, Command: "node", Identity: model.ProcessIdentity{PID: 4321, CreatedMillis: 100}}}, nil
}
func (f *takeoverFake) ResolveConflicts(context.Context, []model.ServiceID, []model.PortConflict, string) error {
	f.released = true
	return nil
}
func TestConflictConfirmationCancelAndQuit(t *testing.T) {
	for _, cancel := range []string{"n", "q"} {
		f := &takeoverFake{fakeController: fixture()}
		m := NewModel(f)
		m.selected = 1
		m, cmd := key(m, "s")
		if cmd == nil {
			t.Fatal("no preflight")
		}
		v, _ := m.Update(cmd())
		m = v.(Model)
		if !m.confirm || f.released || !strings.Contains(m.render(), "PID 4321") {
			t.Fatal("missing safe confirmation")
		}
		m, cmd = key(m, cancel)
		if cmd != nil {
			cmd()
		}
		if f.released {
			t.Fatal("cancel released process")
		}
		if cancel == "q" && f.action != "shutdown" {
			t.Fatal("quit skipped shutdown")
		}
	}
}
func TestProjectFooterAt80ColumnsKeepsActions(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := NewModel(fixture())
	m.selected = 1
	m.snapshot.DockerFile = "compose.yaml"
	footer := m.footer()
	for _, k := range []string{"Stop x", "Restart r", "All Shift+S/X/R", "Docker d", "Quit q"} {
		if !strings.Contains(footer, k) {
			t.Fatal(k, footer)
		}
	}
	if ansi.StringWidth(footer) > 80 {
		t.Fatal("footer overflow", footer)
	}
}
func TestDockerSelectionFitsAndDispatchesSingleScope(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	f := fixture()
	f.snapshot.DockerFile = "compose.yaml"
	for i := 0; i < 20; i++ {
		f.snapshot.Docker = append(f.snapshot.Docker, model.DockerSnapshot{Service: fmt.Sprintf("db%d", i), Name: "数据库", State: "running", Health: "healthy"})
	}
	m := NewModel(f)
	m, _ = key(m, "d")
	for i := 0; i < 30; i++ {
		m, _ = key(m, "right")
	}
	if !strings.Contains(m.render(), "db19") {
		t.Fatal("last container inaccessible")
	}
	m, cmd := key(m, "x")
	if !m.confirm || cmd != nil || len(m.pendingDocker) != 1 || m.pendingDocker[0] != "db19" {
		t.Fatal("wrong docker scope")
	}
}
