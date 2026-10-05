package supervisor

import (
	"crypto/sha256"
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
	cache := os.Getenv("STACKHARBOR_CACHE_DIR")
	if cache == "" {
		cache, e = os.UserCacheDir()
		if e != nil {
			return nil, e
		}
		cache = filepath.Join(cache, "stackharbor")
	}
	if e = os.MkdirAll(cache, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(cache, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(real))))
	fd, e := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("Project already has a StackHarbor session, or session lock failed: %w", e)
	}
	return &Lock{file: f}, nil
}
func (l *Lock) Close() error { l.once.Do(func() { l.err = l.file.Close() }); return l.err }
