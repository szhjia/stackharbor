package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/szhjia/stackharbor/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIAllCommandsFlagsAndExitCodes(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		args     []string
		code     int
		contains string
	}{{[]string{"--version"}, 0, "0.1.0"}, {[]string{"--wat"}, 2, ""}, {[]string{"discover", "--root", root, "--json"}, 0, "schema_version"}, {[]string{"--root", root, "validate"}, 0, "Root"}, {[]string{"init", "--root", root}, 2, ""}, {[]string{"--help"}, 0, "discover"}} {
		var out, errs bytes.Buffer
		code := Run(context.Background(), tc.args, strings.NewReader(""), &out, &errs)
		if code != tc.code || !strings.Contains(out.String(), tc.contains) {
			t.Fatalf("%v: code %d: %s %s", tc.args, code, out.String(), errs.String())
		}
		if strings.Contains(tc.contains, "schema_version") {
			var v map[string]any
			if json.Unmarshal(out.Bytes(), &v) != nil || v["schema_version"] != float64(1) {
				t.Fatal("bad JSON")
			}
		}
	}
	os.WriteFile(filepath.Join(root, "stackharbor.yaml"), []byte("version: 2"), 0600)
	if Run(context.Background(), []string{"validate", "--root", root}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}) != 2 {
		t.Fatal("invalid configuration accepted")
	}
}
func TestInitNeverOverwritesSymlinkOrCollision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "stackharbor.yaml")
	before := []byte("existing")
	os.WriteFile(path, before, 0600)
	ds := WriteDrafts(root, []Draft{{Path: path, Content: []byte("replacement")}})
	after, _ := os.ReadFile(path)
	if len(ds) == 0 || !bytes.Equal(before, after) {
		t.Fatal("init overwrote existing")
	}
	os.Remove(path)
	outside := filepath.Join(t.TempDir(), "outside")
	os.Symlink(outside, path)
	if len(WriteDrafts(root, []Draft{{Path: path, Content: []byte("replacement")}})) == 0 {
		t.Fatal("init replaced symlink")
	}
	if _, e := os.Stat(outside); e == nil {
		t.Fatal("outside file created")
	}
	if len(WriteDrafts(root, []Draft{{Path: outside, Content: []byte("bad")}})) == 0 {
		t.Fatal("outside draft accepted")
	}
}
func TestInitWritesSafeDraftAndResolvesIDs(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "web"), 0700)
	w := model.Workspace{Root: root, Candidates: []model.Candidate{{ID: "web", Name: "Web", Cwd: filepath.Join(root, "web"), SuggestedCommand: []string{"pnpm", "dev"}}}}
	ds := PlanInit(w)
	if len(ds) != 1 {
		t.Fatal("no draft")
	}
	if errs := WriteDrafts(root, ds); len(errs) != 1 || errs[0].Code != "written" {
		t.Fatal(errs)
	}
	if Run(context.Background(), []string{"validate", "--root", root}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}) != 0 {
		t.Fatal("written draft invalid")
	}
}
func TestNonTTYDoesNotStartServices(t *testing.T) {
	root := t.TempDir()
	var out, errs bytes.Buffer
	if Run(context.Background(), []string{"--root", root}, strings.NewReader("s"), &out, &errs) != 2 {
		t.Fatal("non TTY accepted")
	}
	if !strings.Contains(errs.String(), "discover") {
		t.Fatal("missing useful hint")
	}
}
