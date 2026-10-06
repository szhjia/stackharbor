package supervisor

import (
	"context"
	"errors"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/docker"
	"github.com/szhjia/stackharbor/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func resourceFixture(t *testing.T) (*Session, string, string) {
	t.Helper()
	root := t.TempDir()
	identity := filepath.Join(root, "identity")
	calls := filepath.Join(root, "calls")
	os.WriteFile(identity, []byte("old"), 0600)
	script := `#!/bin/sh
case "$1" in
 info) printf '"fixture-daemon"'; exit;;
esac
case "$*" in
 *' config '*) printf '{"name":"fixture-project"}'; exit;;
 *' ps '*) printf '[{"ID":"%s","Service":"db","State":"running"}]' "$(cat "$SH_RESOURCE_ID")";exit;;
esac
printf '%s\n' "$*" >> "$SH_RESOURCE_CALLS"
`
	os.WriteFile(filepath.Join(root, "docker"), []byte(script), 0700)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SH_RESOURCE_ID", identity)
	t.Setenv("SH_RESOURCE_CALLS", calls)
	os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services:\n  db: {image: postgres}\n"), 0600)
	node := service("app/a")
	node.DockerDependsOn = []string{"db"}
	s, _ := setup(t, node)
	// The manager follows the actual fixture compose path; session native config stays unchanged.
	s.docker = docker.New(root)
	return s, identity, calls
}
func TestRecreatedContainerInvalidatesPlan(t *testing.T) {
	s, identity, calls := resourceFixture(t)
	b := NewControlBackend(s, control.Identity{})
	req := control.PlanRequest{Action: "start", Targets: []string{"app/a"}}
	plan, err := b.PlanState(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(identity, []byte("recreated"), 0600)
	_, err = b.Execute(context.Background(), req, plan)
	var api *control.APIError
	if !errors.As(err, &api) || api.Code != "plan_conflict" {
		t.Fatal("recreated container accepted", err)
	}
	if raw, _ := os.ReadFile(calls); len(raw) != 0 {
		t.Fatal("stale plan caused mutation", string(raw))
	}
}
func TestControlResourceLockPropagatesIntoDependencyLaunch(t *testing.T) {
	s, _, calls := resourceFixture(t)
	b := NewControlBackend(s, control.Identity{})
	req := control.PlanRequest{Action: "start", Targets: []string{"app/a"}}
	plan, err := b.PlanState(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err = b.Execute(ctx, req, plan); err != nil {
		t.Fatal("lock capability lost in detached launch", err)
	}
	if raw, _ := os.ReadFile(calls); len(raw) == 0 {
		t.Fatal("Docker start absent")
	}
}

func TestDockerRestartLocksAndBindsDependencyClosure(t *testing.T) {
	s, identity, calls := resourceFixture(t)
	compose := s.docker.File
	if err := os.WriteFile(compose, []byte("services:\n  db: {image: postgres, depends_on: [redis]}\n  redis: {image: redis}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.docker = docker.New(filepath.Dir(compose))
	script := `#!/bin/sh
case "$1" in info) printf '"fixture-daemon"';exit;; esac
case "$*" in
 *' config '*) printf '{"name":"fixture-project"}';exit;;
 *' ps '*) printf '[{"ID":"db-fixed","Service":"db","State":"running"},{"ID":"%s","Service":"redis","State":"running"}]' "$(cat "$SH_RESOURCE_ID")";exit;;
esac
printf '%s\n' "$*" >> "$SH_RESOURCE_CALLS"
`
	os.WriteFile(filepath.Join(filepath.Dir(compose), "docker"), []byte(script), 0700)
	s.entries["app/a"].spec.DockerDependsOn = nil
	b := NewControlBackend(s, control.Identity{})
	req := control.PlanRequest{Action: "docker-restart", Targets: []string{"db"}}
	plan, err := b.PlanState(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	execution := plan.Data.(controlExecution)
	if len(execution.ResourceKeys) != 2 {
		t.Errorf("restart omitted dependency lock: %+v", execution.ResourceKeys)
	}
	os.WriteFile(identity, []byte("recreated-dependency"), 0600)
	_, err = b.Execute(context.Background(), req, plan)
	var api *control.APIError
	if !errors.As(err, &api) || api.Code != "plan_conflict" {
		t.Error("dependency recreation accepted", err)
	}
	if raw, _ := os.ReadFile(calls); len(raw) != 0 {
		t.Error("restart mutated before dependency revalidation", string(raw))
	}
	// An occupied dependency must block the complete operation before its target stop.
	os.WriteFile(identity, []byte("old"), 0600)
	plan, err = b.PlanState(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	dependencyKeys, err := s.docker.LockKeys(context.Background(), []string{"redis"})
	if err != nil {
		t.Fatal(err)
	}
	release, err := control.AcquireResourceLocks(context.Background(), control.ResourceLockNamespace, dependencyKeys)
	if err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	_, err = b.Execute(bounded, req, plan)
	cancel()
	release()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("occupied dependency was not acquired before mutation", err)
	}
	if raw, _ := os.ReadFile(calls); len(raw) != 0 {
		t.Fatal("target mutated while dependency lock occupied", string(raw))
	}
	bounded, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err = b.Execute(bounded, req, plan); err != nil {
		t.Fatal("restart dependency borrowed locks deadlocked", err)
	}
	raw, _ := os.ReadFile(calls)
	if !strings.Contains(string(raw), "60 redis") {
		t.Fatal("restart dependency not ensured", string(raw))
	}
}
func cleanupResourceFixture(t *testing.T, rows string, lifetime, ownership string) (*Session, string, string) {
	t.Helper()
	return cleanupResourceFixtureWithIdentity(t, rows, lifetime, ownership, true)
}
func cleanupResourceFixtureWithIdentity(t *testing.T, rows string, lifetime, ownership string, identityKnown bool) (*Session, string, string) {
	t.Helper()
	root := t.TempDir()
	compose := filepath.Join(root, "compose.yaml")
	data := filepath.Join(root, "rows")
	calls := filepath.Join(root, "calls")
	os.WriteFile(compose, []byte("services:\n  db: {image: postgres}\n"), 0600)
	os.WriteFile(data, []byte(rows), 0600)
	script := `#!/bin/sh
case "$1" in info) if [ "$SH_CLEANUP_IDENTITY_KNOWN" = "1" ]; then printf '"cleanup-fixture-daemon"';exit; else exit 1; fi;; stats) printf '';exit;; esac
case "$*" in *' config '*) printf '{"name":"cleanup-fixture"}';exit;; *' ps '*) cat "$SH_CLEANUP_ROWS";exit;; esac
printf '%s\n' "$*" >> "$SH_CLEANUP_CALLS"
`
	os.WriteFile(filepath.Join(root, "docker"), []byte(script), 0700)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SH_CLEANUP_ROWS", data)
	t.Setenv("SH_CLEANUP_CALLS", calls)
	if identityKnown {
		t.Setenv("SH_CLEANUP_IDENTITY_KNOWN", "1")
	} else {
		t.Setenv("SH_CLEANUP_IDENTITY_KNOWN", "0")
	}
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	node := model.Service{ID: "infra/db", ProjectID: "infra", Kind: "resource", Name: "db", Resource: &model.ResourceSpec{File: compose, Project: "cleanup-fixture", Service: "db", Available: "healthy", Lifetime: lifetime, Control: ownership}}
	s, err := newManuallySampledSession(model.Workspace{Root: root, Version: 2, Projects: []model.Project{{ID: "infra", Services: []model.Service{node}}}}, &fakeRunner{}, &countingPorts{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	s.sample(context.Background())
	return s, data, calls
}
func TestCleanupRetainsAndChecksCompleteReplicaIdentitySet(t *testing.T) {
	s, data, calls := cleanupResourceFixture(t, `[{"ID":"A","Service":"db","State":"running","Health":"healthy"},{"ID":"B","Service":"db","State":"running","Health":"healthy"}]`, "ephemeral", "managed")
	os.WriteFile(data, []byte(`[{"ID":"B","Service":"db","State":"running","Health":"healthy"},{"ID":"A","Service":"db","State":"running","Health":"healthy"}]`), 0600)
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatal("unchanged replicas refused", err)
	}
	raw, _ := os.ReadFile(calls)
	if !strings.Contains(string(raw), "stop --timeout 15 db") {
		t.Fatal("replicas not stopped", string(raw))
	}
}
func TestCleanupStopsUnhealthyEphemeralResourceAndRetainsExclusions(t *testing.T) {
	for _, tc := range []struct {
		lifetime, ownership string
		stopped             bool
	}{{"ephemeral", "managed", true}, {"persistent", "managed", false}, {"ephemeral", "observe", false}} {
		t.Run(tc.lifetime+tc.ownership, func(t *testing.T) {
			s, _, calls := cleanupResourceFixture(t, `[{"ID":"A","Service":"db","State":"running","Health":"unhealthy"}]`, tc.lifetime, tc.ownership)
			if s.entries["infra/db"].state != "unavailable" {
				t.Fatal("fixture not unhealthy")
			}
			if err := s.Cleanup(context.Background()); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(calls)
			if strings.Contains(string(raw), "stop --timeout 15 db") != tc.stopped {
				t.Fatal("physical running state ignored or exclusion broken", tc, string(raw))
			}
		})
	}
}
func TestCleanupRejectsChangedReplicaSetWithoutMutation(t *testing.T) {
	for _, rows := range []string{`[{"ID":"A","Service":"db","State":"running","Health":"healthy"},{"ID":"C","Service":"db","State":"running","Health":"healthy"}]`, `[{"ID":"A","Service":"db","State":"running","Health":"healthy"}]`, `[{"ID":"A","Service":"db","State":"running","Health":"healthy"},{"ID":"B","Service":"db","State":"running","Health":"healthy"},{"ID":"C","Service":"db","State":"running","Health":"healthy"}]`} {
		t.Run(rows, func(t *testing.T) {
			s, data, calls := cleanupResourceFixture(t, `[{"ID":"A","Service":"db","State":"running","Health":"healthy"},{"ID":"B","Service":"db","State":"running","Health":"healthy"}]`, "ephemeral", "managed")
			os.WriteFile(data, []byte(rows), 0600)
			if err := s.Cleanup(context.Background()); err == nil {
				t.Fatal("changed replica set accepted")
			}
			if raw, _ := os.ReadFile(calls); len(raw) != 0 {
				t.Fatal("changed replicas mutated", string(raw))
			}
		})
	}
}

func TestCleanupStopsResourceAfterFailedUnhealthyStart(t *testing.T) {
	s, data, calls := cleanupResourceFixture(t, `[]`, "ephemeral", "managed")
	root := filepath.Dir(data)
	script := `#!/bin/sh
case "$1" in info) printf '"cleanup-fixture-daemon"';exit;; stats) printf '';exit;; esac
case "$*" in
 *' config '*) printf '{"name":"cleanup-fixture"}';exit;;
 *' ps '*) cat "$SH_CLEANUP_ROWS";exit;;
 *' up '*) printf '[{"ID":"created-unhealthy","Service":"db","State":"running","Health":"unhealthy"}]' > "$SH_CLEANUP_ROWS";exit 1;;
esac
printf '%s\n' "$*" >> "$SH_CLEANUP_CALLS"
`
	os.WriteFile(filepath.Join(root, "docker"), []byte(script), 0700)
	if err := s.Start(context.Background(), []model.ServiceID{"infra/db"}); err == nil {
		t.Fatal("fixture failed start unexpectedly succeeded")
	}
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatal("failed but running resource not cleaned", err)
	}
	raw, _ := os.ReadFile(calls)
	if !strings.Contains(string(raw), "stop --timeout 15 db") {
		t.Fatal("failed start left unhealthy running container", string(raw))
	}
}

func TestCleanupReportsRunningResourceWithUnknownDaemonIdentity(t *testing.T) {
	for name, rows := range map[string]string{"single": `[{"ID":"running-with-unknown-daemon","Service":"db","State":"running","Health":"healthy"}]`, "unknown-exited-replica-before-running": `[{"ID":"exited-with-unknown-daemon","Service":"db","State":"exited"},{"ID":"running-with-unknown-daemon","Service":"db","State":"running","Health":"healthy"}]`} {
		t.Run(name, func(t *testing.T) {
			s, _, calls := cleanupResourceFixtureWithIdentity(t, rows, "ephemeral", "managed", false)
			if err := s.Cleanup(context.Background()); err == nil {
				t.Error("cleanup silently skipped known running resource with unknown identity")
			}
			if raw, _ := os.ReadFile(calls); len(raw) != 0 {
				t.Fatal("unknown identity authorized mutation", string(raw))
			}
		})
	}
}
