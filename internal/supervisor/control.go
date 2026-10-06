package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/process"
)

// ControlBackend adapts a Session to the control protocol. Writes are serialized
// by the Coordinator, not recursively by Session methods.
type ControlBackend struct {
	session  *Session
	identity control.Identity
	revision atomic.Uint64
}

var _ control.Backend = (*ControlBackend)(nil)

func NewControlBackend(session *Session, identity control.Identity) *ControlBackend {
	identity.Capabilities = append([]string{}, identity.Capabilities...)
	return &ControlBackend{session: session, identity: identity}
}
func (b *ControlBackend) Snapshot(ctx context.Context) (control.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return control.Snapshot{}, err
	}
	out := control.ProjectSnapshot(b.identity, b.session.Snapshot())
	out.Revision = b.revision.Add(1)
	return out, nil
}
func (b *ControlBackend) Logs(ctx context.Context, target string, after uint64, limit int) (control.LogPage, error) {
	if err := ctx.Err(); err != nil {
		return control.LogPage{}, err
	}
	if target != "" {
		b.session.mu.Lock()
		_, ok := b.session.entries[model.ServiceID(target)]
		b.session.mu.Unlock()
		if !ok {
			return control.LogPage{}, controlError("not_found", "Unknown log target")
		}
	}
	return control.ReadLogs(b.session.Logs(), model.ServiceID(target), after, limit), nil
}

// Finalize releases the session lock after the host has written its completion
// record and shut down its transports. Execute(close) deliberately retains it.
func (b *ControlBackend) Finalize() error { return b.session.ReleaseLock() }
func controlError(code, message string) *control.APIError {
	return &control.APIError{Code: code, Message: message}
}

type controlExecution struct {
	ResourceKeys      []string
	IDs               []model.ServiceID
	Conflicts         []model.PortConflict
	Ports             []model.Port
	LaunchFingerprint string
}
type controlRevision struct {
	ID              model.ServiceID
	Generation      uint64
	Owned, Observed []model.ProcessIdentity
	Container       string
}

