package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/runner"
)

// A service without probes or dependencies needs only lifecycle notifications,
// while a probe-less dependent must still notice a dependency becoming unready.
func TestHealthlessServicesRetainExitAndDependencyMonitoring(t *testing.T) {
	db := service("app/db")
	api := service("app/api", db.ID)
	s, fake := setup(t, db, api)
	if err := s.Start(context.Background(), []model.ServiceID{api.ID}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	handle := fake.handles[db.ID]
	fake.mu.Unlock()
	handle.done <- runner.ExitResult{Code: 7}

	deadline := time.Now().Add(4 * time.Second)
	for {
		states := map[model.ServiceID]string{}
		for _, item := range s.Snapshot().Services {
			states[item.Spec.ID] = item.State
		}
		if states[db.ID] == "failed" && states[api.ID] == "unready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe-less lifecycle/dependency monitoring was lost: %v", states)
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
