package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/szhjia/stackharbor/internal/supervisor"
)

func TestLocateConflictUnsupportedTerminalPreservesOwner(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	t.Setenv("TERM_PROGRAM", "unsupported-test-terminal")
	root := t.TempDir()
	lock, err := supervisor.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	_, err = supervisor.Acquire(root)
	conflict := err.(*supervisor.SessionConflict)
	var out, errs bytes.Buffer
	if code := locateConflict(context.Background(), conflict, &out, &errs); code != 1 {
		t.Fatalf("unsupported terminal claimed located: %d", code)
	}
	if !strings.Contains(errs.String(), "stackharbor sessions --focus") || !strings.Contains(errs.String(), root) {
		t.Fatal("missing actionable owner details", errs.String())
	}
	if _, err := supervisor.Acquire(root); err == nil {
		t.Fatal("locating released the existing owner's lock")
	}
}

func TestTerminalFocusUsesArgumentAndRechecksSession(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS Terminal integration")
	}
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("TMUX", "")
	t.Setenv("STY", "")
	t.Setenv("SSH_CONNECTION", "")
	lock, err := supervisor.Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	all, err := supervisor.ListSessions()
	if err != nil || len(all) != 1 {
		t.Fatal(all, err)
	}
	info := all[0]
	// A hostile-looking TTY must stay data in argv, never become AppleScript source.
	info.TTY = "/dev/tty\" & do shell script \"unexpected"
	data, _ := json.Marshal(info)
	entries, _ := os.ReadDir(os.Getenv("STACKHARBOR_CACHE_DIR"))
	path := filepath.Join(os.Getenv("STACKHARBOR_CACHE_DIR"), entries[0].Name())
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	argsFile := filepath.Join(bin, "args")
	t.Setenv("SH_FOCUS_ARGS", argsFile)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	stub := "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$SH_FOCUS_ARGS\"\nprintf 'located\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "osascript"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	if err := focusTerminal(context.Background(), info); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(argsFile)
	parts := strings.Split(string(args), "\x00")
	if len(parts) != 4 || parts[0] != "-e" || parts[1] != focusTerminalScript || parts[2] != info.TTY {
		t.Fatalf("unsafe invocation: %q", parts)
	}
	lock.Close()
	os.Remove(argsFile)
	if err := focusTerminal(context.Background(), info); err == nil {
		t.Fatal("focused a released session")
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatal("automation ran for a stale session")
	}
}
