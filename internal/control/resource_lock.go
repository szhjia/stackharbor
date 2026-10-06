package control

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// AcquireResourceLocks uses one private per-user namespace across all workspace
// cache directories. Files are retained: removing a flock inode breaks exclusion.
func AcquireResourceLocks(ctx context.Context, namespace string, keys []string) (func(), error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("stackharbor-resource-locks-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Getuid()) || !st.IsDir() || st.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("resource lock directory must be private and owned by current user")
	}
	ordered := append([]string{}, keys...)
	sort.Strings(ordered)
	files := []*os.File{}
	var once sync.Once
	release := func() {
		once.Do(func() {
			for i := len(files) - 1; i >= 0; i-- {
				_ = syscall.Flock(int(files[i].Fd()), syscall.LOCK_UN)
				_ = files[i].Close()
			}
		})
	}
	previous := ""
	for i, key := range ordered {
		if key == "" {
			release()
			return nil, fmt.Errorf("empty resource identity")
		}
		if i > 0 && key == previous {
			continue
		}
		previous = key
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		path := filepath.Join(dir, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(namespace+"\x00"+key))))
		fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
		if err != nil {
			release()
			return nil, err
		}
		f := os.NewFile(uintptr(fd), path)
		st, err := f.Stat()
		if err != nil {
			f.Close()
			release()
			return nil, err
		}
		stat, ok := st.Sys().(*syscall.Stat_t)
		if !ok || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
			f.Close()
			release()
			return nil, fmt.Errorf("unsafe resource lock file")
		}
		files = append(files, f)
		for {
			err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
			if err == nil {
				break
			}
			if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
				release()
				return nil, err
			}
			select {
			case <-ctx.Done():
				release()
				return nil, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	return release, nil
}

type lockContextKey struct{}
type lockScope struct {
	mu        sync.Mutex
	namespace string
	keys      map[string]bool
	leases    int
	release   func()
}
type lockLease struct {
	scope  *lockScope
	active bool
}

// WithResourceLocks borrows a held subset. Each lease is individually revoked,
// while active nested leases retain the flock until their side effects finish.
func WithResourceLocks(ctx context.Context, namespace string, keys []string) (context.Context, func(), error) {
	if lease, ok := ctx.Value(lockContextKey{}).(*lockLease); ok {
		scope := lease.scope
		scope.mu.Lock()
		covered := lease.active && scope.namespace == namespace
		for _, key := range keys {
			covered = covered && scope.keys[key]
		}
		if covered {
			scope.leases++
			nested := &lockLease{scope: scope, active: true}
			scope.mu.Unlock()
			return context.WithValue(ctx, lockContextKey{}, nested), releaseLease(nested), nil
		}
		overlap := false
		if lease.active && scope.namespace == namespace {
			for _, key := range keys {
				overlap = overlap || scope.keys[key]
			}
		}
		scope.mu.Unlock()
		if overlap {
			return ctx, nil, &APIError{Code: "identity_conflict", Message: "Resource lock scope changed; request a new plan"}
		}
	}
	release, err := AcquireResourceLocks(ctx, namespace, keys)
	if err != nil {
		return ctx, nil, err
	}
	scope := &lockScope{namespace: namespace, keys: map[string]bool{}, leases: 1, release: release}
	for _, key := range keys {
		scope.keys[key] = true
	}
	lease := &lockLease{scope: scope, active: true}
	return context.WithValue(ctx, lockContextKey{}, lease), releaseLease(lease), nil
}
func releaseLease(lease *lockLease) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			scope := lease.scope
			scope.mu.Lock()
			lease.active = false
			scope.leases--
			if scope.leases == 0 {
				scope.release()
			}
			scope.mu.Unlock()
		})
	}
}

// InheritResourceLocks copies only the current operation capability, preserving
// runtime cancellation/deadline semantics. Completed leases cannot authorize reuse.
func InheritResourceLocks(dst, src context.Context) context.Context {
	if lease, ok := src.Value(lockContextKey{}).(*lockLease); ok {
		return context.WithValue(dst, lockContextKey{}, lease)
	}
	return dst
}

const ResourceLockNamespace = "physical"

func ProcessLockKey(id model.ProcessIdentity) string {
	return fmt.Sprintf("process:%d:%d", id.PID, id.CreatedMillis)
}
