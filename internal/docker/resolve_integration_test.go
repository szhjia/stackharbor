package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRealComposeMultiFile(t *testing.T) {
	if os.Getenv("STACKHARBOR_COMPOSE_INTEGRATION") != "1" {
		t.Skip("set STACKHARBOR_COMPOSE_INTEGRATION=1 for config-only integration")
	}
	root := t.TempDir()
	base := filepath.Join(root, "compose.yaml")
	over := filepath.Join(root, "compose.local.yaml")
	os.WriteFile(base, []byte(`services:
  db:
    image: postgres:16
    healthcheck: {test: [CMD, "true"]}
  api:
    image: nginx:stable
    environment: {VALUE: "${SH_FIXTURE_VALUE:-default}"}
`), 0600)
	os.WriteFile(over, []byte(`services:
  api:
    image: nginx:alpine
    depends_on: {db: {condition: service_healthy}}
`), 0600)
	s := ComposeScope{Files: []string{base, over}, ProjectDirectory: root, Project: "sh-config-fixture"}
	env := os.Environ()
	a, e := Resolve(context.Background(), s, env)
	if e != nil {
		t.Fatal(e)
	}
	if a.Services["api"].Image != "nginx:alpine" || !a.Services["db"].HasHealthcheck || a.Services["api"].Dependencies["db"].Condition != "service_healthy" {
		t.Fatal("merge mismatch")
	}
	s.Files = []string{over, base}
	b, e := Resolve(context.Background(), s, env)
	if e != nil {
		t.Fatal(e)
	}
	if a.Digest == b.Digest || b.Services["api"].Image != "nginx:stable" {
		t.Fatal("merge order lost")
	}
	m := NewResolved(s, env, b)
	dotenv := filepath.Join(root, ".env")
	os.WriteFile(dotenv, []byte("SH_FIXTURE_VALUE=changed\n"), 0600)
	if e = m.CheckResolved(context.Background()); e == nil {
		t.Fatal("new default env ignored")
	}
	c, e := Resolve(context.Background(), s, env)
	if e != nil {
		t.Fatal(e)
	}
	m = NewResolved(s, env, c)
	os.Remove(dotenv)
	if e = m.CheckResolved(context.Background()); e == nil {
		t.Fatal("deleted default env ignored")
	}
	envfile := filepath.Join(root, "test.env")
	os.WriteFile(envfile, []byte("SH_FIXTURE_VALUE=explicit\n"), 0600)
	s.EnvFiles = []string{envfile}
	d, e := Resolve(context.Background(), s, env)
	if e != nil || d.Digest == b.Digest {
		t.Fatal("explicit env not applied", e)
	}
}
