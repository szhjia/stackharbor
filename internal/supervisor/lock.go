package supervisor

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

type Lock struct {
	file   *os.File
	mu     sync.Mutex
	info   SessionInfo
	closed bool
	once   sync.Once
	err    error
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
	if e = ensureSessionCache(cache); e != nil {
		return nil, e
	}
	cache, e = filepath.EvalSymlinks(cache)
	if e != nil {
		return nil, e
	}
	cache, e = filepath.Abs(cache)
	if e != nil {
		return nil, e
	}
	path := filepath.Join(cache, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(real))))
	fd, e := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || !ownedPrivateFile(st, 0600) {
		f.Close()
		return nil, fmt.Errorf("Invalid session lock file: %s", path)
	}
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		var info SessionInfo
		if errors.Is(e, syscall.EWOULDBLOCK) || errors.Is(e, syscall.EAGAIN) {
			info = readOccupiedSessionInfo(f, path)
		}
		f.Close()
		if errors.Is(e, syscall.EWOULDBLOCK) || errors.Is(e, syscall.EAGAIN) {
			info.Root = real
			return nil, &SessionConflict{Session: info, Err: e}
		}
		return nil, fmt.Errorf("Session lock failed for %q: %w", real, e)
	}
	info := currentSessionInfo(real)
	info.CacheDir = cache
	info.WorkspaceID = fmt.Sprintf("%x", sha256.Sum256([]byte(real)))
	info.NamespaceID = fmt.Sprintf("%x", sha256.Sum256([]byte(cache)))
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		f.Close()
		return nil, e
	}
	info.SessionID = fmt.Sprintf("%x", nonce)
	if e = writeSessionInfo(f, info); e != nil {
		f.Close()
		return nil, fmt.Errorf("Record session owner: %w", e)
	}
	return &Lock{file: f, info: info}, nil
}
func (l *Lock) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.once.Do(func() { l.closed = true; l.err = l.file.Close() })
	return l.err
}

// Endpoint only extends discovery metadata; the acquired lock and owner never change.
type Endpoint struct {
	SocketPath      string
	ProtocolVersion int
	Capabilities    []string
}

func (l *Lock) Info() SessionInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	info := l.info
	info.Capabilities = append([]string{}, info.Capabilities...)
	return info
}
func (l *Lock) PublishEndpoint(endpoint Endpoint) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return fmt.Errorf("session lock is closed")
	}
	st, err := l.file.Stat()
	if err != nil {
		return err
	}
	named, err := os.Lstat(l.file.Name())
	if err != nil {
		return err
	}
	if !os.SameFile(st, named) || !st.Mode().IsRegular() || !ownedPrivateFile(st, 0600) {
		return fmt.Errorf("session lock pathname or ownership changed")
	}
	info := l.info
	info.SocketPath = endpoint.SocketPath
	info.ProtocolVersion = endpoint.ProtocolVersion
	info.Capabilities = append([]string{}, endpoint.Capabilities...)
	if err := writeSessionInfo(l.file, info); err != nil {
		return err
	}
	l.info = info
	return nil
}

// EnsurePrivateDirectory creates or verifies a current-user private directory.
// Existing symlinks and broad permissions are rejected, never silently repaired.
func EnsurePrivateDirectory(path string) error {
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err = os.MkdirAll(path, 0700); err != nil {
			return err
		}
		st, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !st.IsDir() || !ownedPrivateFile(st, 0700) {
		return fmt.Errorf("unsafe private directory: %s", path)
	}
	return nil
}
func ownedPrivateFile(st os.FileInfo, mode os.FileMode) bool {
	owner, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(owner.Uid) == os.Getuid() && st.Mode().Perm() == mode && st.Mode()&os.ModeSymlink == 0
}

// Registry caches may be readable, but must not permit another user to replace
// lock paths. Runtime endpoint directories use the stricter 0700 helper.
func ensureSessionCache(path string) error {
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err = os.MkdirAll(path, 0700); err != nil {
			return err
		}
		st, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || !ok || int(owner.Uid) != os.Getuid() || st.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("unsafe session cache: %s", path)
	}
	return nil
}