func (b *ControlBackend) PlanState(ctx context.Context, req control.PlanRequest) (control.PlanState, error) {
	if err := ctx.Err(); err != nil {
		return control.PlanState{}, err
	}
	s := b.session
	action := req.Action
	composite := strings.HasPrefix(action, "release-")
	if composite {
		action = strings.TrimPrefix(action, "release-")
		if action != "start" && action != "restart" && action != "all-start" && action != "all-restart" {
			return control.PlanState{}, controlError("invalid_request", "Unsupported composite action")
		}
	}
	global := strings.HasPrefix(action, "all-")
	dockerAction := strings.HasPrefix(action, "docker-")
	if global {
		action = strings.TrimPrefix(action, "all-")
	}
	if dockerAction {
		action = strings.TrimPrefix(action, "docker-")
	}
	if action != "start" && action != "stop" && action != "restart" && action != "release" && action != "close" {
		return control.PlanState{}, controlError("invalid_request", "Unsupported action")
	}
	if req.Port < 0 || req.Port > 65535 || req.Port != 0 && action != "release" {
		return control.PlanState{}, controlError("invalid_request", "Invalid port")
	}
	if (action == "close" || global) && len(req.Targets) > 0 {
		return control.PlanState{}, controlError("invalid_request", "Global actions do not accept targets")
	}
	if action != "close" && action != "stop" {
		if err := s.checkInputs(); err != nil {
			return control.PlanState{}, controlError("plan_conflict", "Configuration input changed; reopen the session")
		}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return control.PlanState{}, controlError("session_closed", "Session closed")
	}
	if action == "close" {
		out := control.PlanState{Fingerprint: "close", Affected: []string{}, Warnings: []string{"Close applies to the whole session, including owned nodes active when cleanup begins"}}
		for _, id := range s.cleanupTargetsLocked() {
			out.Affected = append(out.Affected, string(id))
		}
		for id, e := range s.entries {
			if e.spec.Control == "observe" || e.spec.Resource != nil && e.spec.Resource.Control == "observe" {
				out.Warnings = append(out.Warnings, fmt.Sprintf("Observe-only node %s is preserved", id))
			} else if e.spec.Resource != nil && e.spec.Resource.Lifetime == "persistent" {
				out.Warnings = append(out.Warnings, fmt.Sprintf("Persistent resource %s is preserved", id))
			}
			if len(e.observed) > 0 {
				out.Warnings = append(out.Warnings, fmt.Sprintf("External observed processes for %s are preserved", id))
			}
		}
		sort.Strings(out.Affected)
		sort.Strings(out.Warnings[1:])
		s.mu.Unlock()
		return out, nil
	}
	ids := []model.ServiceID{}
	if global {
		for _, spec := range s.workspace.Services() {
			if spec.Kind == "task" || spec.Kind == "resource" || spec.Control == "observe" {
				continue
			}
			e := s.entries[spec.ID]
			if action == "restart" && s.workspace.Version == 2 && e.handle == nil && e.state != "waiting" && e.state != "starting" {
				continue
			}
			ids = append(ids, spec.ID)
		}
	} else {
		for _, target := range req.Targets {
			ids = append(ids, model.ServiceID(target))
		}
	}
	if !global && len(ids) == 0 {
		s.mu.Unlock()
		return control.PlanState{}, controlError("invalid_request", "Declared targets are required")
	}
	// Legacy Docker names belong to the manager rather than graph entries.
	legacyDocker := dockerAction && len(s.resources) == 0
	if !legacyDocker {
		for _, id := range ids {
			e := s.entries[id]
			if e == nil {
				s.mu.Unlock()
				return control.PlanState{}, controlError("invalid_request", "Unknown target")
			}
			if dockerAction && e.spec.Kind != "resource" {
				s.mu.Unlock()
				return control.PlanState{}, controlError("invalid_request", "Docker target is not a registered resource")
			}
			if e.spec.Control == "observe" || e.spec.Resource != nil && e.spec.Resource.Control == "observe" {
				s.mu.Unlock()
				return control.PlanState{}, controlError("forbidden", "Read-only targets cannot be controlled")
			}
			if e.spec.Kind == "task" {
				if action == "restart" || action == "release" || action == "stop" && !activeTask(e.state) {
					s.mu.Unlock()
					return control.PlanState{}, controlError("invalid_request", "Action is not allowed for this task state")
				}
			}
			if (action == "start" || action == "restart") && (e.state == "stopping" || e.state == "failed" && e.handle != nil) {
				s.mu.Unlock()
				return control.PlanState{}, controlError("plan_conflict", "Target has not fully stopped")
			}
		}
	}
	relevant := append([]model.ServiceID{}, ids...)
	affected := append([]model.ServiceID{}, ids...)
	if legacyDocker {
		var scopeErr error
		affected, relevant, scopeErr = s.legacyDockerScopeLocked(req.Targets, action)
		if scopeErr != nil {
			s.mu.Unlock()
			return control.PlanState{}, controlError("invalid_request", "Invalid Docker dependency targets")
		}
	} else if action == "start" {
		relevant = s.graph.StartOrder(ids)
		affected = append([]model.ServiceID{}, relevant...)
	} else if action != "release" {
		relevant = append(relevant, s.graph.Dependents(ids)...)
		affected = append(affected, s.affectedLocked(ids)...)
		if action == "restart" {
			relevant = append(relevant, s.graph.StartOrder(affected)...)
			affected = s.graph.StartOrder(affected)
		}
	}
	relevant = uniqueServiceIDs(relevant)
	affected = uniqueServiceIDs(affected)
	revisions := []controlRevision{}
	for _, id := range relevant {
		e := s.entries[id]
		if e == nil {
			continue
		}
		r := controlRevision{ID: id, Generation: e.gen, Observed: append([]model.ProcessIdentity{}, e.observed...), Container: e.resourceIdentity}
		if e.handle != nil {
			r.Owned = append([]model.ProcessIdentity{}, e.handle.Identities()...)
		}
		sortIdentities(r.Owned)
		sortIdentities(r.Observed)
		revisions = append(revisions, r)
	}
	rows := []string{}
	for _, row := range s.dockerRows {
		for _, id := range relevant {
			if row.Service == string(id) {
				rows = append(rows, row.Service+"/"+row.ID)
			}
		}
		if legacyDocker {
			rows = append(rows, row.Service+"/"+row.ID)
		}
	}
	sort.Strings(rows)
	config, _ := json.Marshal(s.workspace)
	inputs := map[string]string{}
	for path, digest := range s.inputDigests {
		inputs[path] = digest
	}
	releasePorts := []model.Port{}
	owned := []model.ProcessIdentity{}
	if action == "release" || composite {
		portIDs := ids
		if composite {
			portIDs = s.graph.StartOrder(affected)
		}
		for _, id := range portIDs {
			e := s.entries[id]
			if composite && (e.spec.Kind == "resource" || e.spec.Control == "observe" || len(e.spec.Ports) == 0) {
				continue
			}
			if e.spec.Kind == "resource" || len(e.spec.Ports) == 0 {
				s.mu.Unlock()
				return control.PlanState{}, controlError("invalid_request", "Release requires declared service ports")
			}
			matched := req.Port == 0
			for _, p := range e.spec.Ports {
				if req.Port == 0 || p.Number == req.Port {
					releasePorts = append(releasePorts, p)
					matched = true
				}
			}
			if !matched {
				s.mu.Unlock()
				return control.PlanState{}, controlError("invalid_request", "Port is not declared by the target")
			}
		}
		for _, e := range s.entries {
			if e.handle != nil {
				owned = append(owned, e.handle.Identities()...)
			}
		}
	}
	s.mu.Unlock()
	if legacyDocker || global {
		names := req.Targets
		if global {
			names = nil
			for _, spec := range s.workspace.Services() {
				names = append(names, spec.DockerDependsOn...)
			}
		}
		if _, err := s.docker.DependencyOrder(names); err != nil {
			return control.PlanState{}, controlError("invalid_request", "Invalid Docker dependency targets")
		}
	}
	resourceKeys, resourceBindings, resourceErr := s.resourceEvidence(ctx, req, relevant)
	if resourceErr != nil {
		return control.PlanState{}, resourceErr
	}
	conflicts := []model.PortConflict{}
	warnings := []string{}
	if action == "release" || composite {
		var err error
		conflicts, err = observe.FindConflicts(ctx, s.ports, process.NewReader(), releasePorts, owned)
		if err != nil {
			return control.PlanState{}, controlError("plan_conflict", "Port listener cannot be safely verified")
		}
		sort.Slice(conflicts, func(i, j int) bool {
			if conflicts[i].Port != conflicts[j].Port {
				return conflicts[i].Port < conflicts[j].Port
			}
			if conflicts[i].Identity.PID != conflicts[j].Identity.PID {
				return conflicts[i].Identity.PID < conflicts[j].Identity.PID
			}
			return conflicts[i].Identity.CreatedMillis < conflicts[j].Identity.CreatedMillis
		})
		for _, c := range conflicts {
			warnings = append(warnings, fmt.Sprintf("Release external listener on port %d (PID %d)", c.Port, c.Identity.PID))
		}
	}
	for _, conflict := range conflicts {
		resourceKeys = append(resourceKeys, control.ProcessLockKey(conflict.Identity))
	}
	sort.Strings(resourceKeys)
	binding, _ := json.Marshal(struct {
		Config       string
		Inputs       map[string]string
		Request      control.PlanRequest
		Relevant     []controlRevision
		Affected     []model.ServiceID
		Containers   []string
		Resources    []string
		ResourceKeys []string
		Conflicts    []model.PortConflict
	}{fmt.Sprintf("%x", sha256.Sum256(config)), inputs, req, revisions, affected, rows, resourceBindings, resourceKeys, conflicts})
	out := control.PlanState{Fingerprint: fmt.Sprintf("%x", sha256.Sum256(binding)), Affected: []string{}, Warnings: warnings, Data: controlExecution{ResourceKeys: resourceKeys, IDs: append([]model.ServiceID{}, ids...), Conflicts: append([]model.PortConflict{}, conflicts...), Ports: append([]model.Port{}, releasePorts...)}}
	for _, id := range affected {
		out.Affected = append(out.Affected, string(id))
	}
	if legacyDocker {
		out.Affected = append(out.Affected, req.Targets...)
	}
	if composite {
		launchReq := req
		launchReq.Action = strings.TrimPrefix(req.Action, "release-")
		launch, err := b.PlanState(ctx, launchReq)
		if err != nil {
			return control.PlanState{}, err
		}
		execution := out.Data.(controlExecution)
		execution.LaunchFingerprint = launch.Fingerprint
		out.Data = execution
	}
	return out, nil
}
func activeTask(state string) bool {
	switch state {
	case "waiting", "starting", "checking", "running-task", "verifying", "stopping":
		return true
	}
	return false
}
func uniqueServiceIDs(ids []model.ServiceID) []model.ServiceID {
	seen := map[model.ServiceID]bool{}
	out := []model.ServiceID{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
func sortIdentities(ids []model.ProcessIdentity) {
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].PID != ids[j].PID {
			return ids[i].PID < ids[j].PID
		}
		return ids[i].CreatedMillis < ids[j].CreatedMillis
	})
}
func (b *ControlBackend) Execute(ctx context.Context, req control.PlanRequest, state control.PlanState) ([]control.TargetResult, error) {
	if req.Action == "close" {
		if len(req.Targets) > 0 || req.Port != 0 {
			return nil, controlError("invalid_request", "Invalid close scope")
		}
		err := b.session.Cleanup(ctx)
		result := control.TargetResult{Target: "session", State: "succeeded"}
		if err != nil {
			result.State = "failed"
			result.Error = controlError("cleanup_failed", "Session cleanup incomplete")
		}
		return []control.TargetResult{result}, err
	}
	current, err := b.PlanState(ctx, req)
	if err != nil {
		return nil, err
	}
	executionKeys, valid := current.Data.(controlExecution)
	if !valid {
		return nil, controlError("invalid_request", "Missing resource evidence")
	}
	locked, release, lockErr := control.WithResourceLocks(ctx, control.ResourceLockNamespace, executionKeys.ResourceKeys)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	ctx = locked
	current, err = b.PlanState(ctx, req)
	if err != nil {
		return nil, err
	}
	if state.Fingerprint != "" && state.Fingerprint != current.Fingerprint {
		return nil, controlError("plan_conflict", "Relevant state changed; request a new plan")
	}
	execution, ok := current.Data.(controlExecution)
	if !ok {
		return nil, controlError("invalid_request", "Missing execution plan")
	}
	executeAction := req.Action
	if strings.HasPrefix(executeAction, "release-") {
		approved, valid := state.Data.(controlExecution)
		if !valid {
			return nil, controlError("invalid_request", "Missing retained release plan")
		}
		if err = observe.ReleaseConflicts(ctx, b.session.ports, process.NewReader(), approved.Conflicts); err != nil {
			return nil, err
		}
		// A new external listener cannot be silently included in the authorization.
		if _, err = b.planConflictsAfterRelease(ctx, req, approved); err != nil {
			return nil, err
		}
		executeAction = strings.TrimPrefix(executeAction, "release-")
	}
	switch executeAction {
	case "start":
		err = b.session.Start(ctx, execution.IDs)
	case "stop":
		err = b.session.Stop(ctx, execution.IDs)
	case "restart":
		err = b.session.Restart(ctx, execution.IDs)
	case "all-start", "all-stop", "all-restart":
		err = b.session.AllAction(ctx, strings.TrimPrefix(executeAction, "all-"))
	case "docker-start", "docker-stop", "docker-restart":
		err = b.session.DockerAction(ctx, strings.TrimPrefix(executeAction, "docker-"), req.Targets)
	case "release":
		approved, valid := state.Data.(controlExecution)
		if !valid {
			return nil, controlError("invalid_request", "Release requires a retained server plan")
		}
		err = observe.ReleaseConflicts(ctx, b.session.ports, process.NewReader(), approved.Conflicts)
		if err == nil {
			err = b.session.ports.CheckStart(ctx, approved.Ports)
		}
	default:
		return nil, controlError("invalid_request", "Unsupported action")
	}
	targets := current.Affected
	if req.Action == "release" {
		targets = req.Targets
	}
	results := []control.TargetResult{}
	snapshot, _ := b.Snapshot(context.Background())
	nodes := map[string]control.Node{}
	for _, n := range snapshot.Nodes {
		nodes[n.ID] = n
	}
	for _, target := range targets {
		r := control.TargetResult{Target: target, State: "succeeded"}
		if err != nil {
			r.State = "failed"
			if n, ok := nodes[target]; ok && targetSucceeded(req.Action, n.State) {
				r.State = "succeeded"
			} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				r.State = "canceled"
			}
			if r.State != "succeeded" {
				r.Error = controlError("execution_failed", "Target action failed; inspect its state and logs")
			}
		}
		results = append(results, r)
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		err = controlError("execution_failed", "Action failed; inspect target results and logs")
	}
	return results, err
}
func targetSucceeded(action, state string) bool {
	if strings.HasSuffix(action, "stop") {
		return state == "stopped" || state == "succeeded"
	}
	if strings.HasSuffix(action, "start") || strings.HasSuffix(action, "restart") {
		return state == "running" || state == "started" || state == "succeeded" || state == "available"
	}
	return false
}

