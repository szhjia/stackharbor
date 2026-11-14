//go:build darwin

package process

import (
	"context"
	"os"

	"github.com/szhjia/stackharbor/internal/model"
	"golang.org/x/sys/unix"
)

// Use one kernel snapshot. gopsutil's Darwin StatusWithContext invokes ps for
// each PID, which defeats shared topology observation for busy workspaces.
func nativeIdentity(ctx context.Context, pid int32) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
	if err != nil {
		return Info{}, err
	}
	if proc.Proc.P_pid != pid {
		return Info{}, os.ErrNotExist
	}
	status := "running"
	// Darwin sys/proc.h: SZOMB is the state awaiting collection by its parent.
	if proc.Proc.P_stat == 5 {
		status = "zombie"
	}
	return Info{Identity: model.ProcessIdentity{PID: pid, CreatedMillis: proc.Proc.P_starttime.Sec*1000 + int64(proc.Proc.P_starttime.Usec)/1000}, PPID: proc.Eproc.Ppid, PGID: proc.Eproc.Pgid, Status: status}, nil
}
