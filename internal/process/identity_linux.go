//go:build linux

package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tklauser/go-sysconf"
)

var linuxClockTicks = sync.OnceValues(func() (uint64, error) {
	ticks, err := sysconf.Sysconf(sysconf.SC_CLK_TCK)
	if err != nil || ticks <= 0 {
		return 0, errors.New("cannot establish Linux clock ticks")
	}
	return uint64(ticks), nil
})

// CreatedMillis uses process start ticks and a boot-ID-bound epoch. Both uptime
// estimates and /proc/stat btime can move when the wall clock is corrected. All
// same-user CLI processes share one epoch for that boot, preserving strict PID
// birth comparisons while retaining Unix milliseconds for uptime presentation.
func CreatedMillis(ctx context.Context, pid int32) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if pid <= 0 {
		return 0, errors.New("process PID must be positive")
	}
	root := os.Getenv("HOST_PROC")
	if root == "" {
		root = "/proc"
	}
	stat, err := os.ReadFile(filepath.Join(root, strconv.Itoa(int(pid)), "stat"))
	if err != nil {
		return 0, err
	}
	epoch, err := linuxBootEpoch(ctx, root, filepath.Join("/tmp", fmt.Sprintf("stackharbor-clock-%d", os.Geteuid())))
	if err != nil {
		return 0, err
	}
	ticks, err := linuxClockTicks()
	if err != nil {
		return 0, err
	}
	return linuxCreatedMillis(string(stat), fmt.Sprintf("btime %d", epoch), ticks)
}

func linuxBootEpoch(ctx context.Context, procRoot, directory string) (uint64, error) {
	boot, err := os.ReadFile(filepath.Join(procRoot, "sys/kernel/random/boot_id"))
	if err != nil {
		return 0, err
	}
	id := strings.TrimSpace(string(boot))
	if len(id) != 36 {
		return 0, errors.New("invalid Linux boot ID")
	}
	for i, c := range id {
		dash := i == 8 || i == 13 || i == 18 || i == 23
		if dash && c == '-' || !dash && (c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			continue
		}
		return 0, errors.New("invalid Linux boot ID")
	}
	if err := os.Mkdir(directory, 0700); err != nil && !os.IsExist(err) {
		return 0, err
	}
	st, err := os.Lstat(directory)
	if err != nil || !privateLinuxClock(st, true) {
		return 0, errors.New("Linux process clock directory is not private and owned")
	}
	fd, err := syscall.Open(filepath.Join(directory, id), syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return 0, err
	}
	f := os.NewFile(uintptr(fd), id)
	defer f.Close()
	st, err = f.Stat()
	if err != nil || !privateLinuxClock(st, false) || st.Size() > 32 {
		return 0, errors.New("Linux process clock file is not private and owned")
	}
	deadline := time.Now().Add(time.Second)
	for {
		if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			break
		} else if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return 0, err
		}
		if time.Now().After(deadline) {
			return 0, errors.New("Linux process clock lock timed out")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil {
		return 0, err
	}
	if len(data) != 0 {
		epoch, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if err != nil || epoch == 0 || epoch > math.MaxInt64/1000 {
			return 0, errors.New("invalid saved Linux boot epoch")
		}
		return epoch, nil
	}
	system, err := os.ReadFile(filepath.Join(procRoot, "stat"))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(system), "\n") {
		row := strings.Fields(line)
		if len(row) != 2 || row[0] != "btime" {
			continue
		}
		epoch, err := strconv.ParseUint(row[1], 10, 64)
		if err != nil || epoch == 0 || epoch > math.MaxInt64/1000 {
			return 0, errors.New("invalid Linux boot epoch")
		}
		if _, err := fmt.Fprintln(f, epoch); err != nil {
			return 0, err
		}
		return epoch, f.Sync()
	}
	return 0, errors.New("Linux boot epoch is unavailable")
}

func privateLinuxClock(st os.FileInfo, directory bool) bool {
	if st == nil || st.Mode()&os.ModeSymlink != 0 || st.IsDir() != directory {
		return false
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	} else if !st.Mode().IsRegular() {
		return false
	}
	return ok && int(owner.Uid) == os.Geteuid() && st.Mode().Perm() == mode
}

func linuxCreatedMillis(stat, system string, ticks uint64) (int64, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 || ticks == 0 {
		return 0, errors.New("invalid Linux process identity")
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) <= 19 {
		return 0, errors.New("incomplete Linux process stat")
	}
	started, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || started > math.MaxInt64/1000 {
		return 0, errors.New("invalid Linux process start ticks")
	}
	for _, line := range strings.Split(system, "\n") {
		row := strings.Fields(line)
		if len(row) != 2 || row[0] != "btime" {
			continue
		}
		boot, err := strconv.ParseUint(row[1], 10, 64)
		if err != nil || boot > math.MaxInt64/1000 || boot*1000 > math.MaxInt64-started*1000/ticks {
			return 0, errors.New("invalid Linux boot epoch")
		}
		return int64(boot*1000 + started*1000/ticks), nil
	}
	return 0, errors.New("Linux boot epoch is unavailable")
}
