package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestToolDistributionExamplesAreExcluded(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "utility")
	for _, name := range []string{"app", "utility/examples/a", "utility/examples/b"} {
		dir := filepath.Join(root, name)
		os.MkdirAll(dir, 0700)
		id := "web"
		if name == "app" {
			id = "main"
		}
		os.WriteFile(filepath.Join(dir, "stackharbor.yaml"), []byte("version: 1\nproject: {id: "+id+", name: Example}\nservices:\n  dev:\n    run: {command: [server]}\n"), 0600)
	}
	os.WriteFile(filepath.Join(tool, ".stackharbor-tool"), []byte("distribution marker\n"), 0600)
	w := Discover(context.Background(), Options{Root: root})
	if w.Invalid() || len(w.Projects) != 1 || w.Projects[0].ID != "main" {
		t.Fatal("bundled examples polluted user discovery", w.Diagnostics, w.Projects)
	}
	own := Discover(context.Background(), Options{Root: tool})
	if own.Invalid() || len(own.Projects) != 0 {
		t.Fatal("tool root registered bundled examples", own.Diagnostics)
	}
}
