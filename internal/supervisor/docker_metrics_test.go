package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szhjia/stackharbor/internal/model"
)

func TestDockerResourceMetricsReachServiceSnapshot(t *testing.T) {
	root := t.TempDir()
	compose := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(compose, []byte("services:\n  db: {image: postgres}\n  redis: {image: redis}\n  unrelated: {image: app}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "calls")
	statsFile := filepath.Join(root, "stats.json")
	if err := os.WriteFile(statsFile, []byte(`{"Container":"redis-id","ID":"redis-id","CPUPerc":"0.00%","MemUsage":"0B / 8GiB"}
{"Container":"db-id","ID":"db-id","CPUPerc":"125.50%","MemUsage":"128MiB / 8GiB"}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$SH_METRIC_CALLS"
case "$1" in
  compose) printf '%s\n' '[{"ID":"db-id","Service":"db","State":"running","Health":"healthy"},{"ID":"redis-id","Service":"redis","State":"running","Health":"healthy"},{"ID":"other-id","Service":"unrelated","State":"running"}]' ;;
  stats) cat "$SH_METRIC_DATA" ;;
esac
`
	if err := os.WriteFile(filepath.Join(root, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SH_METRIC_CALLS", calls)
	t.Setenv("SH_METRIC_DATA", statsFile)
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	nodes := []model.Service{}
	for _, name := range []string{"db", "redis"} {
		nodes = append(nodes, model.Service{ID: model.ServiceID("resource/" + name), ProjectID: "infra", Kind: "resource", Name: name, Resource: &model.ResourceSpec{File: compose, Project: "wordverse", Service: name, Available: "healthy", Control: "observe", Lifetime: "persistent"}})
	}
	s, err := newManuallySampledSession(model.Workspace{Root: root, Projects: []model.Project{{ID: "infra", Services: nodes}}}, &fakeRunner{}, &countingPorts{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	s.sample(context.Background())
	snap := s.Snapshot()
	for _, v := range snap.Services {
		if v.State != "available" || v.MetricSource != "docker" || !v.Metric.Known || v.Metric.CPUPercent == nil {
			t.Fatalf("available Docker resource has no metrics: %+v", v)
		}
		if v.Spec.ID == "resource/db" && (v.Metric.RSS != 134217728 || *v.Metric.CPUPercent != 125.5) {
			t.Fatalf("database metrics mapped incorrectly: %+v", v.Metric)
		}
		if v.Spec.ID == "resource/redis" && (v.Metric.RSS != 0 || *v.Metric.CPUPercent != 0) {
			t.Fatalf("zero metrics must be known: %+v", v.Metric)
		}
	}
	raw, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	stats := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "stats ") {
			stats++
			if !strings.Contains(line, "db-id") || !strings.Contains(line, "redis-id") || strings.Contains(line, "other-id") {
				t.Fatal("stats escaped registered container scope", line)
			}
		}
	}
	if stats != 1 {
		t.Fatal("shared Compose resources must use one batched stats call", string(raw))
	}
	if err := os.WriteFile(statsFile, []byte(`{"Container":"redis-id","CPUPerc":"1.5%","MemUsage":"10MiB / 8GiB"}
{"Container":"db-id","CPUPerc":"5%","MemUsage":"256MiB / 8GiB"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s.sample(context.Background())
	for _, v := range s.Snapshot().Services {
		if v.Spec.ID == "resource/db" && (v.Metric.RSS != 268435456 || v.Metric.CPUPercent == nil || *v.Metric.CPUPercent != 5) {
			t.Fatal("second sample did not update database usage", v.Metric)
		}
	}
	// A failed subsequent sample must not leave the previous values displayed.
	if err := os.WriteFile(statsFile, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	s.sample(context.Background())
	for _, v := range s.Snapshot().Services {
		if v.State != "available" || v.Metric.Known || v.Metric.CPUPercent != nil || v.MetricError == "" {
			t.Fatal("failed stats changed availability or retained stale usage", v)
		}
	}
}
