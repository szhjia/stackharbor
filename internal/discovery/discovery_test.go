package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, r, n, s string) string {
	t.Helper()
	p := filepath.Join(r, n)
	os.MkdirAll(filepath.Dir(p), 0700)
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func registration(id string) string {
	return "version: 1\nproject: {id: " + id + ", name: 测试}\nservices:\n  web: {run: {command: [echo, hi]}}\n"
}
func TestDiscoverNestedProjectsAndWorkspaceGlobs(t *testing.T) {
	r := t.TempDir()
	write(t, r, "apps/one/stackharbor.yaml", registration("one"))
	write(t, r, "apps/one/.git/ignored", "")
	write(t, r, "pnpm-workspace.yaml", "packages: ['apps/*', '!apps/skip']\n")
	write(t, r, "package.json", `{"packageManager":"pnpm@11.9.0"}`)
	write(t, r, "apps/two/package.json", `{"scripts":{"dev":"vite"}}`)
	write(t, r, "apps/skip/package.json", `{"scripts":{"dev":"vite"}}`)
	write(t, r, "backend/pyproject.toml", "[project]\nname='api'\n")
	w := Discover(context.Background(), Options{Root: r})
	if w.Invalid() || len(w.Projects) != 1 || len(w.Candidates) != 2 {
		t.Fatalf("wrong discoveries: %+v", w)
	}
	for _, c := range w.Candidates {
		if c.Kind == "python" && len(c.SuggestedCommand) > 0 {
			t.Fatal("guessed python")
		}
		if strings.HasSuffix(c.Cwd, "two") && strings.Join(c.SuggestedCommand, " ") != "pnpm dev" {
			t.Fatal(c)
		}
	}
}
func TestExplicitImportDeduplicatesRealpath(t *testing.T) {
	r := t.TempDir()
	write(t, r, "app/stackharbor.yaml", registration("one"))
	wf := write(t, r, "stackharbor.workspace.yaml", "version: 1\nroot: .\nregistrations: [app/stackharbor.yaml, app/../app/stackharbor.yaml]\n")
	w := Discover(context.Background(), Options{Root: r, WorkspaceFile: wf})
	if len(w.Projects) != 1 || w.Invalid() {
		t.Fatal(w)
	}
}
func TestUnknownManifestAndScanLimit(t *testing.T) {
	r := t.TempDir()
	write(t, r, "bad/package.json", "{")
	w := Discover(context.Background(), Options{Root: r})
	if !w.Invalid() {
		t.Fatal("malformed manifest accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w = Discover(ctx, Options{Root: r})
	if !w.Invalid() {
		t.Fatal("cancelled scan accepted")
	}
}
