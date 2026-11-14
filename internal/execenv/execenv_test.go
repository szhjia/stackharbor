package execenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspacePATHAndCompleteEnvironment(t *testing.T) {
	beforePATH := os.Getenv("PATH")
	for _, value := range []string{"first", "second"} {
		root := t.TempDir()
		bin := filepath.Join(root, "tools")
		if err := os.Mkdir(bin, 0700); err != nil {
			t.Fatal(err)
		}
		tool := filepath.Join(bin, "same-command")
		if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf '%s:%s' '"+value+"' \"${ONLY_VALUE:-missing}\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		environment := []string{"PATH=tools", "ONLY_VALUE=workspace"}
		path, err := LookPath("same-command", environment, root)
		if err != nil || path != tool {
			t.Fatalf("path=%q err=%v", path, err)
		}
		cmd := CommandContext(context.Background(), environment, root, "same-command")
		environment[1] = "ONLY_VALUE=mutated"
		output, err := cmd.Output()
		if err != nil || string(output) != value+":workspace" {
			t.Fatalf("output=%q err=%v", output, err)
		}
	}
	if os.Getenv("PATH") != beforePATH {
		t.Fatal("changed global PATH")
	}
}
func TestLookPathDoesNotInheritAndResolvesExplicitPaths(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "local-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{tool, "./local-tool"} {
		got, err := LookPath(name, nil, root)
		if err != nil || got != tool {
			t.Fatalf("%q %v", got, err)
		}
	}
	if _, err := LookPath("sh", nil, root); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("inherited engine PATH: %v", err)
	}
	if got, err := LookPath("local-tool", []string{"PATH="}, root); err != nil || got != tool {
		t.Fatalf("empty path should mean cwd: %q %v", got, err)
	}
	if err := os.Chmod(tool, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LookPath("local-tool", []string{"PATH=."}, root); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("non executable accepted: %v", err)
	}
	cmd := CommandContext(context.Background(), []string{}, root, "missing-tool")
	if err := cmd.Run(); err == nil || !strings.Contains(err.Error(), "missing-tool") {
		t.Fatal(err)
	}
}
