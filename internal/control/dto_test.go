package control

import (
	"encoding/json"
	"github.com/szhjia/stackharbor/internal/model"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestProjectSnapshotRedactsConfiguration(t *testing.T) {
	secret := "env-super-secret"
	command := []string{"server", "--password=command-secret", "--private-file=/private/secret-config"}
	internal := model.Snapshot{
		Root: "/private/root", Projects: []model.Project{{Services: []model.Service{{Env: map[string]string{"TOKEN": secret}}}}},
		Candidates: []model.Candidate{{SuggestedCommand: command}},
		Services: []model.ServiceSnapshot{
			{Spec: model.Service{ID: "app/web", Kind: "service", Command: command, Env: map[string]string{"TOKEN": secret}}, State: "running", Reason: secret + " " + strings.Join(command, " "), Owned: []model.ProcessIdentity{{PID: 42, CreatedMillis: 1234}}},
			{Spec: model.Service{ID: "app/external", Kind: "service", Control: "observe", Identity: &model.ObservedIdentity{Command: command}}, State: "running", Ports: []model.PortObservation{{Port: 80, Listeners: []model.Listener{{PID: 43, Command: strings.Join(command, " ")}}}}},
			{Spec: model.Service{ID: "app/database", Kind: "resource", Resource: &model.ResourceSpec{Control: "observe"}}, State: "available"},
			{Spec: model.Service{ID: "app/task", Kind: "task", Task: &model.TaskSpec{}}, State: "succeeded"},
		},
		Docker:      []model.DockerSnapshot{{ID: "container", Service: "app/database", State: "running", Reason: secret}},
		Diagnostics: []model.Diagnostic{{Severity: "error", Message: secret + " " + strings.Join(command, " ")}},
	}
	identity := Identity{WorkspaceID: "workspace", SessionID: "session", ProtocolVersion: 1, Capabilities: []string{"snapshot"}}
	got := ProjectSnapshot(identity, internal)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, "command-secret", "/private/secret-config", "SuggestedCommand", "SourceFile", `"Env"`, `"Command"`, "/private/root"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("configuration leaked %q in %s", forbidden, raw)
		}
	}
	if len(got.Nodes) != 4 || got.Identity.SessionID != "session" {
		t.Fatalf("bad projection: %+v", got)
	}
	if got.Nodes[0].Metric.Known || got.Nodes[0].Metric.RSSBytes != nil || got.Nodes[0].Metric.CPUPercent != nil || got.Nodes[0].Metric.UptimeMillis != nil {
		t.Fatal("unknown metric became zero")
	}
	if slices.Contains(got.Nodes[1].AllowedActions, "stop") || slices.Contains(got.Nodes[2].AllowedActions, "stop") || slices.Contains(got.Nodes[2].AllowedActions, "restart") {
		t.Fatal("read-only node has mutation capability")
	}
	if !slices.Equal(got.Nodes[3].AllowedActions, []string{"start"}) {
		t.Fatalf("completed task actions: %v", got.Nodes[3].AllowedActions)
	}
	if got.ObservedAt.Location() != time.UTC {
		t.Fatal("observed_at is not UTC")
	}
	if len(got.Processes) != 2 || got.Processes[1].CreatedUnixMillis != nil {
		t.Fatal("invented external process identity")
	}
	if got.Processes[0].Metric.Known {
		t.Fatal("aggregate metric copied to physical process")
	}
}

func TestProjectSnapshotCopiesMetricAndIdentity(t *testing.T) {
	cpu := 12.5
	internal := model.Snapshot{Services: []model.ServiceSnapshot{{Spec: model.Service{ID: "app/web"}, Owned: []model.ProcessIdentity{{PID: 42, CreatedMillis: 1234}}, Metric: model.Metric{Known: true, Partial: true, RSS: 123, CPUPercent: &cpu, Uptime: 1500 * time.Millisecond}}}}
	identity := Identity{Capabilities: []string{"snapshot"}}
	got := ProjectSnapshot(identity, internal)
	cpu = 99
	identity.Capabilities[0] = "changed"
	metric := got.Nodes[0].Metric
	if !metric.Known || !metric.Partial || metric.RSSBytes == nil || *metric.RSSBytes != 123 || metric.CPUPercent == nil || *metric.CPUPercent != 12.5 || metric.UptimeMillis == nil || *metric.UptimeMillis != 1500 || got.Identity.Capabilities[0] != "snapshot" {
		t.Fatalf("lost metric or aliased input: %+v", got)
	}
}

