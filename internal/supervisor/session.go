package supervisor

import (
	"context"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/docker"
	"github.com/szhjia/stackharbor/internal/graph"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/process"
	"github.com/szhjia/stackharbor/internal/runner"
	"os"
	"sync"
	"time"
)

type entry struct {
	resourceInstances                        []string
	resourceRunning                          bool
	resourceMutation                         bool
	processSamples                           []model.ProcessSample
	spec                                     model.Service
	state, reason                            string
	observedState, observedReason            string
	gen                                      uint64
	handle                                   runner.Handle
	cancel                                   context.CancelFunc
	ready, launched                          chan struct{}
	readyClosed                              bool
	startErr                                 error
	exit                                     *int
	metric                                   model.Metric
	metricSource                             string
	metricError                              string
	ports                                    []model.PortObservation
	checked                                  bool
	operationID, attemptID, resourceIdentity string
	writeStarted                             bool
	observed                                 []model.ProcessIdentity
}
type Session struct {
	observedAt   time.Time
	startMu      sync.Mutex
	mu           sync.Mutex
	docker       *docker.Manager
	resources    map[model.ServiceID]*docker.Manager
	inputDigests map[string]string
	dockerRows   []model.DockerSnapshot
	dockerError  string
	workspace    model.Workspace
	entries      map[model.ServiceID]*entry
	graph        *graph.Graph
	runner       runner.Runner
	ports        observe.PortProbe
	sampler      *observe.Sampler
	store        *logs.Store
	lock         *Lock
	ctx          context.Context
	cancel       context.CancelFunc
	sem          chan struct{}
	events       []model.Event
	tool         model.Metric
	toolID       model.ProcessIdentity
	closed       bool
	shutdownOnce sync.Once
	shutdownDone chan struct{}
	shutdownErr  error
	workers      sync.WaitGroup
}

