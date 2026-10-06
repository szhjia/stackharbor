package supervisor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/szhjia/stackharbor/internal/model"
)

type externalPorts struct{ port int }

func (p externalPorts) Observe(context.Context, []model.Port, []model.ProcessIdentity) []model.PortObservation {
	return []model.PortObservation{{Port: p.port, Status: "external", Listeners: []model.Listener{{PID: 123, Command: "ssh", Address: "127.0.0.1:" + strconv.Itoa(p.port)}}}}
}
func (externalPorts) CheckStart(context.Context, []model.Port) error { return nil }

func TestExternalDockerEndpointGetsMetricsWithoutOwnership(t *testing.T) {
	root := t.TempDir()
	compose := filepath.Join(root, "compose.yaml")
	os.WriteFile(compose, []byte("services:\n  db: {image: postgres}\n  api: {image: app}\n  unrelated: {image: app}\n"), 0600)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	port, _ := strconv.Atoi(strings.Split(server.URL, ":")[2])
	calls := filepath.Join(root, "calls")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$SH_METRIC_CALLS"
case "$1" in
 compose) printf '%s\n' '[{"ID":"db-id","Service":"db","State":"running","Health":"healthy"},{"ID":"api-id","Service":"api","State":"running","Health":"healthy","Publishers":[{"URL":"127.0.0.1","PublishedPort":` + strconv.Itoa(port) + `,"TargetPort":6210,"Protocol":"tcp"}]},{"ID":"unrelated-id","Service":"unrelated","State":"running"}]' ;;
 stats) printf '%s\n' '{"Container":"db-id","CPUPerc":"0%","MemUsage":"128MiB / 8GiB"}' '{"Container":"api-id","CPUPerc":"2.5%","MemUsage":"24MiB / 8GiB"}' ;;
esac
`
	os.WriteFile(filepath.Join(root, "docker"), []byte(script), 0700)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SH_METRIC_CALLS", calls)
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	db := model.Service{ID: "resource/db", ProjectID: "infra", Kind: "resource", Resource: &model.ResourceSpec{File: compose, Project: "wordverse", Service: "db", Available: "healthy", Control: "observe", Lifetime: "persistent"}}
	api := model.Service{ID: "api/dev", ProjectID: "api", Ports: []model.Port{{Number: port}}, Ready: &model.ReadyProbe{HTTP: server.URL}}
	s, err := newManuallySampledSession(model.Workspace{Root: root, Projects: []model.Project{{ID: "infra", Services: []model.Service{db}}, {ID: "api", Services: []model.Service{api}}}}, &fakeRunner{}, externalPorts{port}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	s.sample(context.Background())
	for _, v := range s.Snapshot().Services {
		if v.Spec.ID != "api/dev" {
			continue
		}
		if !v.Metric.Known || v.Metric.RSS != 24*1048576 || v.Metric.CPUPercent == nil || *v.Metric.CPUPercent != 2.5 {
			t.Fatalf("healthy external Docker endpoint has no container metrics: %+v", v)
		}
		if v.ObservedState != "running" {
			t.Fatal("external readiness missing", v)
		}
		if v.State != "stopped" || len(v.Owned) != 0 {
			t.Fatal("observing endpoint must not adopt ownership", v)
		}
	}
	raw, _ := os.ReadFile(calls)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "stats ") && strings.Contains(line, "unrelated-id") {
			t.Fatal("sample escaped associated container scope", line)
		}
	}
}

func TestDockerEndpointAssociationRejectsAmbiguityAndHostProcesses(t *testing.T) {
	specs := map[model.ServiceID]model.Service{"app/dev": {ID: "app/dev"}}
	ports := map[model.ServiceID][]model.PortObservation{"app/dev": {{Port: 6210, Status: "external", Listeners: []model.Listener{{PID: 123, Command: "ssh", Address: "127.0.0.1:6210"}}}}}
	row := model.DockerSnapshot{ID: "api-id", State: "running", PublishedEndpoints: []model.PublishedEndpoint{{Host: "127.0.0.1", Port: 6210}}}
	for _, tc := range []struct {
		name    string
		rows    []model.DockerSnapshot
		owned   map[model.ServiceID][]model.ProcessIdentity
		command string
		want    bool
	}{
		{"single", []model.DockerSnapshot{row}, nil, "ssh", true},
		{"ambiguous", []model.DockerSnapshot{row, {ID: "other-id", State: "running", PublishedEndpoints: []model.PublishedEndpoint{{Host: "127.0.0.1", Port: 6210}}}}, nil, "ssh", false},
		{"native", []model.DockerSnapshot{row}, nil, "node", false},
		{"owned", []model.DockerSnapshot{row}, map[model.ServiceID][]model.ProcessIdentity{"app/dev": {{PID: 123}}}, "ssh", false},
		{"stopped", []model.DockerSnapshot{{ID: "api-id", State: "exited", PublishedEndpoints: []model.PublishedEndpoint{{Host: "127.0.0.1", Port: 6210}}}}, nil, "ssh", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ports["app/dev"][0].Listeners[0].Command = tc.command
			_, ok := externalDockerServices(specs, ports, tc.owned, tc.rows)["app/dev"]
			if ok != tc.want {
				t.Fatalf("incorrect association: %v", ok)
			}
		})
	}
}

func TestExternalReadinessDoesNotTreatListenerAsHealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	specs := map[model.ServiceID]model.Service{"app/dev": {ID: "app/dev", Ready: &model.ReadyProbe{HTTP: server.URL}}}
	ports := map[model.ServiceID][]model.PortObservation{"app/dev": {{Status: "external"}}}
	states, reasons := observeExternalServices(context.Background(), specs, ports, nil)
	if states["app/dev"] != "unready" || !strings.Contains(reasons["app/dev"], "503") {
		t.Fatal(states, reasons)
	}
	ports["app/dev"][0].Status = "free"
	states, _ = observeExternalServices(context.Background(), specs, ports, nil)
	if states["app/dev"] != "" {
		t.Fatal("free port incorrectly ready", states)
	}
	ports["app/dev"][0].Status = "unknown"
	states, _ = observeExternalServices(context.Background(), specs, ports, nil)
	if states["app/dev"] != "unknown" {
		t.Fatal("unknown observation incorrectly stopped", states)
	}
}

func TestPublishedListenerRequiresCompatibleBinding(t *testing.T) {
	for _, tc := range []struct {
		host, address string
		want          bool
	}{
		{"127.0.0.1", "127.0.0.1:6210", true},
		{"127.0.0.2", "127.0.0.1:6210", false},
		{"0.0.0.0", "127.0.0.1:6210", true},
		{"::1", "127.0.0.1:6210", false},
		{"::", "127.0.0.1:6210", false},
		{"::1", "[::1]:6210", true},
		{"127.0.0.1", "127.0.0.1:6214", false},
		{"127.0.0.1", "", false},
		{"127.0.0.1", "*:6210", false},
		{"::1", "*:6210", false},
		{"", "127.0.0.1:6210", false},
	} {
		if got := publishedListenerMatches(model.PublishedEndpoint{Host: tc.host, Port: 6210}, model.Listener{Address: tc.address}); got != tc.want {
			t.Fatalf("%s / %s: %v", tc.host, tc.address, got)
		}
	}
}
