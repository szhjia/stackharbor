package supervisor

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Prepare and lock a complete record on an unpublished inode. Publishing that
// inode atomically means even old binaries never see an occupied blank record.
// Empty legacy records are deliberately not adopted: opening/locking one would
// let their lsof fallback mistake this shared engine for a legacy owner.
func acquireManaged(root, cache, path string, beforePublish func(SessionInfo)) (*Lock, error) {
	info := currentSessionInfo(root)
	info.OwnerKind = "engine"
	info.ProtocolVersion = 1
	info.TTY = ""
	info.Terminal = ""
	info.CacheDir = cache
	info.WorkspaceID = fmt.Sprintf("%x", sha256.Sum256([]byte(root)))
	info.NamespaceID = fmt.Sprintf("%x", sha256.Sum256([]byte(cache)))
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	info.SessionID = fmt.Sprintf("%x", nonce)
	prepared, err := os.CreateTemp(cache, ".managed-registration-*")
	if err != nil {
		return nil, err
	}
	pending := prepared.Name()
	defer os.Remove(pending)
	defer prepared.Close()
	if err = prepared.Chmod(0600); err != nil {
		return nil, err
	}
	if err = writeSessionInfo(prepared, info); err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(prepared.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, err
	}
	if beforePublish != nil {
		beforePublish(info)
	}
	publishedSource := prepared
	err = os.Link(pending, path)
	if os.IsExist(err) {
		st, e := os.Lstat(path)
		if e != nil {
			return nil, e
		}
		if !st.Mode().IsRegular() || !ownedPrivateFile(st, 0600) {
			return nil, fmt.Errorf("unsafe existing workspace lock")
		}
		if st.Size() == 0 {
			return nil, &SessionConflict{Session: SessionInfo{Root: root}, Err: fmt.Errorf("empty legacy lock requires its owner to be closed before managed migration")}
		}
		fd, e := syscall.Open(path, syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
		if e != nil {
			return nil, e
		}
		old := os.NewFile(uintptr(fd), path)
		defer old.Close()
		if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
			existing := readOccupiedSessionInfo(old, path)
			existing.Root = root
			return nil, &SessionConflict{Session: existing, Err: e}
		}
		current, e := os.Lstat(path)
		opened, statErr := old.Stat()
		if e != nil || statErr != nil || !os.SameFile(st, current) || !os.SameFile(current, opened) {
			return nil, fmt.Errorf("workspace lock changed during registration")
		}
		// Reuse an existing nonempty inode so legacy contenders with an already
		// opened descriptor cannot acquire an unlinked lock and double-own root.
		if e = writeSessionInfo(old, info); e != nil {
			return nil, e
		}
		publishedSource = old
	} else if err != nil {
		return nil, err
	}
	// Duplicate the locked open description to give records/history the public
	// pathname, retaining the flock across the temporary descriptor's close.
	duplicate, err := syscall.Dup(int(publishedSource.Fd()))
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(duplicate)
	published := os.NewFile(uintptr(duplicate), path)
	if st, e := published.Stat(); e != nil || !ownedPrivateFile(st, 0600) {
		published.Close()
		return nil, errors.New("invalid published managed registration")
	}
	return &Lock{file: published, info: info}, nil
}
