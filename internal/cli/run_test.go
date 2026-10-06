package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/supervisor"
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
	}{{[]string{"--version"}, 0, buildinfo.Version}, {[]string{"--wat"}, 2, ""}, {[]string{"discover", "--root", root, "--json"}, 0, "schema_version"}, {[]string{"--root", root, "validate"}, 0, "Root"}, {[]string{"init", "--root", root}, 2, ""}, {[]string{"--help"}, 0, "discover"}} {
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

func TestSessionsWithoutWorkspaceOrTerminal(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(t.TempDir(), "missing-cache"))
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"sessions", "--json"}, strings.NewReader(""), &out, &errs)
	if code != 0 || strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("sessions: code=%d out=%q err=%q", code, out.String(), errs.String())
	}
	if _, err := os.Stat(os.Getenv("STACKHARBOR_CACHE_DIR")); !os.IsNotExist(err) {
		t.Fatal("listing sessions created a cache directory")
	}
}

func TestSessionsListFilterAndFocusValidation(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	lock, err := supervisor.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"sessions", "--json"}, strings.NewReader(""), &out, &errs)
	var sessions []supervisor.SessionInfo
	if code != 0 || json.Unmarshal(out.Bytes(), &sessions) != nil || len(sessions) != 1 || sessions[0].Root != root || sessions[0].PID != os.Getpid() {
		t.Fatalf("listing: code=%d out=%q err=%q", code, out.String(), errs.String())
	}
	out.Reset()
	if code := Run(context.Background(), []string{"sessions", "--root", t.TempDir(), "--json"}, strings.NewReader(""), &out, &errs); code != 0 || strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("root filter: code=%d out=%q", code, out.String())
	}
	for _, args := range [][]string{
		{"sessions", "--focus", "0"},
		{"sessions", "--focus", "-1"},
		{"sessions", "--focus", "1", "--json"},
		{"discover", "--focus", "1"},
		{"sessions", "--workspace", "missing.yaml"},
	} {
		if code := Run(context.Background(), args, strings.NewReader(""), &out, &errs); code != 2 {
			t.Fatalf("invalid options accepted: %v code=%d", args, code)
		}
	}
	if code := Run(context.Background(), []string{"sessions", "--focus", "2147483647"}, strings.NewReader(""), &out, &errs); code != 1 || !strings.Contains(errs.String(), "No active StackHarbor session") {
		t.Fatalf("stale focus PID: code=%d err=%q", code, errs.String())
	}
}

func TestKillWithoutSessionDoesNotRequireTerminal(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(t.TempDir(), "missing-cache"))
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"kill", "--root", t.TempDir(), "--yes"}, strings.NewReader(""), &out, &errs)
	if code != 0 || !strings.Contains(out.String(), "No active StackHarbor session") {
		t.Fatalf("kill: code=%d out=%q err=%q", code, out.String(), errs.String())
	}
	if _, err := os.Stat(os.Getenv("STACKHARBOR_CACHE_DIR")); !os.IsNotExist(err) {
		t.Fatal("kill created a cache directory")
	}
}

func TestKillResolvesWorkspaceRootDespiteInvalidServiceConfig(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	lock, err := supervisor.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := os.WriteFile(filepath.Join(root, "stackharbor.yaml"), []byte("version: 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	selected := t.TempDir()
	if err := os.WriteFile(filepath.Join(selected, "stackharbor.workspace.yaml"), []byte("version: 1\nroot: "+root+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(selected)
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"kill", "--yes"}, strings.NewReader(""), &out, &errs)
	// A self-owned lock is refused, proving resolution reached the selected workspace.
	if code != 1 || !strings.Contains(out.String(), root) || !strings.Contains(errs.String(), "current process") {
		t.Fatalf("workspace scope: code=%d out=%q err=%q", code, out.String(), errs.String())
	}
	for _, args := range [][]string{{"kill", "--json"}, {"kill", "--target", "app/web"}, {"kill", "--focus", "1"}} {
		if code := Run(context.Background(), args, strings.NewReader(""), &out, &errs); code != 2 {
			t.Fatalf("invalid kill flags: %v code=%d", args, code)
		}
	}
}
