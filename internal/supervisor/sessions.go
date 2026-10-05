package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	gp "github.com/shirou/gopsutil/v4/process"
)

// Metadata is advisory. An occupied OS lock and the process birth time establish liveness.
type SessionInfo struct {
	Root                 string    `json:"root"`
	PID                  int       `json:"pid"`
	TTY                  string    `json:"tty,omitempty"`
	Terminal             string    `json:"terminal,omitempty"`
	StartedAt            time.Time `json:"started_at,omitempty"`
	ProcessCreatedMillis int64     `json:"process_created_millis,omitempty"`
	lockPath             string
}

type SessionConflict struct {
	Session SessionInfo
	Err     error
}

func (e *SessionConflict) Error() string {
	s := e.Session
	message := fmt.Sprintf("Workspace already has a StackHarbor session: %q", s.Root)
	if s.PID > 0 {
		message += fmt.Sprintf("\nPID: %d", s.PID)
	}
	if s.TTY != "" {
		message += fmt.Sprintf(" · TTY: %q", s.TTY)
	}
	if s.Terminal != "" {
		message += fmt.Sprintf(" · Terminal: %q", s.Terminal)
	}
	message += "\nList active sessions: stackharbor sessions"
	if s.PID > 0 {
		message += fmt.Sprintf("\nLocate the existing window: stackharbor sessions --focus %d", s.PID)
	}
	return message
}

func (e *SessionConflict) Unwrap() error { return e.Err }

func sessionCacheDir() (string, error) {
	if cache := os.Getenv("STACKHARBOR_CACHE_DIR"); cache != "" {
		return cache, nil
	}
	cache, err := os.UserCacheDir()
	return filepath.Join(cache, "stackharbor"), err
}

func currentSessionInfo(root string) SessionInfo {
	s := SessionInfo{Root: root, PID: os.Getpid(), Terminal: os.Getenv("TERM_PROGRAM"), StartedAt: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if p, err := gp.NewProcessWithContext(ctx, int32(s.PID)); err == nil {
		s.ProcessCreatedMillis, _ = p.CreateTimeWithContext(ctx)
	}
	cmd := exec.CommandContext(ctx, "tty")
	cmd.Stdin = os.Stdin
	if b, err := cmd.Output(); err == nil {
		s.TTY = normalizeTTY(string(b))
	}
	if os.Getenv("TMUX") != "" {
		s.Terminal = "tmux"
	} else if os.Getenv("STY") != "" {
		s.Terminal = "screen"
	} else if os.Getenv("SSH_CONNECTION") != "" {
		s.Terminal = "ssh"
	}
	return s
}

func normalizeTTY(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "?" || value == "??" || value == "-" || strings.ContainsAny(value, " \t\r\n") {
		return ""
	}
	if !strings.HasPrefix(value, "/dev/") {
		value = "/dev/" + value
	}
	return value
}

func writeSessionInfo(f *os.File, info SessionInfo) error {
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	return json.NewEncoder(f).Encode(info)
}

func readSessionInfo(f *os.File) SessionInfo {
	var info SessionInfo
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || st.Size() > 16*1024 {
		return info
	}
	if err := json.NewDecoder(io.NewSectionReader(f, 0, 16*1024)).Decode(&info); err != nil {
		return SessionInfo{}
	}
	return info
}

func validSessionInfo(info SessionInfo, path string) bool {
	if info.PID <= 0 || info.ProcessCreatedMillis <= 0 || !filepath.IsAbs(info.Root) {
		return false
	}
	if filepath.Base(path) != fmt.Sprintf("%x.lock", sha256.Sum256([]byte(info.Root))) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p, err := gp.NewProcessWithContext(ctx, int32(info.PID))
	if err != nil {
		return false
	}
	created, err := p.CreateTimeWithContext(ctx)
	return err == nil && created == info.ProcessCreatedMillis
}

// Old binaries left empty lock files. Recover their owner without changing those files.
func legacySessionInfo(path string) SessionInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "lsof", "-Fpc", "--", path).Output()
	if err != nil {
		return SessionInfo{}
	}
	pid := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "p") {
			pid, _ = strconv.Atoi(line[1:])
		}
		if strings.HasPrefix(line, "cstackharbor") && pid > 0 {
			p, err := gp.NewProcessWithContext(ctx, int32(pid))
			if err != nil {
				continue
			}
			s := SessionInfo{PID: pid}
			s.ProcessCreatedMillis, _ = p.CreateTimeWithContext(ctx)
			if s.ProcessCreatedMillis > 0 {
				s.StartedAt = time.UnixMilli(s.ProcessCreatedMillis).UTC()
			}
			cwd, _ := p.CwdWithContext(ctx)
			if real, err := filepath.EvalSymlinks(cwd); err == nil && filepath.Base(path) == fmt.Sprintf("%x.lock", sha256.Sum256([]byte(real))) {
				s.Root = real
			}
			if tty, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "tty=").Output(); err == nil {
				s.TTY = normalizeTTY(string(tty))
			}
			return s
		}
	}
	return SessionInfo{}
}

// ListSessions never creates or deletes registry files. Unlocked files are historical records.
func ListSessions() ([]SessionInfo, error) {
	cache, err := sessionCacheDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(cache)
	if os.IsNotExist(err) {
		return []SessionInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := []SessionInfo{}
	for _, entry := range entries {
		key := strings.TrimSuffix(entry.Name(), ".lock")
		if entry.Name() == key || len(key) != 64 {
			continue
		}
		if _, err := hex.DecodeString(key); err != nil {
			continue
		}
		path := filepath.Join(cache, entry.Name())
		info, active, err := activeSession(path)
		if err != nil || !active {
			continue
		}
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Root == result[j].Root {
			return result[i].PID < result[j].PID
		}
		return result[i].Root < result[j].Root
	})
	return result, nil
}

func activeSession(path string) (SessionInfo, bool, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if os.IsNotExist(err) {
		return SessionInfo{}, false, nil
	}
	if err != nil {
		return SessionInfo{}, false, err
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return SessionInfo{}, false, fmt.Errorf("Invalid session lock file: %s", path)
	}
	err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		f.Close()
		return SessionInfo{}, false, nil
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
		f.Close()
		return SessionInfo{}, false, err
	}
	info := readSessionInfo(f)
	f.Close()
	if !validSessionInfo(info, path) {
		info = legacySessionInfo(path)
	}
	info.lockPath = path
	return info, true, nil
}
