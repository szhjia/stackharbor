package docker

import (
	"context"
	"github.com/szhjia/stackharbor/internal/model"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestComposeDiscoveryAndScopedControl(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services:\n  db:\n    image: postgres\n    container_name: local-db\n  migrate:\n    image: app\n    depends_on: [db]\n"), 0600)
	m := New(root)
	if len(m.Specs()) != 2 {
		t.Fatal(m.Specs())
	}
	var args []string
	m.run = func(_ context.Context, a ...string) ([]byte, error) { args = a; return []byte{}, nil }
	if err := m.Action(context.Background(), "start", []string{"db"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"compose", "-f", filepath.Join(root, "compose.yaml"), "up", "-d", "--no-deps", "--no-build", "--wait", "--wait-timeout", "60", "db"}
	if !reflect.DeepEqual(args, want) {
		t.Fatal(args)
	}
	if err := m.Action(context.Background(), "start", []string{"--all"}); err == nil {
		t.Fatal("invalid name accepted")
	}
}
func TestPSFormatsAndUnknown(t *testing.T) {
	for _, raw := range []string{`[{"Service":"db","Name":"local-db","State":"running","Health":"healthy"}]`, `{"Service":"db","Name":"local-db","State":"running","Health":"healthy"}`} {
		rows, err := parsePS([]byte(raw))
		if err != nil || len(rows) != 1 || rows[0].Health != "healthy" {
			t.Fatal(rows, err)
		}
	}
	if _, err := parsePS([]byte("broken")); err == nil {
		t.Fatal("invalid output accepted")
	}
}

func TestEnsureDependencyOrderWithoutUnrelatedMigrations(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("x-deps: &deps\n  db:\n    condition: service_healthy\nservices:\n  db:\n    image: postgres\n  worker:\n    image: app\n    depends_on: *deps\n  migrate:\n    image: app\n"), 0600)
	m := New(root)
	order, err := m.DependencyOrder([]string{"worker"})
	if err != nil || !reflect.DeepEqual(order, []string{"db", "worker"}) {
		t.Fatal(order, err)
	}
	calls := []string{}
	m.run = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args[len(args)-1])
		return nil, nil
	}
	if err := m.Ensure(context.Background(), []string{"worker"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"db", "worker"}) {
		t.Fatal(calls)
	}
}

func TestPublishedEndpointAssociationOnlyIncludesLocalTCPBindings(t *testing.T) {
	m := &Manager{File: "compose.yaml", specs: []model.DockerSnapshot{{Service: "api"}}, run: func(context.Context, ...string) ([]byte, error) {
		return []byte(`[{"ID":"api-id","Service":"api","State":"running","Publishers":[{"URL":"127.0.0.1","PublishedPort":6210,"Protocol":"tcp"},{"URL":"0.0.0.0","PublishedPort":6211,"Protocol":"tcp"},{"URL":"192.0.2.1","PublishedPort":6212,"Protocol":"tcp"},{"URL":"127.0.0.1","PublishedPort":6213,"Protocol":"udp"},{"URL":"","PublishedPort":0,"Protocol":"tcp"}]}]`), nil
	}}
	rows, reason := m.Observe(context.Background())
	if reason != "" || len(rows) != 1 || !reflect.DeepEqual(rows[0].PublishedEndpoints, []model.PublishedEndpoint{{Host: "127.0.0.1", Port: 6210}, {Host: "0.0.0.0", Port: 6211}}) {
		t.Fatal(rows, reason)
	}
}

func TestReplicatedServiceDoesNotAttributeOneInstancesPortToAnother(t *testing.T) {
	m := &Manager{File: "compose.yaml", specs: []model.DockerSnapshot{{Service: "api"}}, run: func(context.Context, ...string) ([]byte, error) {
		return []byte(`[{"ID":"api-first","Service":"api","State":"running","Publishers":[{"URL":"127.0.0.1","PublishedPort":6210,"Protocol":"tcp"}]},{"ID":"api-second","Service":"api","State":"running","Publishers":[{"URL":"127.0.0.1","PublishedPort":6214,"Protocol":"tcp"}]}]`), nil
	}}
	rows, reason := m.Observe(context.Background())
	if reason != "" || len(rows) != 1 || len(rows[0].PublishedEndpoints) != 0 {
		t.Fatal("replica ports attributed to one instance", rows, reason)
	}
}
