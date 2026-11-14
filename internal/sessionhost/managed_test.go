package sessionhost

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
)

func TestHostOwnershipCountsAndEnvironmentReload(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := model.Workspace{Root: root, Projects: []model.Project{{ID: "app", Services: []model.Service{{ID: "app/api", ProjectID: "app", Cwd: root, Command: []string{"/bin/sleep", "60"}, EnvFrozen: true, Env: map[string]string{"PATH": "/usr/bin:/bin", "MARKER": "old"}, Stop: model.StopPolicy{Timeout: time.Second}}}}}}
	host, err := NewWithOptions(context.Background(), w, Options{Namespace: filepath.Join(root, "cache"), Environment: []string{"PATH=/usr/bin:/bin", "MARKER=old"}, ExternalObservation: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if processes, resources := host.OwnershipCounts(); processes != 0 || resources != 0 {
		t.Fatalf("opening created ownership: %d/%d", processes, resources)
	}
	plan, err := host.coordinator.Plan(context.Background(), control.PlanRequest{Action: "start", Targets: []string{"app/api"}})
	if err != nil {
		t.Fatal(err)
	}
	w.Projects[0].Services[0].Env = map[string]string{"PATH": "/usr/bin:/bin", "MARKER": "new"}
	if err := host.ReloadEnvironment(w, []string{"PATH=/usr/bin:/bin", "MARKER=new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.coordinator.Submit(context.Background(), control.SubmitRequest{PlanID: plan.ID, IdempotencyKey: "stale-env"}); err == nil {
		t.Fatal("reload preserved an old environment plan")
	}
	if err := host.Controller().Start(context.Background(), []model.ServiceID{"app/api"}); err != nil {
		t.Fatal(err)
	}
	if processes, resources := host.OwnershipCounts(); processes != 1 || resources != 0 {
		t.Fatalf("host lost physical process ownership: %d/%d", processes, resources)
	}
	if err := host.Controller().Stop(context.Background(), []model.ServiceID{"app/api"}); err != nil {
		t.Fatal(err)
	}
	if processes, resources := host.OwnershipCounts(); processes != 0 || resources != 0 {
		t.Fatalf("stopped process remains owned: %d/%d", processes, resources)
	}
}
