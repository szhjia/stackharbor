package docker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/model"
)

func TestContainerStatsMemoryUnitsAndIndependentFields(t *testing.T) {
	for _, tc := range []struct {
		name, cpu, memory string
		bytes             uint64
		memoryKnown       bool
		cpuValue          float64
		cpuKnown          bool
	}{
		{"live Docker format", "0.42%", "181.4MiB / 7.738GiB", 190211686, true, 0.42, true},
		{"zero is known", "0.00%", "0B / 8GiB", 0, true, 0, true},
		{"multiple cores", "125.50%", "128MiB / 8GiB", 134217728, true, 125.5, true},
		{"decimal units", "1%", "2.5MB / 8GB", 2500000, true, 1, true},
		{"small units", "1%", "796 KiB / 64 MiB", 815104, true, 1, true},
		{"gigabytes", "1%", "1.5GiB / 8GiB", 1610612736, true, 1, true},
		{"memory failure preserves CPU", "1.25%", "-- / --", 0, false, 1.25, true},
		{"CPU failure preserves memory", "--", "5.5MiB / 8GiB", 5767168, true, 0, false},
		{"nonfinite CPU", "NaN%", "1MB / 8GB", 1000000, true, 0, false},
		{"infinite CPU", "+Inf%", "1MB / 8GB", 1000000, true, 0, false},
		{"negative CPU", "-1%", "1MB / 8GB", 1000000, true, 0, false},
		{"unknown units", "0%", "5XB / 8GB", 0, false, 0, true},
		{"overflow", "0%", "999999999999999999999TiB / 8GB", 0, false, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metric, reason := parseMetric(tc.cpu, tc.memory)
			if metric.Known != tc.memoryKnown || metric.RSS != tc.bytes || (metric.CPUPercent != nil) != tc.cpuKnown {
				t.Fatalf("invalid metric: %+v, reason %q", metric, reason)
			}
			if tc.cpuKnown && *metric.CPUPercent != tc.cpuValue {
				t.Fatalf("CPU = %v, want %v", *metric.CPUPercent, tc.cpuValue)
			}
			if (reason == "") != (tc.memoryKnown && tc.cpuKnown) {
				t.Fatalf("invalid samples need an explanation: %q", reason)
			}
		})
	}
}

func TestContainerStatsScopesIDsAndMapsOutOfOrderSamples(t *testing.T) {
	m := &Manager{run: func(_ context.Context, args ...string) ([]byte, error) {
		want := []string{"stats", "--no-stream", "--no-trunc", "--format", "{{json .}}", "db-id", "redis-id"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("stats must target running IDs only: %v", args)
		}
		return []byte(`{"Container":"redis-id","ID":"redis-full-id","CPUPerc":"0%","MemUsage":"0B / 8GiB"}
{"Container":"db-id","ID":"db-full-id","CPUPerc":"2%","MemUsage":"128MiB / 8GiB"}`), nil
	}}
	rows := m.SampleMetrics(context.Background(), []model.DockerSnapshot{
		{ID: "db-id", State: "running"}, {ID: "redis-id", State: "running"},
		{ID: "stopped-id", State: "exited"}, {State: "absent"},
	})
	if !rows[0].Metric.Known || rows[0].Metric.RSS != 134217728 || *rows[0].Metric.CPUPercent != 2 || !rows[1].Metric.Known || rows[1].Metric.RSS != 0 || *rows[1].Metric.CPUPercent != 0 {
		t.Fatal("container identities or zero usage lost", rows)
	}
	if rows[2].Metric.Known || rows[3].Metric.Known {
		t.Fatal("stopped or absent container has metrics", rows)
	}
}

func TestContainerStatsNeverFallsBackToEntireEngine(t *testing.T) {
	m := &Manager{run: func(context.Context, ...string) ([]byte, error) {
		t.Fatal("no running IDs must skip stats")
		return nil, nil
	}}
	for _, rows := range [][]model.DockerSnapshot{nil, {{State: "running"}}, {{ID: "id", State: "exited"}}} {
		m.SampleMetrics(context.Background(), rows)
	}
}

func TestContainerStatsFailuresPreserveStatusAndClearStaleMetrics(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		err          error
	}{
		{"engine failure", "", errors.New("engine unreachable")},
		{"malformed JSON", "broken", nil},
		{"missing sample", "", nil},
		{"wrong identity", `{"Container":"other-id","CPUPerc":"0%","MemUsage":"1MiB / 8GiB"}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{run: func(context.Context, ...string) ([]byte, error) { return []byte(tc.output), tc.err }}
			input := []model.DockerSnapshot{{ID: "id", State: "running", Health: "healthy", Metric: model.Metric{Known: true, RSS: 123}}}
			row := m.SampleMetrics(context.Background(), input)[0]
			if row.State != "running" || row.Health != "healthy" || row.Metric.Known || row.Metric.CPUPercent != nil || !strings.Contains(row.MetricError, "unavailable") {
				t.Fatal("stats failure changed status or retained stale metrics", row)
			}
			if !input[0].Metric.Known {
				t.Fatal("sampling mutated its input snapshot")
			}
		})
	}
}

func TestContainerStatsHonorsCancellation(t *testing.T) {
	m := &Manager{run: func(ctx context.Context, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	row := m.SampleMetrics(ctx, []model.DockerSnapshot{{ID: "id", State: "running"}})[0]
	if row.Metric.Known || !strings.Contains(row.MetricError, "deadline exceeded") {
		t.Fatal("cancellation hidden", row)
	}
}
