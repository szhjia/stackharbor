package supervisor

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	gp "github.com/shirou/gopsutil/v4/process"
)

// TerminateSession targets only the canonical workspace's lock owner. SIGTERM lets
// the existing session stop its owned services and restore its terminal normally.
func TerminateSession(ctx context.Context, root string) (SessionInfo, bool, error) {
	real, err := filepath.Abs(root)
	if err == nil {
		real, err = filepath.EvalSymlinks(real)
	}
	if err != nil {
		return SessionInfo{}, false, err
	}
	cache, err := sessionCacheDir()
	if err != nil {
		return SessionInfo{}, false, err
	}
	path := filepath.Join(cache, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(real))))
	info, active, err := activeSession(path)
	if err != nil || !active {
		return info, false, err
	}
	info.Root = real // Also identify legacy sessions whose cwd differs from workspace root.
	if info.PID == os.Getpid() {
		return info, false, fmt.Errorf("Refusing to terminate the current process")
	}
	if err := signalSession(ctx, info, verifySessionOwner, syscall.Kill); err != nil {
		return info, false, err
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		current, active, err := activeSession(path)
		if err != nil {
			return info, false, err
		}
		if !active || current.PID > 0 && (current.PID != info.PID || current.ProcessCreatedMillis != info.ProcessCreatedMillis) {
			return info, true, nil
		}
		select {
		case <-ctx.Done():
			return info, false, fmt.Errorf("Session PID %d did not release its lock: %w", info.PID, ctx.Err())
		case <-tick.C:
		}
	}
}

func signalSession(ctx context.Context, info SessionInfo, verify func(context.Context, SessionInfo) error, signal func(int, syscall.Signal) error) error {
	if info.PID <= 0 || info.ProcessCreatedMillis <= 0 {
		return fmt.Errorf("Session owner could not be identified; no signal sent")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verify(ctx, info); err != nil {
		return fmt.Errorf("Session owner could not be verified; no signal sent: %w", err)
	}
	if err := signal(info.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("Could not stop session PID %d: %w", info.PID, err)
	}
	return nil
}

func verifySessionOwner(ctx context.Context, info SessionInfo) error {
	current, active, err := activeSession(info.lockPath)
	if err != nil {
		return err
	}
	if !active || current.PID != info.PID || current.ProcessCreatedMillis != info.ProcessCreatedMillis {
		return fmt.Errorf("Workspace session changed or ended")
	}
	p, err := gp.NewProcessWithContext(ctx, int32(info.PID))
	if err != nil {
		return err
	}
	name, err := p.NameWithContext(ctx)
	if err != nil || name != "stackharbor" {
		return fmt.Errorf("PID %d is not an observable StackHarbor process", info.PID)
	}
	// Advisory metadata alone must never authorize signaling an unrelated PID.
	lock, err := filepath.EvalSymlinks(info.lockPath)
	if err != nil {
		return err
	}
	lock, err = filepath.Abs(lock)
	if err != nil {
		return err
	}
	owned, err := ownsSessionLock(ctx, p, lock)
	if err != nil {
		return fmt.Errorf("Cannot inspect session lock ownership: %w", err)
	}
	if !owned {
		return fmt.Errorf("PID %d does not own the workspace lock file", info.PID)
	}
	// gopsutil caches birth time, so use a fresh process object immediately before signaling.
	p, err = gp.NewProcessWithContext(ctx, int32(info.PID))
	if err != nil {
		return err
	}
	created, err := p.CreateTimeWithContext(ctx)
	if err != nil || created != info.ProcessCreatedMillis {
		return fmt.Errorf("Session process identity changed")
	}
	return ctx.Err()
}

func ownsSessionLock(ctx context.Context, p *gp.Process, path string) (bool, error) {
	if runtime.GOOS == "darwin" {
		data, err := exec.CommandContext(ctx, "lsof", "-a", "-p", strconv.Itoa(int(p.Pid)), "-Fpf", "--", path).Output()
		if err != nil {
			return false, err
		}
		matchedPID := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "p") {
				matchedPID = line == "p"+strconv.Itoa(int(p.Pid))
			}
			if matchedPID && len(line) > 1 && line[0] == 'f' && line[1] >= '0' && line[1] <= '9' {
				return true, nil
			}
		}
		return false, nil
	}
	files, err := p.OpenFilesWithContext(ctx)
	if err != nil {
		return false, err
	}
	for _, file := range files {
		if file.Path == path {
			return true, nil
		}
	}
	return false, nil
}
