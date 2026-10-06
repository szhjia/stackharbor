package observe

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type metricReader struct{}

func (metricReader) List(context.Context) ([]process.Info, error) { return nil, nil }
func (metricReader) Read(ctx context.Context, pid int32) (process.Info, error) {
	if pid == 2 {
		return process.Info{}, fmt.Errorf("permission")
	}
	return process.Info{Identity: model.ProcessIdentity{PID: pid, CreatedMillis: 100}, MetricKnown: true, RSS: 4096, CPUSeconds: 1}, nil
}
func TestMetricUnknownFirstSamplePartialAndDedup(t *testing.T) {
	s := NewSampler(metricReader{})
	id := model.ProcessIdentity{PID: 1, CreatedMillis: 100}
	out, tool := s.Sample(context.Background(), map[model.ServiceID][]model.ProcessIdentity{"a/web": {id, {PID: 2, CreatedMillis: 100}}, "b/web": {id}}, model.ProcessIdentity{PID: 3, CreatedMillis: 100})
	a := out["a/web"]
	if !a.Known || !a.Partial || a.RSS != 4096 || a.CPUPercent != nil || tool.RSS != 4096 {
		t.Fatal(out, tool)
	}
	if out["b/web"].Known {
		t.Fatal("duplicate PID counted")
	}
}
func TestPortsPreserveOutputOnLsofExitOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lsof")
	if e := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'p123\\ncserver\\nn127.0.0.1:8080\\np456\\ncworker\\nn[::1]:9090\\n'\nexit 1\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir)
	observations := NewPortProbe().Observe(context.Background(), []model.Port{{Number: 8080}, {Number: 9090}}, nil)
	if len(observations) != 2 || observations[0].Status != "external" || len(observations[0].Listeners) != 1 {
		t.Fatal("valid nonzero lsof output discarded", observations)
	}

	ports, e := parsePorts([]byte("p123\ncserver\nn127.0.0.1:8080\np456\ncworker\nn[::1]:9090\n"))
	if e != nil || len(ports[8080]) != 1 || ports[9090][0].PID != 456 {
		t.Fatal(ports, e)
	}
}
func TestMissingLsofBindFallbackAndIPv6Conflict(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	free, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	freePort := free.Addr().(*net.TCPAddr).Port
	free.Close()
	if e := NewPortProbe().CheckStart(context.Background(), []model.Port{{Number: freePort}}); e != nil {
		t.Fatal("free bind fallback failed", e)
	}
	ipv6, e := net.Listen("tcp6", "[::1]:0")
	if e == nil {
		defer ipv6.Close()
		port := ipv6.Addr().(*net.TCPAddr).Port
		if e := NewPortProbe().CheckStart(context.Background(), []model.Port{{Number: port}}); e == nil {
			t.Fatal("IPv6 occupied port accepted")
		}
	}

	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	p := l.Addr().(*net.TCPAddr).Port
	if e = NewPortProbe().CheckStart(context.Background(), []model.Port{{Number: p}}); e == nil {
		t.Fatal("occupied port accepted")
	}
}
func TestHealthRejectsNonLoopbackRedirect(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "http://192.0.2.1/", 302)
	}))
	defer server.Close()
	ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	e := CheckHealth(ctx, model.ReadyProbe{HTTP: server.URL})
	if e == nil || requests.Load() != 1 {
		t.Fatal("external redirect accepted", e, requests.Load())
	}
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer ok.Close()
	if e = CheckHealth(ctx, model.ReadyProbe{HTTP: ok.URL}); e != nil {
		t.Fatal(e)
	}
}

func TestExternalListenerMetricsRemainReadOnlyAndExcludeForwarders(t *testing.T) {
	s := NewSampler(metricReader{})
	ctx := context.Background()
	ports := map[model.ServiceID][]model.PortObservation{"external/dev": {{Port: 8000, Status: "external", Listeners: []model.Listener{{PID: 1, Command: "node"}, {PID: 1, Command: "node"}}}}, "docker/dev": {{Port: 8001, Status: "external", Listeners: []model.Listener{{PID: 4, Command: "com.docker"}}}}}
	owned := map[model.ServiceID][]model.ProcessIdentity{}
	first, _, sources := s.SampleServices(ctx, owned, ports, model.ProcessIdentity{PID: 3, CreatedMillis: 100})
	if !first["external/dev"].Known || first["external/dev"].RSS != 4096 || first["external/dev"].CPUPercent != nil || sources["external/dev"] != "external" {
		t.Fatal(first, sources)
	}
	second, _, _ := s.SampleServices(ctx, owned, ports, model.ProcessIdentity{PID: 3, CreatedMillis: 100})
	if second["external/dev"].CPUPercent == nil || second["docker/dev"].Known || len(owned) != 0 {
		t.Fatal("external observation altered ownership or counted forwarding process", second, owned)
	}
}

