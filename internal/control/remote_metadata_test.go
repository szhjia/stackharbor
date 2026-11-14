package control

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/szhjia/stackharbor/internal/model"
)

func TestProjectionRetainsRemoteObservationAndSelectionMetadata(t *testing.T) {
	spec := model.Service{ID: "app/api", Version: 2, Env: map[string]string{"TOKEN": "secret-observation"}}
	snapshot := ProjectSnapshot(Identity{}, model.Snapshot{DockerFile: "compose.yaml", Services: []model.ServiceSnapshot{{Spec: spec, State: "stopped", ObservedState: "running", ObservedReason: "secret-observation is listening", ReadinessChecked: true}}})
	if !snapshot.ComposeConfigured || len(snapshot.Nodes) != 1 {
		t.Fatal(snapshot)
	}
	node := snapshot.Nodes[0]
	if node.Version != 2 || node.AllActions == nil || !*node.AllActions || node.ObservedState != "running" || !node.ReadinessChecked || node.ObservedReason != "[redacted] is listening" {
		t.Fatal(node)
	}
	data, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(data), "secret-observation") || !strings.Contains(string(data), `"all_actions":true`) {
		t.Fatal(string(data), err)
	}
	no := false
	spec.AllActions = &no
	snapshot = ProjectSnapshot(Identity{}, model.Snapshot{Services: []model.ServiceSnapshot{{Spec: spec}}})
	if snapshot.Nodes[0].AllActions == nil || *snapshot.Nodes[0].AllActions {
		t.Fatal("exclusion not represented", snapshot)
	}
}
