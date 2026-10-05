package observe

import (
	"context"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"os"
	"strings"
	"syscall"
	"time"
)

func FindConflicts(ctx context.Context, probe PortProbe, reader process.Reader, ports []model.Port, owned []model.ProcessIdentity) ([]model.PortConflict, error) {
	out := []model.PortConflict{}
	declared := map[int]bool{}
	for _, p := range ports {
		declared[p.Number] = true
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, p := range probe.Observe(ctx, ports, owned) {
		if !declared[p.Port] {
			continue
		}
		if p.Status == "unknown" {
			return nil, fmt.Errorf("Port %d status unknown; cannot release: %s", p.Port, p.Reason)
		}
		if p.Status != "external" {
			continue
		}
		for _, l := range p.Listeners {
			v, err := reader.Read(ctx, l.PID)
			if err != nil {
				return nil, fmt.Errorf("Unable to verify port %d PID %d: %w", p.Port, l.PID, err)
			}
			own := false
			for _, id := range owned {
				if id == v.Identity {
					own = true
				}
			}
			if own {
				continue
			}
			cmd := strings.ToLower(l.Command)
			if l.PID <= 1 || int(l.PID) == os.Getpid() || strings.Contains(cmd, "docker") || strings.Contains(cmd, "colima") || strings.Contains(cmd, "com.dock") || strings.Contains(cmd, "gvproxy") || strings.Contains(cmd, "qemu") || strings.Contains(cmd, "ssh") {
				return nil, fmt.Errorf("Port %d is forwarded or protected by %s (PID %d); stop the service in Docker or its original session", p.Port, l.Command, l.PID)
			}
			out = append(out, model.PortConflict{Port: p.Port, Command: l.Command, Identity: v.Identity})
		}
	}
	return out, nil
}
func ReleaseConflicts(ctx context.Context, probe PortProbe, reader process.Reader, targets []model.PortConflict) error {
	// Recheck the entire approved plan before sending any signal; a changed PID never inherits consent.
	ports := []model.Port{}
	for _, t := range targets {
		ports = append(ports, model.Port{Number: t.Port})
	}
	current, err := FindConflicts(ctx, probe, reader, ports, nil)
	if err != nil {
		return err
	}
	for _, c := range current {
		approved := false
		for _, t := range targets {
			if c.Port == t.Port && c.Identity == t.Identity {
				approved = true
			}
		}
		if !approved {
			return fmt.Errorf("Port listener changed; retry the start action and confirm again")
		}
	}
	ids := map[model.ProcessIdentity]bool{}
	for _, c := range current {
		ids[c.Identity] = true
	}
	deadline := time.Now().Add(3 * time.Second)
	signalled := map[model.ProcessIdentity]syscall.Signal{}
	for len(ids) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		for id := range ids {
			v, e := reader.Read(ctx, id.PID)
			if errors.Is(e, os.ErrNotExist) {
				delete(ids, id)
				continue
			}
			if e != nil {
				return e
			}
			if v.Identity != id || !process.Live(v) {
				delete(ids, id)
				continue
			}
			sig := syscall.SIGTERM
			if time.Now().After(deadline) {
				sig = syscall.SIGKILL
			}
			if signalled[id] == sig {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if e = syscall.Kill(int(id.PID), sig); e != nil && !errors.Is(e, syscall.ESRCH) {
				return e
			}
			signalled[id] = sig
		}
		if time.Now().After(deadline.Add(2 * time.Second)) {
			return fmt.Errorf("Conflicting process did not exit; check whether its launcher restarts it")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return probe.CheckStart(ctx, ports)
}
