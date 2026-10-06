package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

func TestInteractiveJSONConfirmationShowsPlanOnStderr(t *testing.T) {
	h := controlHost(t, false)
	// A real second legacy registry owner makes the plan's partial-coverage warning
	// observable, rather than injecting a fabricated warning into a presentation test.
	legacy, e := supervisor.Acquire(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer legacy.Close()
	master, slave, e := pty.Open()
	if e != nil {
		t.Fatal(e)
	}
	defer master.Close()
	defer slave.Close()
	state, e := term.MakeRaw(slave.Fd())
	if e != nil {
		t.Fatal(e)
	}
	defer term.Restore(slave.Fd(), state)
	if !terminal(slave) {
		t.Fatal("PTY does not enter actual terminal detection")
	}
	if _, e = master.Write([]byte("yes\n")); e != nil {
		t.Fatal(e)
	}
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"stop", "--root", h.SessionInfo().Root, "--target", "app/web", "--json"}, slave, slave, &stderr)
	// Write/read an exact delimiter while the terminal remains open: portable PTY
	// reads need not depend on platform-specific EOF/EIO semantics on slave close.
	marker := []byte("\nSH_JSON_END\n")
	if _, e = slave.Write(marker); e != nil {
		t.Fatal(e)
	}
	var stdout bytes.Buffer
	chunk := make([]byte, 1024)
	for !bytes.Contains(stdout.Bytes(), marker) {
		n, e := master.Read(chunk)
		stdout.Write(chunk[:n])
		if e != nil {
			t.Fatal(e)
		}
	}
	result := bytes.TrimSuffix(stdout.Bytes(), marker)
	var op control.Operation
	if code != 0 || json.Unmarshal(result, &op) != nil || op.State != "succeeded" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, result, stderr.String())
	}
	for _, text := range []string{"Plan ", "stop targets=[app/web] affected=[app/web]", "Warning: Resource dependency coverage is partial", "Execute this plan? [y/N]"} {
		if !strings.Contains(stderr.String(), text) {
			t.Fatalf("missing %q in stderr=%q", text, stderr.String())
		}
	}
	if strings.Index(stderr.String(), "Plan ") > strings.Index(stderr.String(), "Execute this plan?") {
		t.Fatal("prompt preceded plan")
	}
}

func TestOperationsUnavailableRegisteredOwnerIsUnresolved(t *testing.T) {
	for _, journal := range []string{"missing", "finalizing"} {
		t.Run(journal, func(t *testing.T) {
			t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
			root := t.TempDir()
			root, _ = filepath.EvalSymlinks(root)
			lock, e := supervisor.Acquire(root)
			if e != nil {
				t.Fatal(e)
			}
			defer lock.Close()
			info := lock.Info()
			shortBase, e := filepath.EvalSymlinks("/tmp")
			if e != nil {
				t.Fatal(e)
			}
			runDir, e := os.MkdirTemp(shortBase, "sh-cli6-")
			if e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(runDir)
			socket := filepath.Join(runDir, "missing.sock")
			if e = lock.PublishEndpoint(supervisor.Endpoint{SocketPath: socket, ProtocolVersion: sessionapi.ProtocolVersion, Capabilities: []string{"operations"}}); e != nil {
				t.Fatal(e)
			}
			if journal == "finalizing" {
				dir := filepath.Join(info.CacheDir, "completions")
				if e = os.Mkdir(dir, 0700); e != nil {
					t.Fatal(e)
				}
				record := map[string]any{"id": "unresolved-operation", "session_id": info.SessionID, "workspace_id": info.WorkspaceID, "action": "close", "state": "finalizing", "updated_at": time.Now().UTC()}
				data, e := json.Marshal(record)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(dir, info.SessionID+".json"), data, 0600); e != nil {
					t.Fatal(e)
				}
			}
			var out, errs bytes.Buffer
			code := Run(context.Background(), []string{"operations", "--root", root, "--id", "unresolved-operation", "--json"}, strings.NewReader(""), &out, &errs)
			if code != 3 {
				t.Fatalf("journal=%s code=%d stdout=%q stderr=%q", journal, code, out.String(), errs.String())
			}
			if !strings.Contains(errs.String(), "unresolved") {
				t.Fatalf("missing unresolved explanation: %q", errs.String())
			}
			if out.Len() != 0 {
				t.Fatal("unknown result fabricated JSON success", out.String())
			}
		})
	}
}

func TestReleaseRequiresExplicitPositivePortBeforeRootResolution(t *testing.T) {
	for _, port := range []string{"omitted", "0", "-1", "65536"} {
		t.Run(port, func(t *testing.T) {
			args := []string{"release", "--root", filepath.Join(t.TempDir(), "missing-root"), "--target", "app/web", "--yes", "--dry-run"}
			if port != "omitted" {
				args = append(args, "--port", port)
			}
			var out, errs bytes.Buffer
			code := Run(context.Background(), args, strings.NewReader(""), &out, &errs)
			if code != 2 || !strings.Contains(errs.String(), "release requires --port between 1 and 65535") {
				t.Fatalf("port=%s code=%d out=%q err=%q", port, code, out.String(), errs.String())
			}
		})
	}
}

func TestOperationQueryUnsupportedProtocolRemainsUsageError(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	root := t.TempDir()
	lock, e := supervisor.Acquire(root)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"operations", "--root", root, "--id", "operation", "--json"}, strings.NewReader(""), &out, &errs)
	if code != 2 || !strings.Contains(errs.String(), "protocol") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errs.String())
	}
}
