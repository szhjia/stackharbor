package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSharedDockerSamplingUsesWorkspaceExecutables(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "calls")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$CALLS"
case "$1" in
 info) printf '"fixture-daemon"\n' ;;
 stats) shift 5; for id do printf '{"Container":"%s","CPUPerc":"1%%","MemUsage":"2MiB / 8GiB"}\n' "$id"; done ;;
 compose)
  project=""; previous=""
  for arg do if [ "$previous" = "-p" ]; then project="$arg"; fi; previous="$arg"; done
  printf '[{"ID":"%s-id","Service":"db","State":"running"}]\n' "$project"
  ;;
 *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cache := NewObservationCache(func() time.Duration { return time.Hour })
	managers := []*Manager{}
	for _, project := range []string{"first", "second"} {
		workspace := filepath.Join(root, project)
		if err := os.Mkdir(workspace, 0700); err != nil {
			t.Fatal(err)
		}
		compose := filepath.Join(workspace, "compose.yaml")
		if err := os.WriteFile(compose, []byte("services: {db: {image: postgres}}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		manager := NewScoped(workspace, compose, project)
		manager.SetEnvironment([]string{"PATH=" + bin, "CALLS=" + calls})
		manager.SetObservationCache(cache)
		managers = append(managers, manager)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prepared := cache.Prepare(ctx, managers)
	for _, manager := range managers {
		rows, reason := manager.Observe(prepared)
		if reason != "" {
			t.Fatal(reason)
		}
		rows = manager.SampleMetrics(prepared, rows)
		if len(rows) != 1 || !rows[0].Metric.Known {
			t.Fatalf("workspace metric missing: %+v", rows)
		}
	}
	raw, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	stats, info := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.HasPrefix(line, "stats ") {
			stats++
			if !strings.HasSuffix(line, "first-id second-id") {
				t.Fatalf("not a targeted union stats command: %q", line)
			}
		}
		if strings.HasPrefix(line, "info ") {
			info++
		}
	}
	if stats != 1 || info != 1 {
		t.Fatalf("engine context was not shared: stats=%d info=%d calls=%s", stats, info, raw)
	}
}
