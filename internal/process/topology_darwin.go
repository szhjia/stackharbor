//go:build darwin

package process

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/szhjia/stackharbor/internal/model"
	"golang.org/x/sys/unix"
)

// platformTopology replaces only the concrete production reader's managed
// observation path. Legacy reader.List and custom/fake Reader contracts remain
// untouched. One kernel snapshot avoids spawning an entire-machine ps helper.
func platformTopology(ctx context.Context, source Reader) ([]Info, error) {
	if _, native := source.(reader); !native {
		return source.List(ctx)
	}
	return readDarwinTopology(ctx, unix.SysctlKinfoProcSlice)
}

func readDarwinTopology(ctx context.Context, load func(string, ...int) ([]unix.KinfoProc, error)) ([]Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := load("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("cannot read Darwin process topology: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return darwinTopologyRows(rows, int32(os.Getpid()))
}

func darwinTopologyRows(rows []unix.KinfoProc, self int32) ([]Info, error) {
	if len(rows) == 0 || len(rows) > 1<<20 {
		return nil, errors.New("Darwin process topology has an invalid size")
	}
	out := make([]Info, 0, len(rows))
	seen := make(map[int32]bool, len(rows))
	foundSelf := false
	for _, row := range rows {
		pid := row.Proc.P_pid
		status, valid := darwinProcessStatus(row.Proc.P_stat)
		if pid < 0 || row.Eproc.Ppid < 0 || row.Eproc.Pgid < 0 || !valid || seen[pid] {
			return nil, errors.New("Darwin process topology has invalid or duplicate process data")
		}
		seen[pid] = true
		if pid == self {
			foundSelf = true
		}
		// Birth identity is deliberately established by a subsequent fresh
		// ReadIdentity before ownership expansion or any individual PID signal.
		out = append(out, Info{Identity: model.ProcessIdentity{PID: pid}, PPID: row.Eproc.Ppid, PGID: row.Eproc.Pgid, Status: status})
	}
	if !foundSelf {
		return nil, errors.New("Darwin process topology does not include the observing process")
	}
	return out, nil
}
func darwinProcessStatus(state int8) (string, bool) {
	// Darwin sys/proc.h: SIDL, SRUN, SSLEEP, SSTOP and SZOMB.
	switch state {
	case 1:
		return "idle", true
	case 2:
		return "running", true
	case 3:
		return "sleeping", true
	case 4:
		return "stopped", true
	case 5:
		return "zombie", true
	default:
		return "", false
	}
}