// Caller holds session.mu. Match legacy DockerAction's active native set and
// its transitive service effects before approving a container action.
func (s *Session) legacyDockerScopeLocked(names []string, action string) (active, potential []model.ServiceID, err error) {
	if _, err = s.docker.DependencyOrder(names); err != nil {
		return nil, nil, err
	}
	if action == "start" {
		return nil, nil, nil
	}
	for _, spec := range s.workspace.Services() {
		order, e := s.docker.DependencyOrder(spec.DockerDependsOn)
		if e != nil {
			return nil, nil, e
		}
		affected := false
		for _, dep := range order {
			for _, name := range names {
				if dep == name {
					affected = true
				}
			}
		}
		if !affected {
			continue
		}
		potential = append(potential, spec.ID)
		entry := s.entries[spec.ID]
		owned := false
		if entry.handle != nil {
			owned = len(entry.handle.Identities()) > 0
		}
		if owned || entry.state != "stopped" && entry.state != "failed" {
			active = append(active, spec.ID)
		}
	}
	potential = append(potential, s.graph.Dependents(potential)...)
	active = append(active, s.affectedLocked(active)...)
	if action == "restart" {
		active = s.graph.StartOrder(active)
		potential = append(potential, s.graph.StartOrder(potential)...)
	}
	return uniqueServiceIDs(active), uniqueServiceIDs(potential), nil
}

