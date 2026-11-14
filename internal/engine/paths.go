// Package engine owns independent, per-user workspace runtimes.
package engine

import (
	"crypto/sha256"
	"fmt"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const BundleID = "com.xahacktech.stackharbor"
const AgentLabel = BundleID + ".engine"
const ProtocolVersion = 1

func ManagedEnabled() bool {
	switch os.Getenv("STACKHARBOR_ENGINE_MODE") {
	case "managed":
		return true
	case "legacy":
		return false
	}
	if runtime.GOOS != "darwin" {
		return false
	}
	exe, _ := os.Executable()
	real, _ := filepath.EvalSymlinks(exe)
	return strings.Contains(real, ".app/Contents/MacOS/") || strings.Contains(real, "/"+BundleID+"/versions/")
}

func StateDirectory() (string, error) {
	if p := os.Getenv("STACKHARBOR_ENGINE_STATE_DIR"); p != "" {
		return privatePath(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	var p string
	if runtime.GOOS == "darwin" {
		p = filepath.Join(home, "Library/Application Support", BundleID)
	} else {
		p = filepath.Join(home, ".local/share", BundleID)
	}
	return privatePath(p)
}

func RuntimeDirectory() (string, error) {
	p, err := runtimePath()
	if err != nil {
		return "", err
	}
	return privatePath(p)
}

func runtimePath() (string, error) {
	if p := os.Getenv("STACKHARBOR_ENGINE_RUNTIME_DIR"); p != "" {
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("engine directory must be absolute")
		}
		return filepath.Clean(p), nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(cache, "stackharbor", "engine-run")
	if len(filepath.Join(p, "engine.sock")) >= 100 {
		base, err := filepath.EvalSymlinks(os.TempDir())
		if err != nil {
			return "", err
		}
		digest := sha256.Sum256([]byte(p))
		p = filepath.Join(base, fmt.Sprintf("sh-engine-%d-%x", os.Getuid(), digest[:6]))
	}
	return p, nil
}

func privatePath(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("engine directory must be absolute")
	}
	p = filepath.Clean(p)
	if err := supervisor.EnsurePrivateDirectory(p); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	if err = sessionapi.ValidatePrivateDirectory(real); err != nil {
		return "", err
	}
	return real, nil
}
