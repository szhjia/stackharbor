package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/szhjia/stackharbor/internal/model"
)

// SampleMetrics reads only the observed running container IDs. Keep it separate
// from Observe: readiness checks need status, not the slower stats sample.
func (m *Manager) SampleMetrics(ctx context.Context, rows []model.DockerSnapshot) []model.DockerSnapshot {
	rows = append([]model.DockerSnapshot(nil), rows...)
	ids := []string{}
	seen := map[string]bool{}
	for i := range rows {
		rows[i].Metric = model.Metric{}
		rows[i].MetricError = ""
		if rows[i].State == "running" && rows[i].ID != "" && !seen[rows[i].ID] {
			ids = append(ids, rows[i].ID)
			seen[rows[i].ID] = true
		}
	}
	// With no IDs, docker stats would sample every container on the engine.
	if len(ids) == 0 {
		return rows
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	args := append([]string{"stats", "--no-stream", "--no-trunc", "--format", "{{json .}}"}, ids...)
	raw, err := m.run(ctx, args...)
	metrics, metricErrors := map[string]model.Metric{}, map[string]string{}
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(raw))
		for {
			var stat struct{ Container, ID, CPUPerc, MemUsage string }
			if err = dec.Decode(&stat); err != nil {
				if err == io.EOF {
					err = nil
				}
				break
			}
			id := stat.Container
			if id == "" {
				id = stat.ID
			}
			metric, reason := parseMetric(stat.CPUPerc, stat.MemUsage)
			metrics[id], metricErrors[id] = metric, reason
		}
	}
	for i := range rows {
		row := &rows[i]
		if row.State != "running" || row.ID == "" {
			continue
		}
		if metric, ok := metrics[row.ID]; ok {
			row.Metric, row.MetricError = metric, metricErrors[row.ID]
		} else if err != nil {
			row.MetricError = "Docker metrics unavailable: " + err.Error()
		} else {
			row.MetricError = "Docker metrics unavailable: no sample for container"
		}
	}
	return rows
}

var memoryPattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*(B|kB|KB|MB|GB|TB|KiB|MiB|GiB|TiB)$`)

// Docker's memory usage is its CLI working-set value (cache subtracted on
// Linux), not host process RSS. The shared metric carries the byte count.
func parseMetric(cpu, memory string) (model.Metric, string) {
	metric := model.Metric{SampledAt: time.Now().UTC()}
	reasons := []string{}
	usage := strings.TrimSpace(strings.SplitN(memory, "/", 2)[0])
	parts := memoryPattern.FindStringSubmatch(usage)
	if len(parts) == 3 {
		value, err := strconv.ParseFloat(parts[1], 64)
		unit := map[string]float64{"B": 1, "kB": 1e3, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "KiB": 1024, "MiB": 1048576, "GiB": 1073741824, "TiB": 1099511627776}[parts[2]]
		value *= unit
		if err == nil && !math.IsInf(value, 0) && value < math.Exp2(64) {
			metric.Known, metric.RSS = true, uint64(value)
		}
	}
	if !metric.Known {
		reasons = append(reasons, "memory sample invalid")
	}
	cpu = strings.TrimSpace(cpu)
	if strings.HasSuffix(cpu, "%") {
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(cpu, "%")), 64)
		if err == nil && value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			metric.CPUPercent = &value
		}
	}
	if metric.CPUPercent == nil {
		reasons = append(reasons, "CPU sample invalid")
	}
	if len(reasons) > 0 {
		return metric, fmt.Sprintf("Docker metrics unavailable: %s", strings.Join(reasons, "; "))
	}
	return metric, ""
}
