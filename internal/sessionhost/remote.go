package sessionhost

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/graph"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"github.com/szhjia/stackharbor/internal/tui"
)

type remoteAPI interface {
	Snapshot(context.Context) (control.Snapshot, error)
	Logs(context.Context, string, uint64, int) (control.LogPage, error)
	Plan(context.Context, control.PlanRequest) (control.Plan, error)
	Submit(context.Context, control.SubmitRequest) (control.Operation, error)
	Operation(context.Context, string) (control.Operation, error)
	Close() error
}

// Remote is a detachable terminal client. Workspace ownership and operation
// lifetime belong to the host; Shutdown never submits a close or cancel action.
type Remote struct {
	api       remoteAPI
	ctx       context.Context
	workspace model.Workspace
	mu        sync.RWMutex
	refreshMu sync.Mutex
	snapshot  model.Snapshot
	store     *logs.Store
	cursor    uint64
	cancel    context.CancelFunc
	done      chan struct{}
	once      sync.Once
}

func NewRemote(ctx context.Context, w model.Workspace, info supervisor.SessionInfo) (*Remote, error) {
	client, err := sessionapi.Connect(ctx, info)
	if err != nil {
		return nil, err
	}
	w.Root = info.Root
	return newRemote(ctx, w, client)
}

func newRemote(ctx context.Context, w model.Workspace, client remoteAPI) (*Remote, error) {
	ctx, cancel := context.WithCancel(ctx)
	r := &Remote{api: client, ctx: ctx, workspace: w, store: logs.NewStore(), cancel: cancel, done: make(chan struct{})}
	if err := r.refresh(ctx); err != nil {
		_ = r.Shutdown(context.Background())
		return nil, err
	}
	go func() {
		defer r.Shutdown(context.Background())
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				budget, stop := context.WithTimeout(ctx, 10*time.Second)
				err := r.refresh(budget)
				stop()
				if err != nil {
					r.mu.Lock()
					diagnostics := make([]model.Diagnostic, 0, len(r.snapshot.Diagnostics)+1)
					for _, d := range r.snapshot.Diagnostics {
						if d.Code != "remote_observation" {
							diagnostics = append(diagnostics, d)
						}
					}
					r.snapshot.Diagnostics = append(diagnostics, model.Diagnostic{Severity: "error", Code: "remote_observation", Message: err.Error()})
					r.mu.Unlock()
				}
			}
		}
	}()
	return r, nil
}

