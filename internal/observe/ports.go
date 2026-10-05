package observe

import (
	"context"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type PortProbe interface {
	Observe(context.Context, []model.Port, []model.ProcessIdentity) []model.PortObservation
	CheckStart(context.Context, []model.Port) error
}
type portProbe struct{}

func NewPortProbe() PortProbe { return portProbe{} }
func parsePorts(b []byte) (map[int][]model.Listener, error) {
	out := map[int][]model.Listener{}
	var pid int32
	command := ""
	for _, line := range strings.Split(string(b), "\n") {
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case 'p':
			n, e := strconv.ParseInt(line[1:], 10, 32)
			if e != nil {
				return nil, e
			}
			pid = int32(n)
		case 'c':
			command = line[1:]
		case 'n':
			i := strings.LastIndex(line, ":")
			if i < 0 || pid == 0 {
				return nil, fmt.Errorf("malformed listener")
			}
			n, e := strconv.Atoi(strings.TrimSuffix(line[i+1:], " (LISTEN)"))
			if e != nil {
				return nil, e
			}
			out[n] = append(out[n], model.Listener{PID: pid, Command: command, Address: line[1:]})
		}
	}
	return out, nil
}
func (portProbe) Observe(ctx context.Context, declared []model.Port, owned []model.ProcessIdentity) []model.PortObservation {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, e := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcn").Output()
	known := true
	reason := ""
	if e != nil {
		var exit *exec.ExitError
		if !errors.As(e, &exit) || len(exit.Stderr) > 0 || ctx.Err() != nil {
			known = false
			reason = e.Error()
		}
	}
	listeners, err := parsePorts(b)
	if err != nil {
		known = false
		reason = err.Error()
	}
	ownedPids := map[int32]bool{}
	reader := process.NewReader()
	for _, id := range owned {
		if p, e := reader.Read(ctx, id.PID); e == nil && p.Identity == id {
			ownedPids[id.PID] = true
		}
	}
	ports := map[int]bool{}
	for _, p := range declared {
		ports[p.Number] = true
	}
	for n, ls := range listeners {
		for _, l := range ls {
			if ownedPids[l.PID] {
				ports[n] = true
			}
		}
	}
	nums := []int{}
	for n := range ports {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	out := []model.PortObservation{}
	for _, n := range nums {
		p := model.PortObservation{Port: n, Status: "free", Listeners: listeners[n]}
		if !known {
			p.Status = "unknown"
			p.Reason = reason
		} else if len(p.Listeners) > 0 {
			p.Status = "owned"
			for _, l := range p.Listeners {
				if !ownedPids[l.PID] {
					p.Status = "external"
				}
			}
		}
		out = append(out, p)
	}
	return out
}
func (portProbe) CheckStart(ctx context.Context, ports []model.Port) error {
	for _, obs := range (portProbe{}).Observe(ctx, ports, nil) {
		if obs.Status == "external" {
			return fmt.Errorf("Port %d is held by an external process; confirm release before starting", obs.Port)
		}
	}
	for _, p := range ports {
		for _, network := range []string{"tcp4", "tcp6"} {
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Number))
			if network == "tcp6" {
				addr = net.JoinHostPort("::1", strconv.Itoa(p.Number))
			}
			cfg := net.ListenConfig{}
			l, e := cfg.Listen(ctx, network, addr)
			if e != nil {
				if network == "tcp6" && (errors.Is(e, syscall.EAFNOSUPPORT) || errors.Is(e, syscall.EADDRNOTAVAIL)) {
					continue
				}
				return fmt.Errorf("Port %d unavailable: %w", p.Number, e)
			}
			l.Close()
		}
	}
	return nil
}