func New(w model.Workspace, r runner.Runner, p observe.PortProbe, sampler *observe.Sampler) (*Session, error) {
	if w.Invalid() {
		return nil, errors.New("Project configuration contains errors")
	}
	g, ds := graph.Build(w.Services())
	if len(ds) > 0 {
		return nil, fmt.Errorf("Invalid dependency graph: %s", ds[0].Message)
	}
	w.Projects = g.Navigation(w)
	lock, e := Acquire(w.Root)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{workspace: w, entries: map[model.ServiceID]*entry{}, graph: g, runner: r, ports: p, sampler: sampler, store: logs.NewStore(), lock: lock, ctx: ctx, cancel: cancel, sem: make(chan struct{}, 4), shutdownDone: make(chan struct{})}
	s.docker = docker.New(w.Root)
	s.resources = map[model.ServiceID]*docker.Manager{}
	s.inputDigests = map[string]string{}
	managers := map[string]*docker.Manager{}
	s.dockerRows = s.docker.Specs()
	s.dockerError = s.docker.Error
	for _, v := range w.Services() {
		s.entries[v.ID] = &entry{spec: v, state: "stopped"}
		if v.Resource != nil {
			key := v.Resource.File + "\x00" + v.Resource.Project
			if managers[key] == nil {
				managers[key] = docker.NewScoped(w.Root, v.Resource.File, v.Resource.Project)
			}
			s.resources[v.ID] = managers[key]
		}
	}
	if err := s.freezeInputs(); err != nil {
		cancel()
		lock.Close()
		return nil, err
	}
	if len(s.resources) > 0 {
		s.dockerRows = nil
		for _, n := range w.Services() {
			if n.Resource != nil {
				s.dockerRows = append(s.dockerRows, model.DockerSnapshot{Service: string(n.ID), Name: n.Name, State: "unknown"})
			}
		}
	}
	if info, e := process.NewReader().Read(ctx, int32(os.Getpid())); e == nil {
		s.toolID = info.Identity
	}
	s.workers.Add(1)
	go s.observeLoop()
	return s, nil
}
func (s *Session) Logs() *logs.Store { return s.store }
func (s *Session) event(id model.ServiceID, kind, message string) {
	event := model.Event{Time: time.Now(), ServiceID: id, Kind: kind, Message: message}
	if e := s.entries[id]; e != nil {
		event.OperationID = e.operationID
		event.AttemptID = e.attemptID
		event.Message = Redact(e.spec, message)
	}
	s.events = append(s.events, event)
	s.record(event)
	if len(s.events) > 200 {
		s.events = s.events[len(s.events)-200:]
	}
}
func (s *Session) readyResult(e *entry, err error) {
	if !e.readyClosed {
		e.startErr = err
		e.readyClosed = true
		close(e.ready)
	}
}
func (s *Session) Start(ctx context.Context, ids []model.ServiceID) error {
	for !s.startMu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer s.startMu.Unlock()
	if err := s.checkInputs(); err != nil {
		return err
	}
	op := newOperationID()
	created := map[model.ServiceID]uint64{}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("Session closed")
	}
	for _, id := range ids {
		if e, ok := s.entries[id]; !ok {
			s.mu.Unlock()
			return fmt.Errorf("Unknown service %s", id)
		} else if e.state == "stopping" || (e.handle != nil && e.state == "failed") {
			s.mu.Unlock()
			return fmt.Errorf("%s has not fully stopped", id)
		}
	}
	plan, err := s.graph.Plan(s.workspace, "start", ids)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	order := []model.ServiceID{}
	for _, layer := range plan.OrderedLayers {
		order = append(order, layer...)
	}
	s.record(model.Event{Time: time.Now(), Kind: "operation/start", OperationID: op, Message: fmt.Sprint(ids)})
	for _, id := range order {
		e := s.entries[id]
		if e.state == "stopping" || (e.handle != nil && e.state == "failed") {
			s.mu.Unlock()
			return fmt.Errorf("%s has not fully stopped", id)
		}
	}
	generations := map[model.ServiceID]uint64{}
	wait := []chan struct{}{}
	for _, id := range order {
		e := s.entries[id]
		if e.state == "stopped" || e.state == "failed" || e.state == "blocked" || e.state == "unknown" || e.state == "unavailable" || e.state == "unhealthy" || e.spec.Control == "observe" && e.state == "unready" || e.spec.Kind == "task" && e.state == "succeeded" || e.spec.Kind == "resource" && e.state == "available" {
			e.gen++
			e.operationID = op
			e.attemptID = fmt.Sprintf("%s/%s/%d", op, id, e.gen)
			e.writeStarted = false
			e.state = "waiting"
			e.reason = "Waiting for dependencies"
			e.ready = make(chan struct{})
			e.launched = make(chan struct{})
			e.readyClosed = false
			e.startErr = nil
			e.exit = nil
			runctx, cancel := context.WithCancel(control.InheritResourceLocks(s.ctx, ctx))
			e.cancel = cancel
			gen := e.gen
			s.workers.Add(1)
			go s.launch(runctx, id, gen)
		}
		if e.state == "waiting" {
			created[id] = e.gen
		}
		generations[id] = e.gen
		if e.ready != nil {
			wait = append(wait, e.ready)
		}
	}
	s.mu.Unlock()
	for _, done := range wait {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.record(model.Event{Time: time.Now(), Kind: "operation/result", OperationID: op, Message: "canceled"})
			for id, gen := range created {
				e := s.entries[id]
				if e.gen == gen && e.cancel != nil {
					e.cancel()
				}
			}
			s.mu.Unlock()
			return ctx.Err()
		case <-done:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	errs := []error{}
	for _, id := range order {
		if e := s.entries[id]; e.gen != generations[id] {
			errs = append(errs, fmt.Errorf("%s start cancelled by another action", id))
			continue
		}
		if e := s.entries[id]; e.startErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, e.startErr))
		}
	}
	result := "success"
	if len(errs) > 0 {
		result = "failed"
		if len(errs) < len(order) {
			result = "partial"
		}
	}
	s.record(model.Event{Time: time.Now(), Kind: "operation/result", OperationID: op, Message: result})
	return errors.Join(errs...)
}
func (s *Session) launch(ctx context.Context, id model.ServiceID, gen uint64) {
	defer s.workers.Done()
	s.mu.Lock()
	e := s.entries[id]
	launched := e.launched
	spec := e.spec
	s.mu.Unlock()
	defer close(launched)
	fail := func(err error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if e.gen == gen {
			e.state = "failed"
			e.reason = Redact(e.spec, err.Error())
			s.readyResult(e, err)
			s.event(id, "error", err.Error())
		}
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 600*time.Second)
	defer waitCancel()
	for _, dep := range spec.DependsOn {
		for {
			s.mu.Lock()
			d := s.entries[dep]
			state := d.state
			condition := ""
			for _, edge := range spec.Requires {
				if edge.Node == dep {
					condition = edge.Condition
				}
			}
			okay := dependencySatisfied(d, condition)
			depErr := d.startErr
			s.mu.Unlock()
			if okay {
				break
			}
			if state == "failed" || state == "stopped" || state == "stopping" || state == "blocked" || state == "unknown" || state == "unavailable" || state == "unhealthy" || state == "unready" && depErr != nil {
				s.mu.Lock()
				why := d.reason
				s.mu.Unlock()
				fail(fmt.Errorf("Dependency %s not ready: %s", dep, why))
				s.mu.Lock()
				if e.gen == gen {
					e.state = "blocked"
				}
				s.mu.Unlock()
				return
			}
			select {
			case <-waitCtx.Done():
				fail(fmt.Errorf("Waiting for dependencies timed out or cancelled: %w", waitCtx.Err()))
				s.mu.Lock()
				if e.gen == gen {
					e.state = "blocked"
				}
				s.mu.Unlock()
				return
			case <-time.After(25 * time.Millisecond):
			}
		}
	}

	if spec.Kind == "task" {
		s.runTask(ctx, id, gen, spec, fail)
		return
	}
	select {
	case <-ctx.Done():
		fail(ctx.Err())
		return
	case s.sem <- struct{}{}:
	}
	defer func() { <-s.sem }()
	s.mu.Lock()
	if e.gen != gen {
		s.mu.Unlock()
		return
	}
	e.state = "starting"
	e.reason = ""
	s.mu.Unlock()
	if err := s.docker.Ensure(ctx, spec.DockerDependsOn); err != nil {
		fail(err)
		return
	}
	if spec.Kind == "resource" {
		s.runResource(ctx, id, gen, spec, fail)
		return
	}
	if spec.Control == "observe" {
		s.runObserved(ctx, id, gen, spec, fail)
		return
	}
	if len(spec.Ports) > 0 {
		if err := s.ports.CheckStart(ctx, spec.Ports); err != nil {
			fail(err)
			return
		}
	}
	h, err := s.runner.Start(ctx, spec, func(stream, line string) {
		s.store.Append(logs.Entry{ServiceID: id, ProjectID: spec.ProjectID, Stream: stream, Text: Redact(spec, line)})
	})
	if err != nil {
		fail(err)
		return
	}
	s.mu.Lock()
	e.handle = h
	stale := e.gen != gen
	if !stale {
		s.event(id, "start", "Service started")
	}
	s.mu.Unlock()
	if stale {
		return
	}
	s.workers.Add(1)
	go s.watch(ctx, id, gen, h)
}
func (s *Session) watch(ctx context.Context, id model.ServiceID, gen uint64, h runner.Handle) {
	defer s.workers.Done()
	s.mu.Lock()
	spec := s.entries[id].spec
	s.mu.Unlock()
	timeout := 60 * time.Second
	if spec.Ready != nil && spec.Ready.Timeout > 0 {
		timeout = spec.Ready.Timeout
	}
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	everReady := false
	probe := func() {
		var err error
		if spec.Ready != nil {
			err = observe.CheckHealth(ctx, *spec.Ready)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		e := s.entries[id]
		if e.gen != gen || ctx.Err() != nil {
			return
		}
		e.checked = spec.Ready != nil
		if err == nil {
			for _, dep := range spec.DependsOn {
				d := s.entries[dep]
				condition := ""
				for _, edge := range spec.Requires {
					if edge.Node == dep {
						condition = edge.Condition
					}
				}
				if !dependencySatisfied(d, condition) {
					err = fmt.Errorf("Dependency %s unavailable: %s", dep, d.reason)
					break
				}
			}
		}
		if err == nil {
			if e.state == "unready" && e.readyClosed && e.startErr != nil {
				e.ready = make(chan struct{})
				e.readyClosed = false
				e.startErr = nil
			}
			everReady = true
			state := "running"
			if spec.Version == 2 && spec.Ready == nil {
				state = "started"
			}
			if e.state != state {
				e.state = state
				e.reason = ""
				s.readyResult(e, nil)
				s.event(id, "ready", "Service ready")
			}
			tick.Reset(2 * time.Second)
		} else {
			e.reason = err.Error()
			if everReady || time.Now().After(deadline) {
				if e.state != "unready" {
					s.event(id, "unready", err.Error())
					if e.readyClosed {
						e.ready = make(chan struct{})
						e.readyClosed = false
						e.startErr = nil
					}
				}
				e.state = "unready"
				if spec.Version == 2 && !everReady {
					s.readyResult(e, fmt.Errorf("Readiness timed out: %s", err))
				}
			}
		}
	}
	probe()
	for {
		select {
		case <-ctx.Done():
			return
		case result := <-h.Done():
			s.mu.Lock()
			e := s.entries[id]
			if e.gen == gen {
				e.exit = &result.Code
				e.state = "failed"
				e.reason = fmt.Sprintf("Root process exited (%d)", result.Code)
				if len(h.Identities()) == 0 {
					e.handle = nil
				}
				s.readyResult(e, errors.New(e.reason))
				s.event(id, "exit", e.reason)
			}
			s.mu.Unlock()
			return
		case <-tick.C:
			probe()
		}
	}
}
func (s *Session) Affected(ids []model.ServiceID) []model.ServiceID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.affectedLocked(ids)
}
func (s *Session) affectedLocked(ids []model.ServiceID) []model.ServiceID {
	out := []model.ServiceID{}
	for _, id := range s.graph.Dependents(ids) {
		e := s.entries[id]
		if e.spec.Control != "observe" && (e.handle != nil || e.state == "waiting" || e.state == "starting") {
			out = append(out, id)
		}
	}
	return out
}
func (s *Session) Stop(ctx context.Context, ids []model.ServiceID) error {
	s.mu.Lock()
	op := newOperationID()
	all := append(append([]model.ServiceID{}, ids...), s.affectedLocked(ids)...)
	order := s.graph.StopOrder(all)
	for _, id := range order {
		if e := s.entries[id]; e != nil {
			e.operationID = op
		}
	}
	s.record(model.Event{Time: time.Now(), Kind: "operation/stop", OperationID: op, Message: fmt.Sprint(order)})
	s.mu.Unlock()
	errs := []error{}
	for _, id := range order {
		if err := s.stopOne(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	s.mu.Lock()
	result := "success"
	if len(errs) > 0 {
		result = "failed"
	}
	s.record(model.Event{Time: time.Now(), Kind: "operation/result", OperationID: op, Message: result})
	s.mu.Unlock()
	return errors.Join(errs...)
}
func (s *Session) stopOne(ctx context.Context, id model.ServiceID) error {
	s.mu.Lock()
	node := s.entries[id]
	if node != nil && node.spec.Kind == "resource" {
		if node.cancel != nil {
			node.cancel()
		}
		node.gen++
		launched := node.launched
		spec := node.spec
		s.mu.Unlock()
		if launched != nil {
			select {
			case <-launched:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		r := spec.Resource
		if r.Control == "observe" {
			return errors.New("Read-only resources cannot be stopped")
		}
		m := s.resources[id]
		keys, err := m.LockKeys(ctx, []string{r.Service})
		if err != nil {
			return err
		}
		locked, release, err := control.WithResourceLocks(ctx, control.ResourceLockNamespace, keys)
		if err != nil {
			return err
		}
		defer release()
		ctx = locked
		s.mu.Lock()
		closing, expected := s.closed, append([]string(nil), node.resourceInstances...)
		expectedKnown := node.resourceInstances != nil
		s.mu.Unlock()
		if closing {
			if !expectedKnown {
				return fmt.Errorf("Resource instance identities unavailable; cleanup refused")
			}
			rows, reason := m.Observe(ctx)
			if reason != "" {
				return fmt.Errorf("Resource identity unavailable during cleanup")
			}
			current, _, known := physicalResourceObservation(r.Service, rows)
			if !known || !sameResourceInstances(current, expected) {
				return fmt.Errorf("Resource instance set changed; cleanup refused")
			}
		}
		if err := m.Action(ctx, "stop", []string{r.Service}); err != nil {
			return err
		}
		s.mu.Lock()
		node.state = "stopped"
		node.resourceRunning = false
		node.resourceMutation = false
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	s.mu.Lock()
	e := s.entries[id]
	if e == nil {
		s.mu.Unlock()
		return fmt.Errorf("Unknown service %s", id)
	}
	if e.spec.Control == "observe" {
		s.mu.Unlock()
		return errors.New("Read-only services cannot be stopped")
	}
	if e.state == "stopped" || e.spec.Kind == "task" && e.state == "succeeded" || e.spec.Kind == "resource" {
		s.mu.Unlock()
		return nil
	}
	if e.state == "stopping" {
		s.mu.Unlock()
		return fmt.Errorf("%s is stopping", id)
	}
	uncertain := e.writeStarted && e.spec.Task != nil && e.spec.Task.Effect == "schema-write"
	e.gen++
	e.state = "stopping"
	if e.cancel != nil {
		e.cancel()
	}
	if e.ready != nil {
		s.readyResult(e, errors.New("Service stopped"))
	}
	launched := e.launched
	s.mu.Unlock()
	if launched != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-launched:
		}
	}
	s.mu.Lock()
	h := e.handle
	s.mu.Unlock()
	result := runner.StopResult{Complete: true}
	if h != nil {
		result = h.Stop(ctx, e.spec.Stop)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !result.Complete {
		e.state = "failed"
		e.reason = "Process tree shutdown incomplete"
		s.event(id, "error", e.reason)
		return fmt.Errorf("%s: %s: %v", id, e.reason, result.Errors)
	}
	e.handle = nil
	e.state = "stopped"
	e.reason = ""
	if uncertain {
		e.state = "unknown"
		e.reason = "Write task interrupted; check database state again before retrying"
	}
	e.metric = model.Metric{}
	s.event(id, "stop", "Service stopped")
	return nil
}
func (s *Session) Restart(ctx context.Context, ids []model.ServiceID) error {
	s.mu.Lock()
	set := append(append([]model.ServiceID{}, ids...), s.affectedLocked(ids)...)
	s.mu.Unlock()
	if err := s.Stop(ctx, ids); err != nil {
		return err
	}
	return s.Start(ctx, set)
}

// cleanupTargetsLocked is shared by cleanup and its plan disclosure. The caller
// holds s.mu; persistent and observe-only resources retain their own lifetime.
func (s *Session) cleanupTargetsLocked() []model.ServiceID {
	ids := []model.ServiceID{}
	for id, e := range s.entries {
		if e.spec.Control == "observe" || e.spec.Resource != nil && (e.spec.Resource.Lifetime == "persistent" || e.spec.Resource.Control == "observe") {
			continue
		}
		if e.spec.Resource != nil && !e.resourceRunning && !e.resourceMutation && e.state != "waiting" && e.state != "starting" && e.state != "stopping" {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// Cleanup stops owned ephemeral resources and drains workers while retaining the
// session lock. A host can durably publish its final record before ReleaseLock.
func (s *Session) Cleanup(ctx context.Context) error {
	s.shutdownOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		ids := s.cleanupTargetsLocked()
		s.mu.Unlock()
		s.cancel()
		budget, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		s.shutdownErr = s.Stop(budget, ids)
		done := make(chan struct{})
		go func() { s.workers.Wait(); close(done) }()
		select {
		case <-budget.Done():
			s.shutdownErr = errors.Join(s.shutdownErr, budget.Err())
		case <-done:
		}
		close(s.shutdownDone)
	})
	<-s.shutdownDone
	return s.shutdownErr
}

// ReleaseLock finalizes ownership only after Cleanup completed.
func (s *Session) ReleaseLock() error {
	<-s.shutdownDone
	return s.lock.Close()
}
func (s *Session) Shutdown(ctx context.Context) error {
	return errors.Join(s.Cleanup(ctx), s.ReleaseLock())
}

// SessionInfo returns a copied advisory identity for host registration.
func (s *Session) SessionInfo() SessionInfo                { return s.lock.Info() }
func (s *Session) PublishEndpoint(endpoint Endpoint) error { return s.lock.PublishEndpoint(endpoint) }