func TestProjectSnapshotKeepsPhysicalMetricsUnknownAndDeduplicatesExactIdentity(t *testing.T) {
	internal := model.Snapshot{Services: []model.ServiceSnapshot{
		{Spec: model.Service{ID: "app/first"}, Owned: []model.ProcessIdentity{{PID: 42, CreatedMillis: 100}}, Metric: model.Metric{Known: true, RSS: 1000}},
		{Spec: model.Service{ID: "app/shared"}, Owned: []model.ProcessIdentity{{PID: 42, CreatedMillis: 100}}},
		{Spec: model.Service{ID: "app/reused"}, Owned: []model.ProcessIdentity{{PID: 42, CreatedMillis: 200}}},
	}}
	got := ProjectSnapshot(Identity{}, internal)
	if len(got.Processes) != 2 || got.Nodes[0].ResourceRefs[0] != got.Nodes[1].ResourceRefs[0] || got.Nodes[0].ResourceRefs[0] == got.Nodes[2].ResourceRefs[0] {
		t.Fatalf("incorrect process identity merge: %+v", got)
	}
	for _, p := range got.Processes {
		if p.Metric.Known || p.Metric.RSSBytes != nil {
			t.Fatal("duplicated aggregate service metric as per-process metric")
		}
	}
}

func TestProjectSnapshotRedactsSensitiveOptionValues(t *testing.T) {
	args := []string{"server", "--password=command-secret", "--token", "separate-token-secret", "--api-key=api-key-secret", "--client_secret", "client-secret-value", "--mode=production"}
	text := "command-secret separate-token-secret api-key-secret client-secret-value production"
	internal := model.Snapshot{Services: []model.ServiceSnapshot{{Spec: model.Service{ID: "app/web", Command: args}, Reason: text, MetricError: text}}, Diagnostics: []model.Diagnostic{{Message: text}}}
	raw, err := json.Marshal(ProjectSnapshot(Identity{}, internal))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "production") {
		t.Fatal("non-sensitive option value was over-redacted")
	}
	for _, secret := range []string{"command-secret", "separate-token-secret", "api-key-secret", "client-secret-value"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("sensitive option value leaked %q: %s", secret, raw)
		}
	}
}

func TestProjectSnapshotPreservesIndependentCPUAndSampleAge(t *testing.T) {
	at := time.Unix(100, 0).UTC()
	cpu := 2.5
	internal := model.Snapshot{ObservedAt: at, Services: []model.ServiceSnapshot{{Spec: model.Service{ID: "app/a"}, Owned: []model.ProcessIdentity{{PID: 1, CreatedMillis: 2}}, Processes: []model.ProcessSample{{Identity: model.ProcessIdentity{PID: 1, CreatedMillis: 2}, Metric: model.Metric{CPUPercent: &cpu, SampledAt: at}}}, Metric: model.Metric{Known: true, RSS: 999}}}}
	got := ProjectSnapshot(Identity{}, internal)
	if !got.ObservedAt.Equal(at) || got.Processes[0].Metric.SampledAt == nil || !got.Processes[0].Metric.SampledAt.Equal(at) || got.Processes[0].Metric.CPUPercent == nil || *got.Processes[0].Metric.CPUPercent != cpu || got.Processes[0].Metric.RSSBytes != nil {
		t.Fatalf("sample age or CPU-only metric lost: %+v", got.Processes)
	}
}

func TestProjectSnapshotReleaseActionRequiresExternalDeclaredPort(t *testing.T) {
	for _, tc := range []struct {
		kind, control, status string
		want                  bool
	}{{"service", "", "external", true}, {"task", "", "external", true}, {"service", "", "owned", false}, {"service", "", "unknown", false}, {"service", "observe", "external", false}, {"resource", "", "external", false}} {
		s := model.ServiceSnapshot{Spec: model.Service{ID: "app/api", Kind: tc.kind, Control: tc.control}, State: "stopped", Ports: []model.PortObservation{{Port: 8080, Status: tc.status}}}
		got := ProjectSnapshot(Identity{}, model.Snapshot{Services: []model.ServiceSnapshot{s}}).Nodes[0].AllowedActions
		if slices.Contains(got, "release") != tc.want {
			t.Errorf("%+v actions %v", tc, got)
		}
	}
}
