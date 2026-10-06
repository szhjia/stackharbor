package web

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/inventory"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (g *Gateway) find(ctx context.Context, id string) (supervisor.SessionInfo, error) {
	infos, err := (registrySource{g.options.Namespace}).ListSessions(ctx)
	if err != nil {
		return supervisor.SessionInfo{}, err
	}
	for _, info := range infos {
		if info.SessionID == id {
			return info, nil
		}
	}
	return supervisor.SessionInfo{}, apiError("not_found", "session not found or ended")
}
func (g *Gateway) impact(ctx context.Context, p control.Plan) (inventory.Impact, error) {
	inv, err := g.collect(ctx)
	if err != nil {
		return inventory.Impact{}, err
	}
	impact := inventory.SharedImpact(inv, p.SessionID, p.Action, p.Affected)
	if len(impact.Blockers) > 0 {
		var blockers []string
		for _, b := range impact.Blockers {
			blockers = append(blockers, fmt.Sprintf("%s (%s), stop nodes %s", b.Root, b.WorkspaceID, strings.Join(b.NodeIDs, ", ")))
		}
		return impact, apiError("shared_resource_conflict", "shared resource has active consumers; stop them then replan: "+strings.Join(blockers, "; "))
	}
	return impact, nil
}
func (g *Gateway) retain(p control.Plan, info supervisor.SessionInfo) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for key, v := range g.plans {
		if !time.Now().Before(v.Plan.ExpiresAt) {
			delete(g.plans, key)
		}
	}
	if len(g.plans) >= 4096 {
		return apiError("plan_capacity", "gateway plan capacity reached")
	}
	g.plans[p.SessionID+"/"+p.ID] = retainedPlan{Plan: p, Info: info}
	return nil
}
func (g *Gateway) retained(session, plan string) (retainedPlan, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.plans[session+"/"+plan]
	return p, ok
}
func (g *Gateway) completion(session, id string) (control.Operation, error) {
	g.mu.Lock()
	g.pruneCloses(time.Now())
	record, ok := g.closes[session+"/"+id]
	g.mu.Unlock()
	if !ok {
		return control.Operation{}, apiError("unavailable", "operation outcome is unknown; session is unavailable")
	}
	op, err := sessionhost.ReadOperationCompletion(record.Info.CacheDir, record.Info.Root, id)
	if err != nil {
		return control.Operation{}, err
	}
	if op.SessionID != session {
		return control.Operation{}, apiError("identity_conflict", "completion session mismatch")
	}
	return op, nil
}
func (g *Gateway) forward(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/")
	parts := strings.Split(rest, "/")
	if rest == r.URL.Path || len(parts) < 2 || parts[0] == "" {
		fail(w, apiError("not_found", "route not found"))
		return
	}
	sid := parts[0]
	route := strings.Join(parts[1:], "/")
	// Validate the whitelist before touching private discovery/transport metadata.
	allowed := false
	switch route {
	case "identity", "snapshot", "logs":
		allowed = method(w, r, "GET")
	case "plans":
		allowed = method(w, r, "POST")
	case "operations":
		allowed = method(w, r, "GET", "POST")
	default:
		if len(parts) == 3 && parts[1] == "operations" && parts[2] != "" {
			allowed = method(w, r, "GET")
		} else if len(parts) == 4 && parts[1] == "operations" && parts[2] != "" && parts[3] == "cancel" {
			allowed = method(w, r, "POST")
		} else {
			fail(w, apiError("not_found", "route not found"))
			return
		}
	}
	if !allowed {
		return
	}
	var planReq control.PlanRequest
	var submit control.SubmitRequest
	if route == "plans" {
		if err := decodeBrowserJSON(w, r, &planReq); err != nil {
			fail(w, err)
			return
		}
	}
	if route == "operations" && r.Method == "POST" {
		if err := decodeBrowserJSON(w, r, &submit); err != nil {
			fail(w, err)
			return
		}
		if !strings.HasPrefix(submit.IdempotencyKey, submit.PlanID+":") || submit.PlanID == "" {
			fail(w, apiError("invalid_request", "idempotency key must be bound to plan ID"))
			return
		}
		retained, ok := g.retained(sid, submit.PlanID)
		if !ok || !time.Now().Before(retained.Plan.ExpiresAt) {
			fail(w, apiError("plan_expired", "retained plan expired or missing; replan explicitly"))
			return
		}
		if retained.Operation != nil {
			if retained.AcceptedKey != submit.IdempotencyKey {
				fail(w, apiError("plan_conflict", "plan has already authorized an operation"))
				return
			}
			writeJSON(w, 202, *retained.Operation, nil)
			return
		}
		if _, err := g.impact(r.Context(), retained.Plan); err != nil {
			fail(w, err)
			return
		}
	}
	if strings.HasSuffix(route, "/cancel") {
		var req struct{}
		if err := decodeBrowserJSON(w, r, &req); err != nil {
			fail(w, err)
			return
		}
	}
	info, err := g.find(r.Context(), sid)
	var c *sessionapi.Client
	if err == nil {
		c, err = sessionapi.Connect(r.Context(), info)
	}
	if err != nil {
		if len(parts) == 3 && parts[1] == "operations" {
			op, completeErr := g.completion(sid, parts[2])
			if completeErr == nil {
				writeJSON(w, 200, op, nil)
				return
			}
			fail(w, completeErr)
			return
		}
		fail(w, err)
		return
	}
	defer c.Close()
	var value any
	status := 200
	switch route {
	case "identity":
		value = c.Identity()
	case "snapshot":
		value, err = c.Snapshot(r.Context())
	case "logs":
		var target string
		var cursor string
		var limit int
		target, cursor, limit, err = browserLogsQuery(r.URL, sid)
		if err == nil {
			value, err = c.LogsAfter(r.Context(), target, cursor, limit)
		}
	case "plans":
		var p control.Plan
		p, err = c.Plan(r.Context(), planReq)
		if err == nil {
			var impact inventory.Impact
			impact, err = g.impact(r.Context(), p)
			if err == nil {
				if impact.Partial {
					p.Warnings = append(p.Warnings, "Resource dependency coverage is partial; some sessions are unreachable or identities unknown.")
				}
				for _, resource := range impact.Resources {
					for _, ref := range resource.References {
						if ref.SessionID != p.SessionID {
							p.Warnings = append(p.Warnings, fmt.Sprintf("Shared resource %s: workspace %s, session %s, nodes %v", resource.ID, ref.WorkspaceID, ref.SessionID, ref.Nodes))
						}
					}
				}
				err = g.retain(p, info)
				value = p
			}
		}
	case "operations":
		if r.Method == "GET" {
			value, err = c.Operations(r.Context())
		} else {
			var op control.Operation
			op, err = c.Submit(r.Context(), submit)
			value = op
			status = 202
			if err == nil {
				g.mu.Lock()
				p := g.plans[sid+"/"+submit.PlanID]
				p.AcceptedKey = submit.IdempotencyKey
				p.Operation = &op
				g.plans[sid+"/"+submit.PlanID] = p
				g.mu.Unlock()
			}
			if err == nil && op.Action == "close" {
				g.mu.Lock()
				g.pruneCloses(time.Now())
				g.closes[sid+"/"+op.ID] = retainedClose{Info: info, At: op.UpdatedAt, OperationID: op.ID}
				g.mu.Unlock()
			}
		}
	default:
		if strings.HasSuffix(route, "/cancel") {
			err = c.Cancel(r.Context(), parts[2])
			value = struct{}{}
		} else {
			value, err = c.Operation(r.Context(), parts[2])
			if err != nil {
				if op, journalErr := g.completion(sid, parts[2]); journalErr == nil {
					value = op
					err = nil
				}
			}
		}
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, status, value, nil)
}
func logsQuery(u *url.URL) (string, uint64, int, error) {
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", 0, 0, apiError("invalid_request", "invalid logs query")
	}
	for k, v := range q {
		if len(v) != 1 || (k != "target" && k != "after" && k != "limit") {
			return "", 0, 0, apiError("invalid_request", "invalid logs query")
		}
	}
	after := uint64(0)
	limit := 500
	if v, ok := q["after"]; ok {
		after, err = strconv.ParseUint(v[0], 10, 64)
		if err != nil {
			return "", 0, 0, apiError("invalid_request", "after must be an unsigned sequence")
		}
	}
	if v, ok := q["limit"]; ok {
		limit, err = strconv.Atoi(v[0])
		if err != nil || limit < 1 || limit > 500 {
			return "", 0, 0, apiError("invalid_request", "limit must be between 1 and 500")
		}
	}
	return q.Get("target"), after, limit, nil
}
