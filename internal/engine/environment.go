package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ShellEnvironment exports through FD3 so shell startup chatter never becomes
// environment data. Nothing in this stream is logged or persisted.
func ShellEnvironment(ctx context.Context, root string) ([]string, error) {
	budget, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	shell := os.Getenv("SHELL")
	if shell == "" {
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		} else {
			shell = "/bin/sh"
		}
	}
	if !filepath.IsAbs(shell) {
		return nil, fmt.Errorf("default shell must be an absolute executable")
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	// macOS Bash closes inherited extra descriptors during interactive startup.
	// Carry the pipe on stdin and move it to FD3 after the shell has initialized.
	cmd := exec.CommandContext(budget, shell, "-lic", `exec "$1" __engine_export_environment 3>&0 </dev/null`, "stackharbor", self)
	cmd.Dir = root
	cmd.Stdin = w
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	stop := context.AfterFunc(budget, func() { _ = r.Close() })
	defer stop()
	if err = cmd.Start(); err != nil {
		w.Close()
		return nil, fmt.Errorf("cannot start default shell: %w", err)
	}
	w.Close()
	data, readErr := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	waitErr := cmd.Wait()
	if readErr != nil || waitErr != nil || len(data) > 1<<20 || budget.Err() != nil {
		return nil, fmt.Errorf("default shell environment unavailable or exceeded 10s/1MiB; check shell startup scripts")
	}
	var env []string
	if json.Unmarshal(data, &env) != nil || len(env) == 0 {
		return nil, fmt.Errorf("default shell did not return a valid environment")
	}
	for _, pair := range env {
		k, _, ok := strings.Cut(pair, "=")
		if !ok || k == "" || strings.ContainsRune(pair, 0) {
			return nil, fmt.Errorf("invalid shell environment")
		}
	}
	return env, nil
}
