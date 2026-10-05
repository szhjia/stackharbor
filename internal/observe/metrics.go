package observe

import (
	"context"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"sort"
	"strings"
	"sync"
	"time"
)

type sample struct {
	cpu float64
	at  time.Time
}
type Sampler struct {
	mu       sync.Mutex
	reader   process.Reader
	previous map[model.ProcessIdentity]sample
}

func NewSampler(r process.Reader) *Sampler {
	return &Sampler{reader: r, previous: map[model.ProcessIdentity]sample{}}
}
func (s *Sampler) Sample(ctx context.Context, owned map[model.ServiceID][]model.ProcessIdentity, tool model.ProcessIdentity) (map[model.ServiceID]model.Metric, model.Metric) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	next := map[model.ProcessIdentity]sample{}
	seen := map[model.ProcessIdentity]bool{}
	measure := func(ids []model.ProcessIdentity) model.Metric {
		m := model.Metric{}
		cpu := 0.0
		cpuKnown := true
		for _, id := range ids {
			if seen[id] {
				m.Partial = true
				cpuKnown = false
				continue
			}
			seen[id] = true
			p, e := s.reader.Read(ctx, id.PID)
			if e != nil || p.Identity != id || !process.Live(p) {
				m.Partial = true
				cpuKnown = false
				continue
			}
			memoryKnown := p.MetricKnown || p.MemoryKnown
			if memoryKnown {
				m.Known = true
				m.RSS += p.RSS
			} else {
				m.Partial = true
			}
			up := now.Sub(time.UnixMilli(id.CreatedMillis))
			if up > m.Uptime {
				m.Uptime = up
			}
			if !p.MetricKnown && !p.CPUKnown {
				m.Partial = true
				cpuKnown = false
				continue
			}
			at := time.Now()
			old, ok := s.previous[id]
			if !ok {
				cpuKnown = false
			} else if dt := at.Sub(old.at).Seconds(); dt > 0 {
				delta := p.CPUSeconds - old.cpu
				if delta >= 0 {
					cpu += delta / dt * 100
				} else {
					cpuKnown = false
				}
			}
			next[id] = sample{p.CPUSeconds, at}
		}
		if len(ids) > 0 && cpuKnown {
			m.CPUPercent = &cpu
		}
		return m
	}
	toolMetric := measure([]model.ProcessIdentity{tool})
	ids := []model.ServiceID{}
	for id := range owned {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := map[model.ServiceID]model.Metric{}
	for _, id := range ids {
		out[id] = measure(owned[id])
	}
	s.previous = next
	return out, toolMetric
}

// External listener trees are read-only observations and never enter runner ownership.
func (s *Sampler) SampleServices(ctx context.Context, owned map[model.ServiceID][]model.ProcessIdentity, ports map[model.ServiceID][]model.PortObservation, tool model.ProcessIdentity) (map[model.ServiceID]model.Metric, model.Metric, map[model.ServiceID]string) {
	observed := map[model.ServiceID][]model.ProcessIdentity{}
	sources := map[model.ServiceID]string{}
	partial := map[model.ServiceID]bool{}
	reserved := map[int32]bool{tool.PID: true}
	for id, ps := range owned {
		observed[id] = append([]model.ProcessIdentity{}, ps...)
		if len(ps) > 0 {
			sources[id] = "session"
		}
		for _, p := range ps {
			reserved[p.PID] = true
		}
	}
	external := false
	for _, ps := range ports {
		for _, p := range ps {
			if p.Status == "external" {
				external = true
			}
		}
	}
	topology := []process.Info{}
	var topologyErr error
	if external {
		topology, topologyErr = s.reader.List(ctx)
	}
	for id, ps := range ports {
		if len(observed[id]) > 0 {
			continue
		}
		roots := map[int32]model.ProcessIdentity{}
		for _, p := range ps {
			if p.Status != "external" {
				continue
			}
			for _, l := range p.Listeners {
				if reserved[l.PID] {
					continue
				}
				if IsForwarder(l.Command) {
					sources[id] = "forwarder"
					continue
				}
				v, err := s.reader.Read(ctx, l.PID)
				if err != nil || !process.Live(v) {
					partial[id] = true
					continue
				}
				roots[l.PID] = v.Identity
				sources[id] = "external"
			}
		}
		if len(roots) == 0 {
			continue
		}
		for changed := true; changed; {
			changed = false
			for _, p := range topology {
				pid := p.Identity.PID
				if _, ok := roots[pid]; ok || reserved[pid] {
					continue
				}
				if _, ok := roots[p.PPID]; !ok {
					continue
				}
				v, err := s.reader.Read(ctx, pid)
				if err != nil {
					partial[id] = true
					continue
				}
				if v.PPID != p.PPID || !process.Live(v) {
					continue
				}
				roots[pid] = v.Identity
				changed = true
			}
		}
		if topologyErr != nil {
			partial[id] = true
		}
		for _, p := range roots {
			observed[id] = append(observed[id], p)
		}
		sort.Slice(observed[id], func(i, j int) bool { return observed[id][i].PID < observed[id][j].PID })
	}
	metrics, self := s.Sample(ctx, observed, tool)
	for id, incomplete := range partial {
		if incomplete {
			v := metrics[id]
			v.Partial = true
			metrics[id] = v
		}
	}
	return metrics, self, sources
}

func IsForwarder(command string) bool {
	name := strings.ToLower(command)
	for _, forwarder := range []string{"docker", "colima", "com.dock", "gvproxy", "qemu", "ssh"} {
		if strings.Contains(name, forwarder) {
			return true
		}
	}
	return false
}
