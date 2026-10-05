package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const valid = "version: 1\nproject: {id: app, name: 教材}\nservices:\n  web:\n    run: {command: [echo, hi]}\n    ports: [{name: http, port: 5612}]\n    ready: {tcp: '127.0.0.1:5612'}\n"

func put(t *testing.T, root, name, text string) string {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.WriteFile(p, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestLoadProjectDefaultsAndRejectsInvalidYAML(t *testing.T) {
	r := t.TempDir()
	p := put(t, r, "stackharbor.yaml", valid)
	got, ds := LoadProject(p, r)
	if len(ds) != 0 || len(got.Services) != 1 {
		t.Fatalf("valid config missing: %+v %+v", got, ds)
	}
	s := got.Services[0]
	if s.Stop.Signal != "TERM" || s.Stop.Timeout != 5*time.Second || s.Ready.Timeout != 60*time.Second || s.ID != "app/web" {
		t.Fatal(s)
	}
	for _, bad := range []string{strings.Replace(valid, "version: 1", "version: 3", 1), valid + "extra: true\n", valid + "version: 1\n", valid + "---\nversion: 1\n", strings.Replace(valid, "echo", "!custom echo", 1), strings.Replace(valid, "5612", "65536", 1), strings.Repeat(" ", 256*1024+1), strings.Replace(valid, "[echo, hi]", "[]", 1), strings.Replace(valid, "127.0.0.1", "example.org", 1), strings.Replace(valid, "[echo, hi]", "[echo, 12]", 1)} {
		put(t, r, "stackharbor.yaml", bad)
		_, ds := LoadProject(p, r)
		if len(ds) == 0 || ds[0].File != p || ds[0].Field == "" {
			t.Fatalf("accepted invalid input: %.80s %+v", bad, ds)
		}
	}
}
func TestResolveCwdUnicodeAndSymlinkEscape(t *testing.T) {
	r := t.TempDir()
	d := filepath.Join(r, "中文 工程")
	os.Mkdir(d, 0700)
	p := filepath.Join(r, "stackharbor.yaml")
	got, err := ResolveCwd(r, p, "中文 工程")
	if err != nil || got != canonical(d) {
		t.Fatal(got, err)
	}
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(r, "escape"))
	if _, err := ResolveCwd(r, p, "escape"); err == nil {
		t.Fatal("accepted symlink escape")
	}
}
func canonical(p string) string { out, _ := filepath.EvalSymlinks(p); return out }
func TestWorkspaceDiscoverDefault(t *testing.T) {
	r := t.TempDir()
	p := put(t, r, "stackharbor.workspace.yaml", "version: 1\nroot: .\n")
	w, ds := LoadWorkspace(p)
	if len(ds) != 0 || !w.Discover || !w.RootExplicit {
		t.Fatal(w, ds)
	}
}