type separateMetricReader struct{ memory, cpu bool }

func (r separateMetricReader) List(context.Context) ([]process.Info, error) { return nil, nil }
func (r separateMetricReader) Read(_ context.Context, pid int32) (process.Info, error) {
	return process.Info{Identity: model.ProcessIdentity{PID: pid, CreatedMillis: 100}, MemoryKnown: r.memory, CPUKnown: r.cpu, RSS: 8192, CPUSeconds: 2}, nil
}
func TestMemoryAndCPUFailuresDoNotHideEachOther(t *testing.T) {
	id := model.ProcessIdentity{PID: 10, CreatedMillis: 100}
	ctx := context.Background()
	for _, mode := range []separateMetricReader{{memory: true}, {cpu: true}} {
		s := NewSampler(mode)
		s.Sample(ctx, map[model.ServiceID][]model.ProcessIdentity{"a": {id}}, model.ProcessIdentity{})
		metrics, _ := s.Sample(ctx, map[model.ServiceID][]model.ProcessIdentity{"a": {id}}, model.ProcessIdentity{})
		m := metrics["a"]
		if m.Known != mode.memory || (m.CPUPercent != nil) != mode.cpu || !m.Partial {
			t.Fatal(mode, m)
		}
	}
}

type metricTreeReader struct{}

func (metricTreeReader) List(context.Context) ([]process.Info, error) {
	return []process.Info{{Identity: model.ProcessIdentity{PID: 11}, PPID: 10}, {Identity: model.ProcessIdentity{PID: 12}, PPID: 11}}, nil
}
func (metricTreeReader) Read(_ context.Context, pid int32) (process.Info, error) {
	parent := int32(0)
	if pid == 11 {
		parent = 10
	}
	if pid == 12 {
		parent = 11
	}
	return process.Info{Identity: model.ProcessIdentity{PID: pid, CreatedMillis: 100}, PPID: parent, MetricKnown: true, RSS: 4096, CPUSeconds: 1}, nil
}
func TestExternalListenerIncludesDescendantsAndReservesManagedProcesses(t *testing.T) {
	ctx := context.Background()
	ports := map[model.ServiceID][]model.PortObservation{"outside": {{Port: 8100, Status: "external", Listeners: []model.Listener{{PID: 10, Command: "node"}, {PID: 10, Command: "node"}}}}}
	for _, managed := range []bool{false, true} {
		owned := map[model.ServiceID][]model.ProcessIdentity{}
		if managed {
			owned["inside"] = []model.ProcessIdentity{{PID: 11, CreatedMillis: 100}, {PID: 12, CreatedMillis: 100}}
		}
		sampler := NewSampler(metricTreeReader{})
		out, _, source := sampler.SampleServices(ctx, owned, ports, model.ProcessIdentity{PID: 99, CreatedMillis: 100})
		want := uint64(3 * 4096)
		if managed {
			want = 4096
		}
		if out["outside"].RSS != want || source["outside"] != "external" {
			t.Fatal(out, source)
		}
		if managed && out["inside"].RSS != 8192 {
			t.Fatal("managed identities lost", out)
		}
	}
}

func TestPhysicalSamplesKeepOwnMetricsAndSharedIdentity(t *testing.T) {
	sampler := NewSampler(metricTreeReader{})
	ports := map[model.ServiceID][]model.PortObservation{"outside": {{Port: 8100, Status: "external", Listeners: []model.Listener{{PID: 10, Command: "node"}}}}}
	metrics, _, _, samples := sampler.SampleServicesDetailed(context.Background(), nil, ports, model.ProcessIdentity{PID: 99, CreatedMillis: 100})
	if metrics["outside"].RSS != 3*4096 || len(samples["outside"]) != 3 {
		t.Fatal(metrics, samples)
	}
	for _, sample := range samples["outside"] {
		if sample.Metric.RSS != 4096 || sample.Metric.SampledAt.IsZero() {
			t.Fatal("aggregate copied to physical process", sample)
		}
	}
}
