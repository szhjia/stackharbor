package control

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

const maximumQueued = 100
const maximumTerminal = 1000
const retention = 24 * time.Hour

// NewIdempotencyKey binds a client retry key to a single-use server plan.
// Reuse the exact returned key for retries; after plan expiry query Operation.
func NewIdempotencyKey(planID string) string { return planID + ":" + randomID() }
func (c *Coordinator) submissionLocked(req SubmitRequest) (storedPlan, *Operation, error) {
	c.pruneLocked()
	p, ok := c.plans[req.PlanID]
	if !ok || !c.opts.Now().Before(p.public.ExpiresAt) {
		return p, nil, apiError("plan_expired", "Plan is missing or expired")
	}
	if p.acceptedKey != "" {
		if p.acceptedKey != req.IdempotencyKey {
			if c.closing {
				return p, nil, apiError("session_closed", "Session is closing")
			}
			return p, nil, apiError("plan_conflict", "Plan was already consumed; request a new plan")
		}
		r := c.records[p.operationID]
		if r == nil {
			return p, nil, apiError("operation_expired", "Accepted operation record expired")
		}
		o := copyOperation(r.public)
		return p, &o, nil
	}
	if c.closing {
		return p, nil, apiError("session_closed", "Session is closing")
	}
	return p, nil, nil
}
func (c *Coordinator) Submit(ctx context.Context, req SubmitRequest) (Operation, error) {
	if req.PlanID == "" || req.IdempotencyKey == "" || len(req.IdempotencyKey) > 256 {
		return Operation{}, apiError("invalid_request", "Plan and bound idempotency key are required (at most 256 bytes)")
	}
	bound, nonce, ok := strings.Cut(req.IdempotencyKey, ":")
	if !ok || nonce == "" {
		return Operation{}, apiError("invalid_request", "Idempotency key must use plan_id:nonce format")
	}
	if bound != req.PlanID {
		return Operation{}, apiError("idempotency_conflict", "Idempotency key belongs to a different plan")
	}
	if err := ctx.Err(); err != nil {
		return Operation{}, err
	}
	c.mu.Lock()
	p, previous, err := c.submissionLocked(req)
	c.mu.Unlock()
	if err != nil {
		return Operation{}, err
	}
	if previous != nil {
		return *previous, nil
	}
	if err = c.validate(ctx, p); err != nil {
		return Operation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, previous, err = c.submissionLocked(req)
	if err != nil {
		return Operation{}, err
	}
	if previous != nil {
		return *previous, nil
	}
	if p.request.Action != "close" && len(c.queue) >= maximumQueued {
		return Operation{}, apiError("queue_full", "Operation queue is full")
	}
	o := c.newRecordLocked(p, req.IdempotencyKey)
	if p.request.Action == "close" {
		c.beginCloseLocked(o.ID)
	} else {
		c.queue = append(c.queue, o.ID)
	}
	c.signal()
	return o, nil
}
func (c *Coordinator) newRecordLocked(p storedPlan, key string) Operation {
	now := c.opts.Now().UTC()
	o := Operation{ID: c.opts.NewID(), SessionID: c.identity.SessionID, Action: p.request.Action, State: "queued", Results: []TargetResult{}, CreatedAt: now, UpdatedAt: now}
	c.records[o.ID] = &operationRecord{public: o, plan: p, key: key}
	if key != "" {
		p.acceptedKey = key
		p.operationID = o.ID
		c.plans[p.public.ID] = p
	}
	return copyOperation(o)
}
func (c *Coordinator) beginCloseLocked(id string) {
	c.closing = true
	c.closeID = id
	c.closeContext, c.closeContextCancel = context.WithTimeout(context.Background(), c.closeBudget)
	c.cancel()
	for _, r := range c.records {
		if r.public.ID == id {
			continue
		}
		if r.public.State == "queued" {
			c.finishLocked(r, nil, context.Canceled)
		} else if r.cancel != nil {
			r.cancel()
		}
	}
	c.queue = nil
}
func (c *Coordinator) Operation(id string) (Operation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	if r := c.records[id]; r != nil {
		return copyOperation(r.public), nil
	}
	return Operation{}, apiError("not_found", "Operation not found")
}
func (c *Coordinator) Operations() []Operation {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	out := make([]Operation, 0, len(c.records))
	for _, r := range c.records {
		out = append(out, copyOperation(r.public))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}
func (c *Coordinator) Cancel(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	r := c.records[id]
	if r == nil {
		return apiError("not_found", "Operation not found")
	}
	if r.public.Action == "close" {
		return apiError("operation_conflict", "Close cannot be canceled")
	}
	if r.public.State == "queued" {
		for i, v := range c.queue {
			if v == id {
				c.queue = append(c.queue[:i], c.queue[i+1:]...)
				break
			}
		}
		c.finishLocked(r, nil, context.Canceled)
	} else if r.cancel != nil {
		r.cancel()
	}
	return nil
}
func (c *Coordinator) Close(ctx context.Context) error {
	c.mu.Lock()
	if !c.closing {
		p := storedPlan{request: PlanRequest{Action: "close"}}
		o := c.newRecordLocked(p, "")
		c.beginCloseLocked(o.ID)
		c.signal()
	}
	closeContext := c.closeContext
	c.mu.Unlock()
	select {
	case <-c.closeDone:
	default:
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.closeDone:
		case <-closeContext.Done():
			select {
			case <-c.closeDone:
			default:
				return closeContext.Err()
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeError != nil {
		value := *c.closeError
		return &value
	}
	return nil
}
func (c *Coordinator) work() {
	for {
		<-c.wake
		for {
			c.mu.Lock()
			id := ""
			closing := c.closing
			if closing {
				id = c.closeID
			} else if len(c.queue) > 0 {
				id = c.queue[0]
				c.queue = c.queue[1:]
			}
			if id == "" {
				c.mu.Unlock()
				break
			}
			r := c.records[id]
			if r.public.State != "queued" {
				c.mu.Unlock()
				if closing {
					return
				}
				continue
			}
			ctx := c.ctx
			var cancel context.CancelFunc
			if closing {
				ctx, cancel = context.WithCancel(c.closeContext)
			} else {
				ctx, cancel = context.WithCancel(ctx)
			}
			r.cancel = cancel
			r.public.State = "running"
			r.public.UpdatedAt = c.opts.Now().UTC()
			p := r.plan
			c.mu.Unlock()
			var results []TargetResult
			var err error
			if !closing {
				err = c.validate(ctx, p)
			}
			if err == nil {
				results, err = c.backend.Execute(ctx, p.request, p.state)
			}
			cancel()
			if closing && c.opts.FinalizeClose != nil {
				c.mu.Lock()
				proposed := outcome(r.public, results, err, c.opts.Now().UTC())
				c.mu.Unlock()
				if finalizeErr := c.opts.FinalizeClose(c.closeContext, copyOperation(proposed)); finalizeErr != nil {
					err = apiError("close_finalization_failed", finalizeErr.Error())
					for i := range results {
						results[i].State = "failed"
						results[i].Error = apiError("close_finalization_failed", finalizeErr.Error())
					}
				}
			}
			c.mu.Lock()
			c.finishLocked(r, results, err)
			o := copyOperation(r.public)
			if closing && o.Error != nil {
				value := *o.Error
				c.closeError = &value
			}
			c.mu.Unlock()
			if closing {
				close(c.closeDone)
				c.closeContextCancel()
				if c.opts.OnClosed != nil {
					c.opts.OnClosed(o)
				}
				return
			}
		}
	}
}
func (c *Coordinator) finishLocked(r *operationRecord, results []TargetResult, err error) {
	r.cancel = nil
	if len(results) == 0 && err != nil {
		targets := r.plan.public.Affected
		if len(targets) == 0 {
			targets = r.plan.request.Targets
		}
		for _, target := range targets {
			state := "failed"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				state = "canceled"
			}
			var detail *APIError
			if !errors.As(err, &detail) {
				detail = apiError("execution_failed", err.Error())
			}
			value := *detail
			results = append(results, TargetResult{Target: target, State: state, Error: &value})
		}
	}
	r.public = outcome(r.public, results, err, c.opts.Now().UTC())
	c.terminals = append(c.terminals, r.public.ID)
	c.pruneLocked()
}
func (c *Coordinator) pruneLocked() {
	now := c.opts.Now()
	for id, p := range c.plans {
		if !now.Before(p.public.ExpiresAt) {
			delete(c.plans, id)
		}
	}
	for len(c.terminals) > 0 {
		id := c.terminals[0]
		r := c.records[id]
		if r != nil && len(c.terminals) <= maximumTerminal && now.Sub(r.public.UpdatedAt) < retention {
			break
		}
		c.terminals = c.terminals[1:]
		if r == nil {
			continue
		}
		delete(c.records, id)

	}
}

func outcome(o Operation, results []TargetResult, err error, now time.Time) Operation {
	o.Results = append([]TargetResult{}, results...)
	o.UpdatedAt = now
	o.State = "succeeded"
	good, bad := 0, 0
	for _, v := range results {
		if v.State == "succeeded" {
			good++
		} else {
			bad++
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		o.State = "canceled"
		if good > 0 {
			o.State = "partial"
		}
	} else if err != nil || bad > 0 {
		o.State = "failed"
		if good > 0 {
			o.State = "partial"
		}
	}
	if err != nil {
		var a *APIError
		if errors.As(err, &a) {
			v := *a
			o.Error = &v
		} else {
			o.Error = apiError("execution_failed", err.Error())
		}
	}
	return o
}