// PlannedConflicts is a local presentation bridge. It returns conflicts only
// while the public plan fingerprint still matches their exact evidence.
func (b *ControlBackend) PlannedConflicts(ctx context.Context, req control.PlanRequest, fingerprint string) ([]model.PortConflict, error) {
	state, err := b.PlanState(ctx, req)
	if err != nil {
		return nil, err
	}
	if state.Fingerprint != fingerprint {
		return nil, controlError("plan_conflict", "Plan changed before confirmation")
	}
	execution, ok := state.Data.(controlExecution)
	if !ok {
		return nil, controlError("invalid_request", "Missing execution evidence")
	}
	return append([]model.PortConflict{}, execution.Conflicts...), nil
}
func (b *ControlBackend) planConflictsAfterRelease(ctx context.Context, req control.PlanRequest, approved controlExecution) ([]model.PortConflict, error) {
	launchReq := req
	launchReq.Action = strings.TrimPrefix(req.Action, "release-")
	current, err := b.PlanState(ctx, launchReq)
	if err != nil {
		return nil, err
	}
	if current.Fingerprint != approved.LaunchFingerprint {
		return nil, controlError("plan_conflict", "Launch evidence changed during port release; request a new plan")
	}
	b.session.mu.Lock()
	owned := []model.ProcessIdentity{}
	for _, e := range b.session.entries {
		if e.handle != nil {
			owned = append(owned, e.handle.Identities()...)
		}
	}
	b.session.mu.Unlock()
	conflicts, err := observe.FindConflicts(ctx, b.session.ports, process.NewReader(), approved.Ports, owned)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 {
		return nil, controlError("plan_conflict", "New external listener appeared; request a new plan")
	}
	return conflicts, nil
}
