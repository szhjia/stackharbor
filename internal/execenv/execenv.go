// Package execenv resolves commands against a request's environment without
// changing the engine's process-global PATH or working directory.
package execenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func executable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Mode().Perm()&0111 == 0 {
		return os.ErrPermission
	}
	return nil
}

// LookPath uses only the supplied PATH. Empty and relative PATH entries are
// rooted at cwd, so separate workspaces can safely use separate toolchains.
func LookPath(name string, environment []string, cwd string) (string, error) {
	root := cwd
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(root) {
		var err error
		root, err = filepath.Abs(root)
		if err != nil {
			return "", err
		}
	}
	if strings.ContainsRune(name, filepath.Separator) {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if err := executable(path); err != nil {
			return "", &exec.Error{Name: name, Err: err}
		}
		return path, nil
	}
	pathValue := ""
	found := false
	for _, entry := range environment {
		if k, v, ok := strings.Cut(entry, "="); ok && k == "PATH" {
			pathValue = v
			found = true
		}
	}
	var permission error
	if found {
		entries := filepath.SplitList(pathValue)
		if pathValue == "" {
			entries = []string{""}
		}
		for _, directory := range entries {
			if !filepath.IsAbs(directory) {
				directory = filepath.Join(root, directory)
			}
			path := filepath.Join(directory, name)
			if err := executable(path); err == nil {
				return path, nil
			} else if errors.Is(err, os.ErrPermission) {
				permission = err
			}
		}
	}
	if permission != nil {
		return "", &exec.Error{Name: name, Err: permission}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// CommandContext preserves the complete supplied environment, including an
// explicitly empty environment, and exposes lookup failures through Cmd.Err.
func CommandContext(ctx context.Context, environment []string, cwd, name string, args ...string) *exec.Cmd {
	path, err := LookPath(name, environment, cwd)
	if path == "" {
		path = name
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Args[0] = name
	cmd.Dir = cwd
	cmd.Env = append([]string{}, environment...)
	if err != nil {
		cmd.Err = err
	}
	return cmd
}
