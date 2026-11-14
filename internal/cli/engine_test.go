package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

func TestEngineCLIValidatesBeforeConnecting(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		text string
	}{
		{[]string{"engine", "--help"}, 0, "serve|ensure|status|stop"},
		{[]string{"engine", "unexpected"}, 2, "requires serve"},
		{[]string{"engine", "status", "extra"}, 2, "Unexpected engine arguments"},
		{[]string{"engine", "stop", "--state-dir", "/tmp/unused"}, 2, "only supported for engine serve"},
		{[]string{"engine", "ensure", "--idle-timeout", "1s"}, 2, "only supported for engine serve"},
		{[]string{"engine", "serve", "--idle-timeout", "0s"}, 2, ""},
		{[]string{"engine", "recover", "--help"}, 0, "Recovery never restarts"},
		{[]string{"engine", "recover", "--dry-run"}, 2, "requires --root"},
		{[]string{"engine", "recover", "--root", "/tmp", "--reviewed-unknown", "--yes"}, 2, "requires --acknowledge"},
		{[]string{"engine", "recover", "--root", "/tmp", "--acknowledge", "--yes"}, 2, "after manual process review"},
		{[]string{"engine", "recover", "--root", "/tmp"}, 2, "requires --yes"},
		{[]string{"attach", "--json"}, 2, "discover --json"},
		{[]string{"attach"}, 2, "requires a terminal"},
	} {
		var out, errs bytes.Buffer
		code := Run(context.Background(), tc.args, strings.NewReader(""), &out, &errs)
		if code != tc.code || !strings.Contains(out.String()+errs.String(), tc.text) {
			t.Fatalf("%v: code %d output %s %s", tc.args, code, out.String(), errs.String())
		}
	}
}

func managedMetadata(t *testing.T, root string, protocol int) *supervisor.Lock {
	t.Helper()
	lock, err := supervisor.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	info := lock.Info()
	info.OwnerKind = "engine"
	info.ProtocolVersion = protocol
	info.SocketPath = ""
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("STACKHARBOR_CACHE_DIR"), fmt.Sprintf("%x.lock", sha256.Sum256([]byte(root))))
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return lock
}

func TestManagedKillNeverDowngradesToLegacySignal(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	for _, protocol := range []int{0, 999} {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		managedMetadata(t, root, protocol)
		var out, errs bytes.Buffer
		if code := Run(context.Background(), []string{"kill", "--root", root, "--yes"}, strings.NewReader(""), &out, &errs); code != 2 {
			t.Fatalf("protocol %d code=%d out=%s err=%s", protocol, code, out.String(), errs.String())
		}
		if strings.Contains(out.String(), "Closing legacy") || strings.Contains(errs.String(), "current process") {
			t.Fatalf("managed owner entered legacy signal path: %s %s", out.String(), errs.String())
		}
		_, occupied, err := supervisor.SessionForRoot(root)
		if err != nil || !occupied {
			t.Fatal("managed ownership was released", err)
		}
	}
	var out, errs bytes.Buffer
	if code := runKill(context.Background(), supervisor.SessionInfo{OwnerKind: "engine", PID: os.Getpid()}, 1, false, &out, &errs); code != 2 || !strings.Contains(errs.String(), "will not be signalled") {
		t.Fatal(code, errs.String())
	}
}

func TestManagedFocusRequiresWorkspaceIdentity(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lock := managedMetadata(t, root, 1)
	second, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	managedMetadata(t, second, 1)
	for _, tc := range []struct{ focus, text string }{
		{strconv.Itoa(os.Getpid()), "engine PID is shared"},
		{lock.Info().SessionID, "requires a terminal"},
		{root, "requires a terminal"},
	} {
		var out, errs bytes.Buffer
		if code := Run(context.Background(), []string{"sessions", "--focus", tc.focus}, strings.NewReader(""), &out, &errs); code != 2 || !strings.Contains(errs.String(), tc.text) {
			t.Fatalf("focus %q code=%d out=%s err=%s", tc.focus, code, out.String(), errs.String())
		}
	}
}

func TestManagedStartRequiresConfirmationBeforeOpeningSession(t *testing.T) {
	t.Setenv("STACKHARBOR_ENGINE_MODE", "managed")
	cache := filepath.Join(t.TempDir(), "missing")
	t.Setenv("STACKHARBOR_CACHE_DIR", cache)
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"start", "--root", t.TempDir(), "--target", "app/api"}, strings.NewReader(""), &out, &errs); code != 2 || !strings.Contains(errs.String(), "--yes") {
		t.Fatal(code, errs.String())
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("unconfirmed start created a session registry", err)
	}
}

func TestTaskAuthorizationDoesNotImplyExternalPortRelease(t *testing.T) {
	h := controlHostWithListener(t, false, true)
	port := h.Controller().Snapshot().Services[0].Spec.Ports[0].Number
	external := externalCLIListener(t, port)
	info := h.SessionInfo()
	var out, errs bytes.Buffer
	code := runControlWithInteraction(context.Background(), "start", []string{"--root", info.Root, "--target", "app/web", "--yes", "--json"}, strings.NewReader(""), &out, &errs, controlInteraction{expected: &info, noImplicitRelease: true})
	var operation control.Operation
	if code == 0 || json.Unmarshal(out.Bytes(), &operation) != nil || operation.Action != "start" || syscall.Kill(external.Process.Pid, 0) != nil {
		t.Fatal("task-style authorization released an external listener", code, out.String(), errs.String())
	}
}
