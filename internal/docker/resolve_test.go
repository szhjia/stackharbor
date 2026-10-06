package docker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func resolverFixture(t *testing.T, output string) (ComposeScope, []string) {
	t.Helper()
	root := t.TempDir()
	file := filepath.Join(root, "compose.yml")
	os.WriteFile(file, []byte("services: {db: {image: postgres}}"), 0600)
	os.WriteFile(filepath.Join(root, "result"), []byte(output), 0600)
	os.WriteFile(filepath.Join(root, "docker"), []byte("#!/bin/sh\ncat \""+filepath.Join(root, "result")+"\"\n"), 0700)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	return ComposeScope{Files: []string{file}, ProjectDirectory: root, Project: "demo"}, os.Environ()
}
func TestResolveCompose(t *testing.T) {
	s, env := resolverFixture(t, `{"services":{"db":{"image":"postgres","healthcheck":{"test":["CMD","true"]}},"api":{"depends_on":{"db":{"condition":"service_healthy","required":true,"restart":false}}}}}`)
	r, e := Resolve(context.Background(), s, env)
	if e != nil {
		t.Fatal(e)
	}
	if !r.Services["db"].HasHealthcheck || r.Services["api"].Dependencies["db"].Condition != "service_healthy" {
		t.Fatal("normalized contract lost")
	}
	for _, bad := range []string{`secret-sentinel`, `[]`, `{}`, `{"services":{}}`} {
		os.WriteFile(filepath.Join(s.ProjectDirectory, "result"), []byte(bad), 0600)
		_, e = Resolve(context.Background(), s, env)
		if e == nil || strings.Contains(e.Error(), "secret-sentinel") {
			t.Fatal("unsafe or missing parse error")
		}
	}
	os.WriteFile(filepath.Join(s.ProjectDirectory, "docker"), []byte("#!/bin/sh\necho secret-sentinel >&2\nexit 1\n"), 0700)
	_, e = Resolve(context.Background(), s, env)
	if e == nil || strings.Contains(e.Error(), "secret-sentinel") {
		t.Fatal("stderr leaked")
	}
	os.WriteFile(filepath.Join(s.ProjectDirectory, "docker"), []byte("#!/bin/sh\nexec sleep 5\n"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, e = Resolve(ctx, s, env)
	if e == nil {
		t.Fatal("timeout ignored")
	}
}
func TestComposeUnsupportedSyntax(t *testing.T) {
	s, env := resolverFixture(t, `{"services":{"db":{"image":"postgres"}}}`)
	for _, body := range []string{"include: other.yml\nservices: {}", "services: {db: {extends: foo}}", "services: {db: {profiles: [local]}}"} {
		os.WriteFile(s.Files[0], []byte(body), 0600)
		if _, e := Resolve(context.Background(), s, env); e == nil {
			t.Fatal("unsupported syntax accepted")
		}
	}
}
func TestComposeModelDigest(t *testing.T) {
	s, env := resolverFixture(t, `{"services":{"db":{"image":"postgres","environment":{"KEY":"one"}}}}`)
	a, e := Resolve(context.Background(), s, env)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(s.ProjectDirectory, "result"), []byte(`{"services":{"db":{"environment":{"KEY":"one"},"image":"postgres"}}}`), 0600)
	b, e := Resolve(context.Background(), s, env)
	if e != nil || a.Digest != b.Digest {
		t.Fatal("key order changed digest")
	}
	os.WriteFile(filepath.Join(s.ProjectDirectory, "result"), []byte(`{"services":{"db":{"image":"postgres","environment":{"KEY":"two"}}}}`), 0600)
	c, e := Resolve(context.Background(), s, env)
	if e != nil || a.Digest == c.Digest {
		t.Fatal("non-projected field ignored")
	}
}

func TestComposeExplicitInputDrift(t *testing.T) {
	s, env := resolverFixture(t, `{"services":{"db":{"image":"postgres"}}}`)
	r, e := Resolve(context.Background(), s, env)
	if e != nil {
		t.Fatal(e)
	}
	m := NewResolved(s, env, r)
	os.WriteFile(s.Files[0], []byte("# changed\nservices: {db: {image: postgres}}"), 0600)
	if e = m.CheckResolved(context.Background()); e == nil {
		t.Fatal("source change ignored when model unchanged")
	}
}
func TestMultiFilePhysicalIdentity(t *testing.T) {
	s, env := resolverFixture(t, `{"services":{"db":{"image":"postgres"}}}`)
	r, e := Resolve(context.Background(), s, env)
	if e != nil {
		t.Fatal(e)
	}
	a := NewResolved(s, env, r)
	s.Files = append(s.Files, "other.yml")
	b := NewResolved(s, env, r)
	run := func(context.Context, ...string) ([]byte, error) { return []byte(`"same-daemon"`), nil }
	a.run = run
	b.run = run
	ka, e := a.LockKeys(context.Background(), []string{"db"})
	if e != nil {
		t.Fatal(e)
	}
	kb, e := b.LockKeys(context.Background(), []string{"db"})
	if e != nil || ka[0] != kb[0] || a.ScopeKey() == b.ScopeKey() {
		t.Fatal("configuration scope replaced physical identity")
	}
}

func TestResolveComposeLimits(t *testing.T) {
	s, env := resolverFixture(t, strings.Repeat("x", 8*1024*1024+1))
	if _, e := Resolve(context.Background(), s, env); e == nil {
		t.Fatal("oversized stdout accepted")
	}
	os.WriteFile(filepath.Join(s.ProjectDirectory, "docker"), []byte("#!/bin/sh\ncat '"+filepath.Join(s.ProjectDirectory, "result")+"' >&2\nprintf '%s' '{\"services\":{\"db\":{\"image\":\"postgres\"}}}'\n"), 0700)
	os.WriteFile(filepath.Join(s.ProjectDirectory, "result"), []byte(strings.Repeat("x", 65537)), 0600)
	if _, e := Resolve(context.Background(), s, env); e == nil {
		t.Fatal("oversized stderr accepted")
	}
	t.Setenv("PATH", t.TempDir())
	if _, e := Resolve(context.Background(), s, os.Environ()); e == nil || !strings.Contains(e.Error(), "CLI unavailable") {
		t.Fatal("missing CLI diagnostic", e)
	}
}

func TestComposeBufferBoundsReaderFrom(t *testing.T) {
	b := &limitedBuffer{limit: 4}
	_, e := io.Copy(b, struct{ io.Reader }{strings.NewReader("123456")})
	if e != nil {
		t.Fatal(e)
	}
	if b.Len() != 4 || !b.exceeded {
		t.Fatal("io.Copy bypassed limit", b.Len())
	}
}
