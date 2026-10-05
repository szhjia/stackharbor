package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func relativeSafe(p string) bool {
	if filepath.IsAbs(p) {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return false
		}
	}
	return true
}
func Within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func ResolveCwd(root, source, cwd string) (string, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if cwd == "" {
		cwd = "."
	}
	p := cwd
	if !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(source), p)
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	p, err = filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if !Within(root, p) {
		return "", fmt.Errorf("cwd is outside selected root")
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("cwd must be a directory")
	}
	return p, nil
}
