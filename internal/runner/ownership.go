package runner

import (
	"context"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"os"
	"slices"
	"sort"
	"syscall"
	"time"
)

func (h *handle) capture(ctx context.Context) error {
	return h.captureWith(ctx, h.reader)
}
func (h *handle) captureFresh(ctx context.Context) error {
	return h.captureWith(ctx, process.FreshReader(h.reader))
}
func (h *handle) captureWith(ctx context.Context, reader process.Reader) error {
	h.captureMu.Lock()
	defer h.captureMu.Unlock()
	var all []process.Info
	var revision uint64
	var listErr error
	if h.journal != nil {
		all, revision, listErr = process.TopologySince(ctx, reader, h.lastTopology)
	} else {
		all, listErr = reader.List(ctx)
	}
	if h.journal != nil && revision > 0 && revision == h.lastTopology && listErr == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	valid := map[int32]bool{}
	for pid, id := range h.owned {
		p, e := process.ReadIdentity(ctx, reader, pid)
		if errors.Is(e, os.ErrNotExist) {
			delete(h.owned, pid)
			continue
		}
		if e == nil && p.Identity != id {
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
				info, e := process.ReadIdentity(ctx, reader, pid)
				if e == nil && info.Identity.PID == pid && info.PPID == p.PPID && process.Live(info) {
					h.owned[pid] = info.Identity
					valid[pid] = true
					changed = true
				}
			}
		}
	}
	if h.journal != nil && !h.journalFinished {
		ids := make([]model.ProcessIdentity, 0, len(h.owned))
		for _, id := range h.owned {
			ids = append(ids, id)
		}
		// Retain the durable root anchor until the entire launch group has been
		// proved empty. A fast root can exit before its children enter owned.
		if h.rootIdentity.PID > 0 {
			if id, exists := h.owned[h.rootIdentity.PID]; !exists || id != h.rootIdentity {
				ids = append(ids, h.rootIdentity)
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i].PID < ids[j].PID })
		if !slices.Equal(ids, h.persisted) {
			if err := h.journal.Updated(h.launchID, ids); err != nil {
				return fmt.Errorf("cannot persist owned process identities: %w", err)
			}
			h.persisted = ids
		}
	}
	h.lastTopology = revision
	return nil
}
func (h *handle) finishJournal() error {
	h.captureMu.Lock()
	defer h.captureMu.Unlock()
	if h.journal == nil || h.journalFinished {
		return nil
	}
	if err := h.journal.Finished(h.launchID, true); err != nil {
		return fmt.Errorf("cannot finish launch journal: %w", err)
	}
	h.journalFinished = true
	return nil
}
func verifiedSignal(ctx context.Context, reader process.Reader, id model.ProcessIdentity, sig func(int) error) error {
	info, e := process.ReadIdentity(ctx, reader, id.PID)
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

// verifyGroupEmpty never signals or adopts unknown group members. An orphan
// can retain the launch PGID after its parent disappeared before observation.
// ESRCH proves this group is absent; reuse/permissions/unknown errors require
// explicit recovery rather than permitting an incorrect clean journal purge.
func (h *handle) verifyGroupEmpty(ctx context.Context) error {
	if h.journal == nil || h.rootIdentity.PID <= 0 {
		return nil
	}
	probe := h.groupProbe
	if probe == nil {
		probe = syscall.Kill
	}
	reapDeadline := time.Now().Add(250 * time.Millisecond)
	for {
		err := probe(-int(h.rootIdentity.PID), 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: cannot prove launch process group %d empty: %v", ErrRecoveryRequired, h.rootIdentity.PID, err)
		}
		// cmd.Wait reaps the root concurrently with explicit Stop. Give a verified
		// zombie root a bounded chance to be reaped before declaring uncertainty.
		root, readErr := process.ReadIdentity(ctx, h.reader, h.rootIdentity.PID)
		if readErr == nil && root.Identity == h.rootIdentity && !process.Live(root) && time.Now().Before(reapDeadline) {
			select {
			case <-ctx.Done():
				return fmt.Errorf("%w: %v", ErrRecoveryRequired, ctx.Err())
			case <-time.After(10 * time.Millisecond):
				continue
			}
		}
		return fmt.Errorf("%w: launch process group %d remains; unobserved descendants may still be running", ErrRecoveryRequired, h.rootIdentity.PID)
	}
}

func (h *handle) markRecovery() error {
	h.captureMu.Lock()
	defer h.captureMu.Unlock()
	if h.journal == nil || h.journalFinished || h.journalRecovery {
		return nil
	}
	if err := h.journal.Finished(h.launchID, false); err != nil {
		return fmt.Errorf("cannot persist required recovery: %w", err)
	}
	h.journalRecovery = true
	return nil
}
