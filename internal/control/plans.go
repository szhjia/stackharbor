package control

import (
	"context"
	"sort"
	"strings"
	"time"
)

const planTTL = 60 * time.Second
const maximumPlans = 1000

func normalizeRequest(r PlanRequest) (PlanRequest, error) {
	switch r.Action {
	case "start", "stop", "restart", "release", "close", "all-start", "all-stop", "all-restart", "docker-start", "docker-stop", "docker-restart", "release-start", "release-restart", "release-all-start", "release-all-restart":
	default:
		return r, apiError("invalid_request", "Unsupported action")
	}
	if r.Port < 0 || r.Port > 65535 {
		return r, apiError("invalid_request", "Port must be between 0 and 65535")
	}
	if r.Port != 0 && r.Action != "release" {
		return r, apiError("invalid_request", "Port is only supported for release")
	}
	r.Targets = append([]string{}, r.Targets...)
	sort.Strings(r.Targets)
	for i, id := range r.Targets {
		if strings.TrimSpace(id) == "" || len(id) > 512 {
			return r, apiError("invalid_request", "Invalid target")
		}
		if i > 0 && r.Targets[i-1] == id {
			return r, apiError("invalid_request", "Duplicate target")
		}
	}
	if len(r.Targets) > 1000 {
		return r, apiError("invalid_request", "Too many targets")
	}
	if r.Action == "close" || strings.HasPrefix(strings.TrimPrefix(r.Action, "release-"), "all-") {
		if len(r.Targets) != 0 {
			return r, apiError("invalid_request", "Global actions do not accept targets")
		}

	}
	if len(r.Targets) == 0 && r.Action != "close" && !strings.HasPrefix(strings.TrimPrefix(r.Action, "release-"), "all-") {
		return r, apiError("invalid_request", "Targets are required")
	}
	return r, nil
}
func (c *Coordinator) Plan(ctx context.Context, req PlanRequest) (Plan, error) {
	req, err := normalizeRequest(req)
	if err != nil {
		return Plan{}, err
	}
	if err = ctx.Err(); err != nil {
		return Plan{}, err
	}
	c.mu.Lock()
	closed := c.closing
	c.mu.Unlock()
	if closed {
		return Plan{}, apiError("session_closed", "Session is closing")
	}
	state, err := c.backend.PlanState(ctx, req)
	if err != nil {
		return Plan{}, err
	}
	state.Affected = append([]string{}, state.Affected...)
	state.Warnings = append([]string{}, state.Warnings...)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return Plan{}, apiError("session_closed", "Session is closing")
	}
	now := c.opts.Now().UTC()
	for id, p := range c.plans {
		if !now.Before(p.public.ExpiresAt) {
			delete(c.plans, id)
		}
	}
	if len(c.plans) >= maximumPlans {
		return Plan{}, apiError("plan_capacity", "Too many unexpired plans")
	}
	p := Plan{ID: c.opts.NewID(), SessionID: c.identity.SessionID, Action: req.Action, Targets: append([]string{}, req.Targets...), Affected: append([]string{}, state.Affected...), ExpiresAt: now.Add(planTTL), Fingerprint: state.Fingerprint, Warnings: append([]string{}, state.Warnings...)}
	c.plans[p.ID] = storedPlan{public: p, request: req, state: state}
	return copyPlan(p), nil
}
func (c *Coordinator) validate(ctx context.Context, p storedPlan) error {
	if !c.opts.Now().Before(p.public.ExpiresAt) {
		return apiError("plan_expired", "Plan expired; request a new plan")
	}
	current, err := c.backend.PlanState(ctx, p.request)
	if err != nil {
		return err
	}
	if current.Fingerprint != p.state.Fingerprint {
		return apiError("plan_conflict", "Relevant state changed; request a new plan")
	}
	return nil
}
