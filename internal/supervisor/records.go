package supervisor

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Bounded event journal, separate from ephemeral UI log buffers; called under session.mu.
func (s *Session) record(e model.Event) {
	if s.lock == nil || s.lock.file == nil {
		return
	}
	path := s.lock.file.Name() + ".events.jsonl"
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() {
			return
		}
		if st.Size() > 2*1024*1024 {
			old := path + ".previous"
			if info, e := os.Lstat(old); e == nil && !info.Mode().IsRegular() {
				return
			}
			if os.Rename(path, old) != nil {
				return
			}
		}
	}
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_WRONLY|syscall.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return
	}
	f := os.NewFile(uintptr(fd), filepath.Base(path))
	defer f.Close()
	f.Chmod(0600)
	_ = json.NewEncoder(f).Encode(e)
}

// ReadHistory does not acquire a session lock or create files, and can inspect a live session.
func ReadHistory(root string) ([]model.Event, error) {
	var err error
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cache := os.Getenv("STACKHARBOR_CACHE_DIR")
	if cache == "" {
		var err error
		cache, err = os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		cache = filepath.Join(cache, "stackharbor")
	}
	path := filepath.Join(cache, fmt.Sprintf("%x.lock.events.jsonl", sha256.Sum256([]byte(root))))
	all := []model.Event{}
	for _, file := range []string{path + ".previous", path} {
		fd, err := syscall.Open(file, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		f := os.NewFile(uintptr(fd), filepath.Base(file))
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() || st.Size() > 3*1024*1024 {
			f.Close()
			return nil, fmt.Errorf("Invalid history file format or size")
		}
		dec := json.NewDecoder(io.LimitReader(f, 3*1024*1024))
		for {
			var event model.Event
			err = dec.Decode(&event)
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			if err != nil {
				f.Close()
				return nil, fmt.Errorf("Corrupt history record")
			}
			all = append(all, event)
		}
		f.Close()
	}
	return all, nil
}
