package runner

import (
	"context"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"os"
)

func (h *handle) capture(ctx context.Context) error {
	all, listErr := h.reader.List(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	valid := map[int32]bool{}
	for pid, id := range h.owned {
		p, e := h.reader.Read(ctx, pid)
		if errors.Is(e, os.ErrNotExist) {
			delete(h.owned, pid)
			continue
		}
		if e == nil && p.Identity == id {
			if process.Live(p) {
				valid[pid] = true
			} else {
				delete(h.owned, pid)
			}
		}
	}
	// A topology failure must not leave already-exited identities in the owned
	// set, but it still prevents us from proving that all descendants are gone.
	if listErr != nil {
		return fmt.Errorf("cannot inspect process tree; descendant cleanup cannot be verified: %w", listErr)
	}
	for changed := true; changed; {
		changed = false
		for _, p := range all {
			pid := p.Identity.PID
			if !valid[pid] && valid[p.PPID] && process.Live(p) {
				info, e := h.reader.Read(ctx, pid)
				if e == nil && info.PPID == p.PPID && process.Live(info) {
					h.owned[pid] = info.Identity
					valid[pid] = true
					changed = true
				}
			}
		}
	}
	return nil
}
func verifiedSignal(ctx context.Context, reader process.Reader, id model.ProcessIdentity, sig func(int) error) error {
	info, e := reader.Read(ctx, id.PID)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if info.Identity != id {
		return fmt.Errorf("PID identity changed; refused signal (expected %d/%d, observed %d/%d)", id.PID, id.CreatedMillis, info.Identity.PID, info.Identity.CreatedMillis)
	}
	if !process.Live(info) {
		return nil
	}
	return sig(int(id.PID))
}
