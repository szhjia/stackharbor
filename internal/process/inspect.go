package process

import (
	"context"
	"errors"
	gp "github.com/shirou/gopsutil/v4/process"
	"github.com/szhjia/stackharbor/internal/model"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

type Info struct {
	Identity              model.ProcessIdentity
	PPID, PGID            int32
	Status                string
	RSS                   uint64
	CPUSeconds            float64
	MetricKnown           bool
	MemoryKnown, CPUKnown bool
}
type Reader interface {
	List(context.Context) ([]Info, error)
	Read(context.Context, int32) (Info, error)
}
type reader struct{}

func NewReader() Reader { return reader{} }
func inspect(ctx context.Context, pid int32, metrics bool) (Info, error) {
	p, err := gp.NewProcessWithContext(ctx, pid)
	if err != nil {
		return Info{}, err
	}
	created, err := p.CreateTimeWithContext(ctx)
	if err != nil {
		return Info{}, err
	}
	ppid, err := p.PpidWithContext(ctx)
	if err != nil {
		return Info{}, err
	}
	pgid, err := syscall.Getpgid(int(pid))
	if err != nil {
		return Info{}, err
	}
	status, _ := p.StatusWithContext(ctx)
	info := Info{Identity: model.ProcessIdentity{PID: pid, CreatedMillis: created}, PPID: ppid, PGID: int32(pgid), Status: strings.Join(status, ",")}
	if metrics {
		m, e := p.MemoryInfoWithContext(ctx)
		cpu, e2 := p.TimesWithContext(ctx)
		if e == nil {
			info.RSS = m.RSS
			info.MemoryKnown = true
		}
		if e2 == nil {
			info.CPUSeconds = cpu.User + cpu.System
			info.CPUKnown = true
		}
		info.MetricKnown = info.MemoryKnown && info.CPUKnown
	}
	return info, nil
}

// List provides parent/group topology; identities are established by Read for owned candidates.
func (reader) List(ctx context.Context) ([]Info, error) {
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,pgid=,stat=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	b, e := cmd.Output()
	if e != nil {
		return nil, e
	}
	out := []Info{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			continue
		}
		pid, e := strconv.ParseInt(f[0], 10, 32)
		if e != nil {
			continue
		}
		ppid, e := strconv.ParseInt(f[1], 10, 32)
		if e != nil {
			continue
		}
		pgid, e := strconv.ParseInt(f[2], 10, 32)
		if e != nil {
			continue
		}
		status := f[3]
		if strings.HasPrefix(status, "Z") {
			status = "zombie"
		}
		out = append(out, Info{Identity: model.ProcessIdentity{PID: int32(pid)}, PPID: int32(ppid), PGID: int32(pgid), Status: status})
	}
	return out, nil
}
func (reader) Read(ctx context.Context, pid int32) (Info, error) {
	v, e := inspect(ctx, pid, true)
	if e != nil {
		if errors.Is(e, syscall.ESRCH) || errors.Is(e, gp.ErrorProcessNotRunning) || errors.Is(syscall.Kill(int(pid), 0), syscall.ESRCH) {
			return Info{}, os.ErrNotExist
		}
	}
	return v, e
}
func Live(info Info) bool { return !strings.Contains(info.Status, "zombie") && info.Status != "Z" }
