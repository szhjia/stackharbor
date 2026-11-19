package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/szhjia/stackharbor/internal/graph"
	"github.com/szhjia/stackharbor/internal/model"
)

func TestDaemonPreviewUsesGlobalStartupScope(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STACKHARBOR_ENGINE_RUNTIME_DIR", filepath.Join(root, "engine"))
	files := map[string]string{
		"stackharbor.workspace.yaml": `version: 2
root: .
discover: false
registrations: [stackharbor.yaml]
resources:
  db:
    adapter: existing-container
    project: fixture
    service: db
    available: healthy
    control: managed
    lifetime: persistent
    all_actions: true
  backup:
    adapter: existing-container
    project: fixture
    service: backup
    available: running
    control: managed
    lifetime: persistent
`,
		"stackharbor.yaml": `version: 2
project: {id: app, name: App}
services:
  api:
    run: {command: [unused]}
    requires: [{node: app/service/worker, condition: started}]
  worker:
    run: {command: [unused]}
    all_actions: false
  manual:
    run: {command: [unused]}
    all_actions: false
`,
	}
	for name, config := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, flag := range []string{"-d", "--daemon"} {
		var out, errs bytes.Buffer
		args := []string{"--root", root, flag, "--dry-run", "--json"}
		code := Run(context.Background(), args, strings.NewReader(""), &out, &errs)
		var plan graph.Plan
		if err := json.Unmarshal(out.Bytes(), &plan); code != 0 || err != nil {
			t.Fatalf("%s: code %d, output %s, error %s, decode %v", flag, code, out.String(), errs.String(), err)
		}
		wantTargets := []model.ServiceID{"resource/db", "app/service/api"}
		if !reflect.DeepEqual(plan.Targets, wantTargets) {
			t.Fatalf("%s: targets %v, want %v", flag, plan.Targets, wantTargets)
		}
		var actions []model.ServiceID
		for _, action := range plan.Actions {
			actions = append(actions, action.ID)
		}
		wantActions := []model.ServiceID{"resource/db", "app/service/worker", "app/service/api"}
		if !reflect.DeepEqual(actions, wantActions) {
			t.Fatalf("%s: actions %v, want %v", flag, actions, wantActions)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "engine")); !os.IsNotExist(err) {
		t.Fatalf("preview accessed the engine directory: %v", err)
	}
}

func TestDaemonRejectsTargetOrUnconfirmedNoninteractiveStart(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"-d", "--target", "app/web", "--dry-run"}, "--target is not supported"},
		{[]string{"--daemon"}, "Noninteractive mutation requires --yes"},
	} {
		var out, errs bytes.Buffer
		code := Run(context.Background(), test.args, strings.NewReader(""), &out, &errs)
		if code != 2 || !strings.Contains(errs.String(), test.want) {
			t.Fatalf("%v: code %d, output %q, error %q", test.args, code, out.String(), errs.String())
		}
	}
}