func (r *Remote) Snapshot() model.Snapshot { r.mu.RLock(); defer r.mu.RUnlock(); return r.snapshot }
func (r *Remote) Logs() *logs.Store        { r.mu.RLock(); defer r.mu.RUnlock(); return r.store }
func (r *Remote) Done() <-chan struct{}    { return r.done }
func (r *Remote) Detachable() bool         { return true }
func (r *Remote) Shutdown(context.Context) error {
	r.once.Do(func() { r.cancel(); _ = r.api.Close(); close(r.done) })
	return nil
}
func (r *Remote) Affected(ids []model.ServiceID) []model.ServiceID {
	snapshot := r.Snapshot()
	specs := make([]model.Service, 0, len(snapshot.Services))
	for _, node := range snapshot.Services {
		specs = append(specs, node.Spec)
	}
	g, ds := graph.Build(specs)
	if len(ds) != 0 {
		return nil // The retained server plan still determines the authorized scope.
	}
	return g.Dependents(ids)
}
func (r *Remote) Start(ctx context.Context, ids []model.ServiceID) error {
	return r.action(ctx, "start", ids, nil)
}
func (r *Remote) Stop(ctx context.Context, ids []model.ServiceID) error {
	return r.action(ctx, "stop", ids, nil)
}
func (r *Remote) Restart(ctx context.Context, ids []model.ServiceID) error {
	return r.action(ctx, "restart", ids, nil)
}
func (r *Remote) DockerAction(ctx context.Context, action string, names []string) error {
	return r.action(ctx, "docker-"+action, nil, names)
}
func (r *Remote) AllAction(ctx context.Context, action string) error {
	return r.action(ctx, "all-"+action, nil, nil)
}
func (r *Remote) action(ctx context.Context, action string, ids []model.ServiceID, docker []string) error {
	plan, err := r.PlanAction(ctx, action, ids, docker)
	if err != nil {
		return err
	}
	if plan.RequiresConfirmation {
		return fmt.Errorf("Review and confirm this action's retained plan before execution")
	}
	return r.ExecuteAction(ctx, plan)
}
func (r *Remote) PlanAction(ctx context.Context, action string, ids []model.ServiceID, docker []string) (tui.PlannedAction, error) {
	ctx, cancel := r.requestContext(ctx)
	defer cancel()
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
		req.Action = "release-" + action
	}
	plan, err := r.api.Plan(ctx, req)
	if err != nil {
		return tui.PlannedAction{}, err
	}
	out := tui.PlannedAction{ID: plan.ID, Key: control.NewIdempotencyKey(plan.ID), Warnings: plan.Warnings, RequiresConfirmation: len(plan.Warnings) != 0}
	for _, id := range plan.Affected {
		out.Affected = append(out.Affected, model.ServiceID(id))
	}
	return out, nil
}
func (r *Remote) ExecuteAction(ctx context.Context, plan tui.PlannedAction) error {
	ctx, cancel := r.requestContext(ctx)
	defer cancel()
	op, err := r.api.Submit(ctx, control.SubmitRequest{PlanID: plan.ID, IdempotencyKey: plan.Key})
	if err != nil {
		return fmt.Errorf("Plan %s submission failed; query operations before retrying: %w", plan.ID, err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if terminal(op.State) {
			if op.Error != nil {
				return op.Error
			}
			if op.State != "succeeded" {
				return fmt.Errorf("Operation %s %s", op.ID, op.State)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Operation %s continues on its host; query operations for its result: %w", op.ID, ctx.Err())
		case <-ticker.C:
			opID := op.ID
			op, err = r.api.Operation(ctx, opID)
			if err != nil {
				return fmt.Errorf("Operation %s result unresolved; query operations: %w", opID, err)
			}
		}
	}
}

func (r *Remote) requestContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(r.ctx, cancel)
	if r.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

func (r *Remote) refresh(ctx context.Context) error {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	snapshot, err := r.api.Snapshot(ctx)
	if err != nil {
		return err
	}
	projected := remoteSnapshot(r.workspace, snapshot)
	r.mu.Lock()
	r.snapshot = projected
	r.mu.Unlock()
	// Read the entire retained initial backlog, then only new pages. Both stores
	// retain their existing per-service and total memory bounds.
	for {
		page, err := r.api.Logs(ctx, "", r.cursor, 500)
		if err != nil {
			return err
		}
		previous := r.cursor
		if page.Reset {
			r.mu.Lock()
			r.store = logs.NewStore()
			r.mu.Unlock()
			previous = 0
		}
		store := r.Logs()
		if page.Gap {
			store.Append(logs.Entry{Stream: "system", Text: fmt.Sprintf("Earlier host log entries were evicted (%d dropped)", page.Dropped)})
		}
		for _, entry := range page.Entries {
			store.Append(logs.Entry{Time: entry.Time, ProjectID: entry.ProjectID, ServiceID: model.ServiceID(entry.ServiceID), Stream: entry.Stream, Text: entry.Text})
		}
		r.cursor = page.NextCursor
		if len(page.Entries) < 500 || r.cursor <= previous {
			return nil
		}
	}
}

// Local files supply descriptive metadata only. The remote node list, states,
// identities, dependency edges and metrics remain authoritative, including when
// local YAML changed while the engine retained the original workspace.
func remoteSnapshot(w model.Workspace, snapshot control.Snapshot) model.Snapshot {
	out := model.Snapshot{Root: w.Root, ObservedAt: snapshot.ObservedAt, Candidates: w.Candidates}
	if snapshot.ComposeConfigured {
		out.DockerFile = "Engine-managed Compose"
	}
	specs := map[string]model.Service{}
	projects := map[string]model.Project{}
	for _, project := range w.Projects {
		projects[project.ID] = model.Project{ID: project.ID, Name: project.Name, SourceFile: project.SourceFile}
		for _, spec := range project.Services {
			specs[string(spec.ID)] = spec
		}
	}
	processes := map[string]control.Process{}
	for _, process := range snapshot.Processes {
		processes[process.ID] = process
	}
	projectOrder := []string{}
	for _, node := range snapshot.Nodes {
		spec := specs[node.ID]
		spec.ID, spec.Name, spec.ProjectID, spec.Kind = model.ServiceID(node.ID), node.Name, node.ProjectID, node.Kind
		if node.Version != 0 {
			spec.Version = node.Version
		}
		if node.AllActions != nil {
			included := *node.AllActions
			spec.AllActions = &included
			if node.Kind == "resource" {
				resource := model.ResourceSpec{}
				if spec.Resource != nil {
					resource = *spec.Resource
				}
				resource.AllActions = included
				spec.Resource = &resource
			}
		}
		if spec.Key == "" {
			parts := strings.Split(node.ID, "/")
			spec.Key = parts[len(parts)-1]
		}
		portNames := map[int]string{}
		for _, port := range spec.Ports {
			portNames[port.Number] = port.Name
		}
		spec.DependsOn, spec.Requires, spec.Ports = nil, nil, nil
		for _, id := range node.DependsOn {
			spec.DependsOn = append(spec.DependsOn, model.ServiceID(id))
		}
		spec.Control = ""
		if node.Ownership == "external" {
			spec.Control = "observe"
		}
		service := model.ServiceSnapshot{Spec: spec, State: node.State, Reason: node.Reason, ObservedState: node.ObservedState, ObservedReason: node.ObservedReason, ReadinessChecked: node.ReadinessChecked, Metric: remoteMetric(node.Metric), MetricSource: node.MetricSource, MetricError: node.MetricError}
		for _, ref := range node.ResourceRefs {
			p, ok := processes[ref]
			if !ok || p.PID <= 0 || p.CreatedUnixMillis == nil || *p.CreatedUnixMillis <= 0 {
				continue
			}
			identity := model.ProcessIdentity{PID: p.PID, CreatedMillis: *p.CreatedUnixMillis}
			service.Processes = append(service.Processes, model.ProcessSample{Identity: identity, Metric: remoteMetric(p.Metric)})
			if p.Ownership == "session" {
				service.Owned = append(service.Owned, identity)
			}
		}
		for _, port := range node.Ports {
			service.Spec.Ports = append(service.Spec.Ports, model.Port{Number: port.Port, Name: portNames[port.Port]})
			observation := model.PortObservation{Port: port.Port, Status: port.Status, Reason: port.Reason}
			for _, ref := range port.ResourceRefs {
				if p, ok := processes[ref]; ok {
					observation.Listeners = append(observation.Listeners, model.Listener{PID: p.PID})
				}
			}
			service.Ports = append(service.Ports, observation)
		}
		out.Services = append(out.Services, service)
		project := projects[node.ProjectID]
		if len(project.Services) == 0 {
			projectOrder = append(projectOrder, node.ProjectID)
		}
		project.ID = node.ProjectID
		if project.Name == "" {
			project.Name = node.ProjectID
		}
		project.Services = append(project.Services, service.Spec)
		projects[node.ProjectID] = project
	}
	sort.Strings(projectOrder)
	for _, id := range projectOrder {
		out.Projects = append(out.Projects, projects[id])
	}
	for _, container := range snapshot.Containers {
		docker := model.DockerSnapshot{EndpointIdentity: container.EndpointIdentity, ID: container.ID, Service: container.ServiceID, Name: container.Name, Image: container.Image, State: container.State, Health: container.Health, Reason: container.Reason, Metric: remoteMetric(container.Metric), MetricError: container.MetricError}
		ports := []string{}
		for _, endpoint := range container.PublishedEndpoints {
			docker.PublishedEndpoints = append(docker.PublishedEndpoints, model.PublishedEndpoint{Host: endpoint.Host, Port: endpoint.Port})
			ports = append(ports, fmt.Sprintf("%s:%d", endpoint.Host, endpoint.Port))
		}
		docker.Ports = strings.Join(ports, ", ")
		out.Docker = append(out.Docker, docker)
		out.DockerFile = "Engine-managed Compose"
	}
	for _, d := range snapshot.Diagnostics {
		out.Diagnostics = append(out.Diagnostics, model.Diagnostic{Severity: d.Severity, Code: d.Code, File: d.File, Field: d.Field, Message: d.Message, Line: d.Line, Column: d.Column})
		if d.Code == "docker_observation" {
			out.DockerError = d.Message
			out.DockerFile = "Engine-managed Compose"
		}
	}
	return out
}

func remoteMetric(metric control.Metric) model.Metric {
	out := model.Metric{Known: metric.Known, Partial: metric.Partial, CPUPercent: metric.CPUPercent}
	if metric.SampledAt != nil {
		out.SampledAt = *metric.SampledAt
	}
	if metric.RSSBytes != nil {
		out.RSS = *metric.RSSBytes
	}
	if metric.UptimeMillis != nil {
		out.Uptime = time.Duration(*metric.UptimeMillis) * time.Millisecond
	}
	return out
}
