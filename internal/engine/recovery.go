package engine

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"net/http"
	"os"
	"sort"
	"syscall"
	"time"
)

type RecoveryProcess struct {
	Identity model.ProcessIdentity `json:"identity"`
	State    string                `json:"state"`
}
type RecoveryPlan struct {
	Root                 string            `json:"root"`
	Namespace            string            `json:"namespace"`
	PlanID               string            `json:"plan_id"`
	Processes            []RecoveryProcess `json:"processes"`
	RequiresManualReview bool              `json:"requires_manual_review"`
	Warnings             []string          `json:"warnings"`
}
type recoveryRequest struct {
	Root            string `json:"root"`
	Namespace       string `json:"namespace,omitempty"`
	Action          string `json:"action"`
	PlanID          string `json:"plan_id"`
	ReviewedUnknown bool   `json:"reviewed_unknown"`
}

func (s *Server) recoveryPlan(r *http.Request, root, namespace string) (RecoveryPlan, error) {
	request, err := s.normalize(OpenRequest{Root: root, Namespace: namespace})
	if err != nil {
		return RecoveryPlan{}, err
	}
	key := workspaceKey(request.Namespace, request.Root)
	s.state.mu.Lock()
	entry := s.state.entries[key]
	if entry == nil || !entry.RecoveryRequired {
		s.state.mu.Unlock()
		return RecoveryPlan{}, &control.APIError{Code: "not_found", Message: "Workspace has no pending recovery"}
	}
	raw, _ := json.Marshal(entry.Attempts)
	ids := map[model.ProcessIdentity]bool{}
	for _, a := range entry.Attempts {
		for _, p := range a.Processes {
			ids[p] = true
		}
	}
	s.state.mu.Unlock()
	plan := RecoveryPlan{Root: request.Root, Namespace: request.Namespace, Processes: []RecoveryProcess{}, RequiresManualReview: true, Warnings: []string{"An unclean engine may have missed descendants. Only recorded, identity-verified processes can be stopped. Review unknown processes before acknowledging recovery.", "Stop sends TERM, then KILL after a 5-second grace period, only to identities shown in this plan."}}
	reader := process.NewReader()
	for id := range ids {
		p, e := reader.Read(r.Context(), id.PID)
		state := "unknown"
		if e == nil {
			if p.Identity != id {
				state = "identity_changed"
			} else if process.Live(p) {
				state = "alive"
			} else {
				state = "gone"
			}
		} else if errors.Is(e, os.ErrNotExist) {
			state = "gone"
		}
		plan.Processes = append(plan.Processes, RecoveryProcess{id, state})
	}
	sort.Slice(plan.Processes, func(i, j int) bool { return plan.Processes[i].Identity.PID < plan.Processes[j].Identity.PID })
	view, _ := json.Marshal(plan.Processes)
	sum := sha256.Sum256(append(raw, view...))
	plan.PlanID = fmt.Sprintf("%x", sum)
	return plan, nil
}
func (s *Server) recovery(w http.ResponseWriter, r *http.Request) (any, error) {
	if r.Method == "GET" {
		return s.recoveryPlan(r, r.URL.Query().Get("root"), r.URL.Query().Get("namespace"))
	}
	if r.Method != "POST" {
		return nil, &control.APIError{Code: "invalid_request", Message: "GET or POST required"}
	}
	var request recoveryRequest
	if err := decodeRequest(w, r, &request); err != nil {
		return nil, err
	}
	plan, err := s.recoveryPlan(r, request.Root, request.Namespace)
	if err != nil {
		return nil, err
	}
	if plan.PlanID != request.PlanID {
		return nil, &control.APIError{Code: "plan_conflict", Message: "Recovery process identities changed; review a fresh plan"}
	}
	switch request.Action {
	case "stop":
		for _, p := range plan.Processes {
			if p.State == "unknown" || p.State == "identity_changed" {
				return nil, &control.APIError{Code: "identity_conflict", Message: "Unverifiable or reused PID preserved; manual review required"}
			}
		}
		for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
			for _, p := range plan.Processes {
				if p.State != "alive" {
					continue
				}
				birth, e := process.CreatedMillis(r.Context(), p.Identity.PID)
				if e != nil {
					continue
				}
				if birth != p.Identity.CreatedMillis {
					return nil, &control.APIError{Code: "identity_conflict", Message: "Process identity changed; no signal sent"}
				}
				if e = syscall.Kill(int(p.Identity.PID), sig); e != nil && e != syscall.ESRCH {
					return nil, e
				}
			}
			if sig == syscall.SIGTERM {
				timer := time.NewTimer(5 * time.Second)
				select {
				case <-r.Context().Done():
					timer.Stop()
					return nil, r.Context().Err()
				case <-timer.C:
				}
			}
		}
		return s.recoveryPlan(r, request.Root, request.Namespace)
	case "acknowledge":
		if !request.ReviewedUnknown {
			return nil, &control.APIError{Code: "invalid_request", Message: "Explicit review of unknown descendants is required"}
		}
		for _, p := range plan.Processes {
			if p.State == "alive" || p.State == "unknown" {
				return nil, &control.APIError{Code: "operation_conflict", Message: "Recorded processes remain alive or unknown; recovery is not complete"}
			}
		}
		key := workspaceKey(plan.Namespace, plan.Root)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.hosts[key] != nil {
			return nil, &control.APIError{Code: "operation_conflict", Message: "Close the current workspace session before acknowledging recovery"}
		}
		s.state.mu.Lock()
		defer s.state.mu.Unlock()
		entry := s.state.entries[key]
		entry.Attempts = map[string]attempt{}
		entry.Mutation = false
		entry.MutationAction = ""
		entry.MutationTargets = nil
		entry.RecoveryRequired = false
		entry.Record.State = "offline"
		entry.Record.Issues = nil
		if err = s.state.saveLocked(); err != nil {
			return nil, err
		}
		return map[string]bool{"recovered": true}, nil
	default:
		return nil, &control.APIError{Code: "invalid_request", Message: "Recovery action must be stop or acknowledge"}
	}
}
