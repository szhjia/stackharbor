package runner

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"syscall"
	"time"
)

func (h *handle) Stop(ctx context.Context, policy model.StopPolicy) StopResult {
	for !h.stopMu.TryLock() {
		select {
		case <-ctx.Done():
			return StopResult{Remaining: h.Identities(), Errors: []string{ctx.Err().Error()}}
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer h.stopMu.Unlock()
	if policy.Timeout <= 0 {
		policy.Timeout = 5 * time.Second
	}
	sig := syscall.SIGTERM
	if policy.Signal == "INT" {
		sig = syscall.SIGINT
	}
	deadline := time.Now().Add(policy.Timeout)
	errorsSeen := map[string]bool{}
	signalled := map[model.ProcessIdentity]syscall.Signal{}
	for {
		if err := h.capture(ctx); err != nil {
			errorsSeen[err.Error()] = true
		}
		ids := h.Identities()
		if len(ids) == 0 {
			return StopResult{Complete: true}
		}
		if ctx.Err() != nil {
			errorsSeen[ctx.Err().Error()] = true
			break
		}
		current := sig
		if time.Now().After(deadline) {
			current = syscall.SIGKILL
		}
		for _, id := range ids {
			if signalled[id] == current {
				continue
			}
			e := verifiedSignal(ctx, h.reader, id, func(pid int) error { return h.signal(pid, current) })
			if e != nil {
				errorsSeen[e.Error()] = true
			} else {
				signalled[id] = current
			}
		}
		if time.Now().After(deadline.Add(2 * time.Second)) {
			errorsSeen["processes remain after signal grace period"] = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	errs := []string{}
	for e := range errorsSeen {
		errs = append(errs, e)
	}
	return StopResult{Remaining: h.Identities(), Errors: errs}
}

var _ = fmt.Sprintf
var _ = process.Live
