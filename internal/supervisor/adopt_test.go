package supervisor

import (
	"context"
	"os"
	"testing"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/discovery"
	"github.com/szhjia/stackharbor/internal/graph"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
)

func TestStoppingDependencyIncludesRunningAdoptedContainer(t *testing.T) {
	db := model.Service{ID: "resource/database", Kind: "resource", Resource: &model.ResourceSpec{Adapter: "existing-container", Control: "managed"}}
	api := model.Service{ID: "resource/api", Kind: "resource", Resource: &model.ResourceSpec{Adapter: "existing-container", Control: "managed"}, DependsOn: []model.ServiceID{db.ID}}
	g, ds := graph.Build([]model.Service{db, api})
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	s := &Session{graph: g, entries: map[model.ServiceID]*entry{db.ID: {spec: db, state: "available"}, api.ID: {spec: api, state: "available", resourceRunning: true}}}
	affected := s.Affected([]model.ServiceID{db.ID})
	if len(affected) != 1 || affected[0] != api.ID {
		t.Fatalf("running dependent container omitted: %v", affected)
	}
}

func TestLiveAdoptedSessionPlansWithoutChangingContainers(t *testing.T) {
	workspace := os.Getenv("STACKHARBOR_TEST_ADOPT_WORKSPACE")
	if workspace == "" {
		t.Skip("set STACKHARBOR_TEST_ADOPT_WORKSPACE for a read-only live session check")
	}
	ctx := context.Background()
	w := discovery.Discover(ctx, discovery.Options{WorkspaceFile: workspace})
	if w.Invalid() {
		t.Fatalf("workspace invalid: %v", w.Diagnostics)
	}
	s, err := New(w, nil, observe.NewPortProbe(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	b := NewControlBackend(s, control.Identity{})
	plan, err := b.PlanState(ctx, control.PlanRequest{Action: "all-start"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Affected) == 0 {
		t.Fatal("global action omitted all applications")
	}
	want := os.Getenv("STACKHARBOR_TEST_ADOPT_NODE")
	excluded := os.Getenv("STACKHARBOR_TEST_ADOPT_EXCLUDED_NODE")
	if want != "" {
		found := false
		for _, id := range plan.Affected {
			found = found || id == want
		}
		if !found {
			t.Fatalf("global plan omitted adopted node %s: %v", want, plan.Affected)
		}
	}
	if excluded != "" {
		for _, id := range plan.Affected {
			if id == excluded {
				t.Fatalf("global plan included excluded node %s: %v", excluded, plan.Affected)
			}
		}
	}
}
