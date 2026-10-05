package supervisor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

func Acquire(root string) (*Lock, error) {
	real, e := filepath.EvalSymlinks(root)
	if e != nil {
		return nil, e
	}
	real, e = filepath.Abs(real)
	if e != nil {
		return nil, e
	}
	cache, e := sessionCacheDir()
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(cache, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(cache, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(real))))
	fd, e := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("Invalid session lock file: %s", path)
	}
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		var info SessionInfo
		if errors.Is(e, syscall.EWOULDBLOCK) || errors.Is(e, syscall.EAGAIN) {
			info = readSessionInfo(f)
		}
		f.Close()
		if errors.Is(e, syscall.EWOULDBLOCK) || errors.Is(e, syscall.EAGAIN) {
			if !validSessionInfo(info, path) {
				info = legacySessionInfo(path)
			}
			info.Root = real
			return nil, &SessionConflict{Session: info, Err: e}
		}
		return nil, fmt.Errorf("Session lock failed for %q: %w", real, e)
	}
	if e = writeSessionInfo(f, currentSessionInfo(real)); e != nil {
		f.Close()
		return nil, fmt.Errorf("Record session owner: %w", e)
	}
	return &Lock{file: f}, nil
}
func (l *Lock) Close() error { l.once.Do(func() { l.err = l.file.Close() }); return l.err }
