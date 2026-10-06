package sessionhost

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/tui"
)

// Controller retains the existing local read model while all writes use the
// same coordinator as the Session API. Confirmations retain their server plan.
type Controller struct{ host *Host }

func (c *Controller) Snapshot() model.Snapshot { return c.host.session.Snapshot() }
func (c *Controller) Logs() *logs.Store        { return c.host.session.Logs() }
func (c *Controller) Affected(ids []model.ServiceID) []model.ServiceID {
	return c.host.session.Affected(ids)
}
func (c *Controller) Done() <-chan struct{}              { return c.host.Done() }
func (c *Controller) Shutdown(ctx context.Context) error { return c.host.Close(ctx) }
func (c *Controller) Start(ctx context.Context, ids []model.ServiceID) error {
	return c.action(ctx, "start", ids, nil)
}
func (c *Controller) Stop(ctx context.Context, ids []model.ServiceID) error {
	return c.action(ctx, "stop", ids, nil)
}
func (c *Controller) Restart(ctx context.Context, ids []model.ServiceID) error {
	return c.action(ctx, "restart", ids, nil)
}
func (c *Controller) DockerAction(ctx context.Context, action string, names []string) error {
	return c.action(ctx, "docker-"+action, nil, names)
}
func (c *Controller) AllAction(ctx context.Context, action string) error {
	return c.action(ctx, "all-"+action, nil, nil)
}
func (c *Controller) action(ctx context.Context, action string, ids []model.ServiceID, docker []string) error {
	p, err := c.PlanAction(ctx, action, ids, docker)
	if err != nil {
		return err
	}
	if len(p.Conflicts) > 0 {
		return fmt.Errorf("Port release requires explicit confirmation")
	}
	return c.ExecuteAction(ctx, p)
}
func (c *Controller) PlanAction(ctx context.Context, action string, ids []model.ServiceID, docker []string) (tui.PlannedAction, error) {
	req := control.PlanRequest{Action: action, Targets: []string{}}
	for _, id := range ids {
		req.Targets = append(req.Targets, string(id))
	}
	if strings.HasPrefix(action, "docker-") {
		req.Targets = append([]string{}, docker...)
	}
	if strings.HasPrefix(action, "all-") {
		req.Targets = []string{}
	}
	if action == "start" || action == "restart" || action == "all-start" || action == "all-restart" {
		// Composite plans capture the exact external listeners as well as all launch
		// effects. An empty conflict set needs no extra confirmation.
		req.Action = "release-" + action
	}
	p, err := c.host.coordinator.Plan(ctx, req)
	if err != nil {
		return tui.PlannedAction{}, err
	}
	out := tui.PlannedAction{ID: p.ID, Key: control.NewIdempotencyKey(p.ID), Warnings: p.Warnings}
	for _, id := range p.Affected {
		out.Affected = append(out.Affected, model.ServiceID(id))
	}
	if strings.HasPrefix(req.Action, "release-") {
		out.Conflicts, err = c.host.backend.PlannedConflicts(ctx, req, p.Fingerprint)
	}
	return out, err
}
func (c *Controller) ExecuteAction(ctx context.Context, p tui.PlannedAction) error {
	op, err := c.host.coordinator.Submit(ctx, control.SubmitRequest{PlanID: p.ID, IdempotencyKey: p.Key})
	if err != nil {
		return err
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if terminal(op.State) {
			if op.Error != nil {
				return op.Error
			}
			if op.State != "succeeded" {
				return fmt.Errorf("Operation %s", op.State)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			_ = c.host.coordinator.Cancel(op.ID)
			return ctx.Err()
		case <-ticker.C:
			op, err = c.host.coordinator.Operation(op.ID)
			if err != nil {
				return err
			}
		}
	}
}
