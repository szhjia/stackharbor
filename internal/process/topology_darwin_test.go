//go:build darwin

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func topologyFixture(pid, ppid, pgid int32, state int8) unix.KinfoProc {
	var row unix.KinfoProc
	row.Proc.P_pid = pid
	row.Proc.P_stat = state
	row.Eproc.Ppid = ppid
	row.Eproc.Pgid = pgid
	return row
}
func TestDarwinTopologyStatusAndShapeValidation(t *testing.T) {
	self := int32(42)
	rows := []unix.KinfoProc{topologyFixture(self, 1, self, 2), topologyFixture(43, self, self, 5)}
	result, err := darwinTopologyRows(rows, self)
	if err != nil || len(result) != 2 || result[0].PPID != 1 || result[0].PGID != self || result[1].Status != "zombie" || Live(result[1]) {
		t.Fatalf("topology/status contract: %+v %v", result, err)
	}
	if result[0].Identity.CreatedMillis != 0 || result[1].Identity.CreatedMillis != 0 {
		t.Fatal("topology accidentally established ownership births")
	}
	cases := map[string][]unix.KinfoProc{
		"empty":          nil,
		"self missing":   {topologyFixture(43, 1, 43, 2)},
		"duplicate PID":  {rows[0], rows[0]},
		"invalid PID":    {rows[0], topologyFixture(-1, self, self, 2)},
		"invalid parent": {topologyFixture(self, -1, self, 2)},
		"invalid group":  {topologyFixture(self, 1, -1, 2)},
		"invalid status": {topologyFixture(self, 1, self, 0)},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := darwinTopologyRows(bad, self); err == nil || got != nil {
				t.Fatalf("invalid snapshot accepted: %+v %v", got, err)
			}
		})
	}
}
func TestDarwinTopologyPermissionAndCancellationFailClosed(t *testing.T) {
	calls := 0
	load := func(name string, _ ...int) ([]unix.KinfoProc, error) {
		calls++
		if name != "kern.proc.all" {
			t.Fatal(name)
		}
		return nil, syscall.EPERM
	}
	if got, err := readDarwinTopology(context.Background(), load); !errors.Is(err, syscall.EPERM) || got != nil {
		t.Fatalf("permission failure became empty success: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := readDarwinTopology(ctx, load); !errors.Is(err, context.Canceled) || got != nil || calls != 1 {
		t.Fatalf("cancellation invoked kernel read: %+v %v calls=%d", got, err, calls)
	}
	ctx, cancel = context.WithCancel(context.Background())
	if got, err := readDarwinTopology(ctx, func(string, ...int) ([]unix.KinfoProc, error) {
		cancel()
		return []unix.KinfoProc{topologyFixture(int32(os.Getpid()), 1, 1, 2)}, nil
	}); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("late cancellation was lost: %+v %v", got, err)
	}
}
func TestDarwinSharedAndFreshTopologyUseNoPSAndSeeOwnChild(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "ps-was-invoked")
	script := "#!/bin/sh\nprintf called > \"$STACKHARBOR_PS_MARKER\"\nexec /bin/ps \"$@\"\n"
	if err := os.WriteFile(filepath.Join(root, "ps"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("STACKHARBOR_PS_MARKER", marker)
	shared := NewSharedReader(NewReader(), func() time.Duration { return time.Hour })
	before, err := shared.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	self := int32(os.Getpid())
	selfGroup, err := syscall.Getpgid(int(self))
	if err != nil {
		t.Fatal(err)
	}
	foundSelf := false
	for _, info := range before {
		if info.Identity.PID == self {
			foundSelf = true
			if info.PPID != int32(os.Getppid()) || info.PGID != int32(selfGroup) || !Live(info) {
				t.Fatalf("own topology differs from live process: %+v", info)
			}
		}
	}
	if !foundSelf {
		t.Fatal("own process absent from kernel snapshot")
	}
	child := exec.Command("/bin/sleep", "60")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	childPID := int32(child.Process.Pid)
	cached, err := shared.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range cached {
		if info.Identity.PID == childPID {
			t.Fatal("fixture unexpectedly refreshed the cached snapshot")
		}
	}
	fresh, err := FreshTopology(context.Background(), shared)
	if err != nil {
		t.Fatal(err)
	}
	foundChild := false
	for _, info := range fresh {
		if info.Identity.PID == childPID {
			foundChild = true
			if info.PPID != self || info.PGID != childPID || !Live(info) {
				t.Fatalf("child topology differs from fresh identity: %+v", info)
			}
		}
	}
	if !foundChild {
		t.Fatal("fresh safety path reused pre-child topology")
	}
	InvalidateTopology(shared)
	refreshed, err := shared.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundChild = false
	for _, info := range refreshed {
		if info.Identity.PID == childPID {
			foundChild = true
		}
	}
	if !foundChild {
		t.Fatal("shared native topology did not refresh")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("managed topology spawned ps: %v", err)
	}
}
func TestDarwinNativeTopologyReportsUnreapedZombie(t *testing.T) {
	child := exec.Command("/bin/sh", "-c", "exit 0")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	deadline := time.Now().Add(2 * time.Second)
	for {
		all, err := FreshTopology(context.Background(), NewReader())
		if err != nil {
			t.Fatal(err)
		}
		for _, info := range all {
			if info.Identity.PID == int32(child.Process.Pid) && strings.Contains(info.Status, "zombie") {
				if Live(info) {
					t.Fatal("zombie classified live")
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("unreaped fixture was not observed as a zombie")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
