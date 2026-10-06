// Package sessionapi provides the private HTTP-over-Unix-socket session protocol.
package sessionapi

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

const ProtocolVersion = 1
const maxSocketPath = 100

func checkOwner(st os.FileInfo, uid int) error {
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != uid {
		return &control.APIError{Code: "forbidden", Message: "path is not owned by the current user"}
	}
	return nil
}

// ValidatePrivateDirectory verifies an existing, canonical private directory.
// It is exported for the web instance transport to use the same ownership rules.
func ValidatePrivateDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return &control.APIError{Code: "forbidden", Message: "private directory must be canonical and absolute"}
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 {
		return &control.APIError{Code: "forbidden", Message: "unsafe private directory permissions or type"}
	}
	if err := checkOwner(st, os.Getuid()); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if real != path {
		return &control.APIError{Code: "forbidden", Message: "private directory contains a symbolic link"}
	}
	return nil
}

// ValidatePrivateSocket applies the session transport ownership, mode and path rules.
func ValidatePrivateSocket(path string) (os.FileInfo, error) { return validateSocket(path) }

func validateSocket(path string) (os.FileInfo, error) {
	if len(path) >= maxSocketPath {
		return nil, &control.APIError{Code: "forbidden", Message: "socket path is too long"}
	}
	if err := ValidatePrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSocket == 0 || st.Mode().Perm() != 0600 {
		return nil, &control.APIError{Code: "forbidden", Message: "unsafe socket permissions or type"}
	}
	if err := checkOwner(st, os.Getuid()); err != nil {
		return nil, err
	}
	return st, nil
}
func socketPath(info supervisor.SessionInfo) (string, error) {
	st, err := os.Lstat(info.CacheDir)
	if err != nil {
		return "", err
	}
	if !st.IsDir() || st.Mode().Perm()&0022 != 0 {
		return "", &control.APIError{Code: "forbidden", Message: "unsafe session cache"}
	}
	if err := checkOwner(st, os.Getuid()); err != nil {
		return "", err
	}
	dir := filepath.Join(info.CacheDir, "run")
	path := filepath.Join(dir, info.SessionID+".sock")
	if len(path) >= maxSocketPath {
		base, err := filepath.EvalSymlinks("/tmp")
		if err != nil {
			return "", err
		}
		digest := sha256.Sum256([]byte(info.CacheDir))
		dir = filepath.Join(base, fmt.Sprintf("sh-%d-%x", os.Getuid(), digest[:8]))
		path = filepath.Join(dir, info.SessionID+".sock")
	}
	if len(path) >= maxSocketPath {
		return "", &control.APIError{Code: "invalid_request", Message: "no safe short Unix socket path available"}
	}
	if err := supervisor.EnsurePrivateDirectory(dir); err != nil {
		return "", err
	}
	if err := ValidatePrivateDirectory(dir); err != nil {
		return "", err
	}
	// Never unlink a pre-existing path: a live or replaced endpoint needs its owner.
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		if err == nil {
			err = &control.APIError{Code: "endpoint_conflict", Message: "socket path already exists"}
		}
		return "", err
	}
	return path, nil
}
func verifyProcess(ctx context.Context, info supervisor.SessionInfo) error {
	p, err := process.NewProcessWithContext(ctx, int32(info.PID))
	if err != nil {
		return identityConflict()
	}
	created, err := p.CreateTimeWithContext(ctx)
	if err != nil || created != info.ProcessCreatedMillis {
		return identityConflict()
	}
	return nil
}
func identityConflict() *control.APIError {
	return &control.APIError{Code: "identity_conflict", Message: "session process or endpoint identity changed"}
}
func unavailable(err error) error {
	var api *control.APIError
	if errors.As(err, &api) {
		return api
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &control.APIError{Code: "unavailable", Message: err.Error()}
}
func samePath(st os.FileInfo, path string) bool {
	named, err := os.Lstat(path)
	return err == nil && os.SameFile(st, named)
}
func dial(ctx context.Context, path string) (net.Conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	return d.DialContext(ctx, "unix", path)
}
