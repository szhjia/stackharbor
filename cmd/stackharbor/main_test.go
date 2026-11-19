package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/szhjia/stackharbor/internal/engine"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__engine_export_environment" {
		main()
	}
	os.Exit(m.Run())
}

func TestExportEnvironmentFromMacOSBash(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("exercises macOS system Bash")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("STACKHARBOR_TEST_SHELL_EXPORT", "")
	profile := `case $- in *i*) ;; *) exit 71 ;; esac
shopt -q login_shell || exit 72
echo 'login startup chatter'
echo 'login startup warning' >&2
export STACKHARBOR_TEST_SHELL_EXPORT='interactive login'
`
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	environment, err := engine.ShellEnvironment(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(environment, "STACKHARBOR_TEST_SHELL_EXPORT=interactive login") {
		t.Fatal("the interactive login profile's export did not reach the engine")
	}
}
