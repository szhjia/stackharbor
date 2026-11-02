package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/supervisor"
)

func TestDesktopCLIHelpValidationAndSSHFailure(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "fixture")
	for _, tc := range []struct {
		args []string
		code int
		text string
	}{
		{[]string{"desktop", "--help"}, 0, "Open the installed desktop"},
		{[]string{"desktop", "extra"}, 2, "no positional"},
		{[]string{"desktop"}, 1, "SSH"},
		{[]string{"discover", "--no-desktop"}, 2, "only valid for run"},
		{[]string{"run", "--no-desktop"}, 2, "requires a terminal"},
		{[]string{"desktop-backend", "--namespace", "/tmp", "extra"}, 2, "parent pipe"},
	} {
		var out, errs bytes.Buffer
		code := Run(context.Background(), tc.args, strings.NewReader(""), &out, &errs)
		if code != tc.code || !strings.Contains(out.String()+errs.String(), tc.text) {
			t.Fatalf("%v code=%d out=%q err=%q", tc.args, code, out.String(), errs.String())
		}
	}
}

func TestOccupiedUnverifiableSessionIsNotReportedAsAbsent(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("STACKHARBOR_CACHE_DIR", cache)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lock, err := supervisor.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	path := filepath.Join(cache, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(root))))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record["process_created_millis"] = float64(1)
	raw, _ = json.Marshal(record)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"kill", "status", "start"} {
		var out, errs bytes.Buffer
		args := []string{command, "--root", root}
		if command != "status" {
			args = append(args, "--yes")
		}
		if command == "start" {
			args = append(args, "--target", "fixture/service/api")
		}
		if code := Run(context.Background(), args, strings.NewReader(""), &out, &errs); code == 0 || !strings.Contains(errs.String(), "session process or endpoint identity changed") {
			t.Fatalf("%s claimed absent/success: code=%d out=%s err=%s", command, code, out.String(), errs.String())
		}
	}
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"operations", "--root", root, "--id", "missing"}, strings.NewReader(""), &out, &errs); code != 2 || !strings.Contains(errs.String(), "identity changed") {
		t.Fatalf("unverified owner lost identity-conflict exit semantics: %d %s", code, errs.String())
	}
	info := lock.Info()
	dir := filepath.Join(cache, "completions")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	completion, _ := json.Marshal(map[string]any{"id": "known-close", "session_id": info.SessionID, "workspace_id": info.WorkspaceID, "action": "close", "state": "succeeded", "updated_at": time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(dir, info.SessionID+".json"), completion, 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errs.Reset()
	if code := Run(context.Background(), []string{"operations", "--root", root, "--id", "known-close", "--json"}, strings.NewReader(""), &out, &errs); code != 0 || !strings.Contains(out.String(), "succeeded") {
		t.Fatalf("durable receipt unavailable with unresolved new owner: %d %s %s", code, out.String(), errs.String())
	}
}
