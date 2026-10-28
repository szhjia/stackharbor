package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
)

// Opt-in acceptance owns every container by full ID. No host ports, networks,
// images, or volumes are created; redis's image volume is covered by tmpfs.
func TestAdoptedEngineLifecycleAndIdentity(t *testing.T) {
	if os.Getenv("STACKHARBOR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set STACKHARBOR_DOCKER_INTEGRATION=1 for disposable Docker acceptance")
	}
	image := os.Getenv("STACKHARBOR_TEST_DOCKER_IMAGE")
	if image == "" {
		image = "redis:7-alpine"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// Fail instead of implicitly pulling anything from a registry.
	docker("image", "inspect", image, "--format", "{{.Id}}")
	project := fmt.Sprintf("stackharbor-acceptance-%d", time.Now().UnixNano())
	create := func(service string) string {
		t.Helper()
		id := docker("create", "--label", "com.docker.compose.project="+project, "--label", "com.docker.compose.service="+service,
			"--network", "none", "--read-only", "--tmpfs", "/data", "--tmpfs", "/tmp", "--health-cmd", "test ! -f /tmp/unhealthy",
			"--health-interval", "1s", "--health-timeout", "1s", "--health-retries", "1", "--entrypoint", "/bin/sh", image, "-c", "trap 'exit 0' TERM INT; while :; do sleep 1 & wait $!; done")
		t.Cleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 20*time.Second)
			defer done()
			// --volumes is scoped to this exact disposable ID, never a prune.
			if out, err := exec.CommandContext(cleanup, "docker", "rm", "--force", "--volumes", id).CombinedOutput(); err != nil && !strings.Contains(string(out), "No such container") {
				t.Errorf("fixture cleanup %s: %v %s", id, err, out)
			}
		})
		return id
	}
	wait := func(description string, fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !fn() {
			if time.Now().After(deadline) {
				t.Fatal("timeout: " + description)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	ids := map[string]string{}
	nodes := []model.Service{}
	for _, name := range []string{"db", "managed", "observer"} {
		ids[name] = create(name)
		docker("start", ids[name])
		r := &model.ResourceSpec{Adapter: "existing-container", Project: project, Service: name, Control: "managed", Lifetime: "persistent", Available: "healthy", AllActions: name == "db"}
		if name == "observer" {
			r.Control = "observe"
		}
		n := model.Service{ID: model.ServiceID("resource/" + name), Kind: "resource", Version: 2, Resource: r}
		if name != "db" {
			n.DependsOn = []model.ServiceID{"resource/db"}
			n.Requires = []model.Requirement{{Node: "resource/db", Condition: "available"}}
		}
		nodes = append(nodes, n)
		wait(name+" healthy", func() bool { return docker("inspect", "--format", "{{.State.Health.Status}}", ids[name]) == "healthy" })
	}
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	s, err := newManuallySampledSession(model.Workspace{Root: t.TempDir(), Version: 2, Projects: []model.Project{{ID: "infra", Services: nodes}}}, &fakeRunner{}, &countingPorts{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	s.sample(ctx)
	b := NewControlBackend(s, control.Identity{})
	observerStarted := docker("inspect", "--format", "{{.State.StartedAt}}", ids["observer"])
	for _, action := range []string{"all-restart", "all-stop", "all-start"} {
		req := control.PlanRequest{Action: action}
		plan, err := b.PlanState(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(plan.Affected, "resource/observer") {
			t.Fatal("read-only resource in action plan", plan.Affected)
		}
		results, err := b.Execute(ctx, req, plan)
		if err != nil {
			t.Fatal(action, results, err)
		}
		for _, result := range results {
			if result.State != "succeeded" {
				t.Fatal(action, results)
			}
		}
		for _, name := range []string{"db", "managed"} {
			want := "running"
			if action == "all-stop" || action == "all-start" && name == "managed" {
				want = "exited"
			}
			if got := docker("inspect", "--format", "{{.State.Status}}", ids[name]); got != want {
				t.Fatalf("%s %s left %s want %s", action, name, got, want)
			}
		}
	}
	if docker("inspect", "--format", "{{.State.StartedAt}}", ids["observer"]) != observerStarted {
		t.Fatal("observer was restarted")
	}
	for _, mode := range []string{"recover", "cancel", "timeout"} {
		docker("exec", ids["db"], "touch", "/tmp/unhealthy")
		wait("unhealthy", func() bool {
			return docker("inspect", "--format", "{{.State.Health.Status}}", ids["db"]) == "unhealthy"
		})
		s.sample(ctx)
		startCtx, startCancel := context.WithTimeout(ctx, 70*time.Second)
		done := make(chan error, 1)
		go func() { done <- s.Start(startCtx, []model.ServiceID{"resource/db"}) }()
		wait("health wait entered", func() bool {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.entries["resource/db"].state == "starting" && !s.entries["resource/db"].readyClosed
		})
		if mode == "recover" {
			docker("exec", ids["db"], "rm", "/tmp/unhealthy")
		} else if mode == "cancel" {
			startCancel()
		}
		startErr := <-done
		startCancel()
		if (startErr != nil) != (mode != "recover") {
			t.Fatalf("health %s outcome: %v", mode, startErr)
		}
		if mode == "timeout" && !strings.Contains(startErr.Error(), "within 60 seconds") {
			t.Fatalf("health deadline not exercised: %v", startErr)
		}
		docker("exec", ids["db"], "rm", "-f", "/tmp/unhealthy")
		wait("health restored", func() bool { return docker("inspect", "--format", "{{.State.Health.Status}}", ids["db"]) == "healthy" })
		s.sample(ctx)
	}
	duplicate := create("db")
	if _, err := b.PlanState(ctx, control.PlanRequest{Action: "start", Targets: []string{"resource/db"}}); err == nil {
		t.Fatal("scaled service accepted")
	}
	docker("rm", "--force", "--volumes", duplicate)
	docker("rm", "--force", "--volumes", ids["db"])
	replacement := create("db")
	if _, err := b.PlanState(ctx, control.PlanRequest{Action: "start", Targets: []string{"resource/db"}}); err == nil {
		t.Fatal("replacement container accepted")
	}
	if docker("inspect", "--format", "{{.State.Status}}", replacement) != "created" {
		t.Fatal("replacement container mutated")
	}
	docker("rm", "--force", "--volumes", replacement)
	if _, err := b.PlanState(ctx, control.PlanRequest{Action: "start", Targets: []string{"resource/db"}}); err == nil {
		t.Fatal("removed container accepted")
	}
	t.Logf("owned project %s: lifecycle, observe protection, health recovery/cancel/60s deadline, scaling/replacement/removal verified; fixture root %s", project, filepath.Base(s.workspace.Root))
}
