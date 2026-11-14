package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/discovery"
	"github.com/szhjia/stackharbor/internal/docker"
	"github.com/szhjia/stackharbor/internal/graph"
	"github.com/szhjia/stackharbor/internal/inventory"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/process"
	"github.com/szhjia/stackharbor/internal/runner"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"github.com/szhjia/stackharbor/internal/web"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Options struct {
	StateDir, RuntimeDir string
	IdleTimeout          time.Duration
	Out                  io.Writer
}
type hosted struct {
	host        *sessionhost.Host
	environment []string
	workspace   string
}
type Server struct {
	mu            sync.Mutex
	observationMu sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	endpoint      Endpoint
	state         *stateStore
	hosts         map[string]*hosted
	gateways      map[string]*web.Gateway
	owners        process.Reader
	metrics       process.Reader
	ports         observe.PortProbe
	dockerCache   *docker.ObservationCache
	budget        *logs.Budget
	lastUse       time.Time
	clients       atomic.Int64
	writes        atomic.Int64
	foreground    atomic.Int64
	observeWake   chan struct{}
	closing       bool
	opening       map[string]chan struct{}
}

func Serve(ctx context.Context, o Options) error {
	var err error
	if o.StateDir == "" {
		o.StateDir, err = StateDirectory()
	} else {
		o.StateDir, err = privatePath(o.StateDir)
	}
	if err != nil {
		return err
	}
	if o.RuntimeDir == "" {
		o.RuntimeDir, err = RuntimeDirectory()
	} else {
		o.RuntimeDir, err = privatePath(o.RuntimeDir)
	}
	if err != nil {
		return err
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = 60 * time.Second
	}
	fd, err := syscall.Open(filepath.Join(o.RuntimeDir, "engine.lock"), syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	lock := os.NewFile(uintptr(fd), "engine-lock")
	defer lock.Close()
	if st, e := lock.Stat(); e != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return fmt.Errorf("unsafe engine lock")
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return &control.APIError{Code: "engine_conflict", Message: "Another engine owns the user runtime"}
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	path := filepath.Join(o.RuntimeDir, "engine.sock")
	if st, e := os.Lstat(path); e == nil {
		if _, e = sessionapi.ValidatePrivateSocket(path); e != nil {
			return e
		}
		if c, e := net.DialTimeout("unix", path, 200*time.Millisecond); e == nil {
			c.Close()
			return fmt.Errorf("existing engine endpoint is active; preserved")
		}
		now, e := os.Lstat(path)
		if e != nil || !os.SameFile(st, now) {
			return fmt.Errorf("engine socket changed")
		}
		if e = os.Remove(path); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	state, err := readState(o.StateDir)
	if err != nil {
		return err
	}
	life, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &Server{ctx: life, cancel: cancel, state: state, hosts: map[string]*hosted{}, gateways: map[string]*web.Gateway{}, budget: logs.NewBudget(32 * 1024 * 1024), lastUse: time.Now(), observeWake: make(chan struct{}, 1)}
	base := process.NewReader()
	s.owners = process.NewSharedReader(base, s.ownershipCadence)
	s.metrics = process.NewSharedReader(base, s.cadence)
	s.ports = observe.NewSharedPortProbe(observe.NewPortProbe(), s.cadence)
	s.dockerCache = docker.NewObservationCache(s.cadence)
	birth, err := process.CreatedMillis(ctx, int32(os.Getpid()))
	if err != nil {
		return err
	}
	s.endpoint = Endpoint{SocketPath: path, PID: os.Getpid(), ProcessCreatedMillis: birth, ProtocolVersion: ProtocolVersion, BuildVersion: buildinfo.Version}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	inode, err := os.Lstat(path)
	if err != nil {
		return err
	}
	meta := filepath.Join(o.RuntimeDir, "engine.json")
	defer func() {
		if st, e := os.Lstat(path); e == nil && os.SameFile(st, inode) {
			_ = os.Remove(path)
		}
		b, e := os.ReadFile(meta)
		var current Endpoint
		if e == nil && json.Unmarshal(b, &current) == nil && current.PID == s.endpoint.PID && current.ProcessCreatedMillis == birth {
			_ = os.Remove(meta)
		}
	}()
	b, _ := json.Marshal(s.endpoint)
	if err = atomicWrite(meta, b, 0600); err != nil {
		return err
	}
	if o.Out != nil {
		_ = json.NewEncoder(o.Out).Encode(s.endpoint)
	}
	httpServer := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	go s.observeLoop()
	idle := time.NewTicker(min(time.Second, o.IdleTimeout))
	defer idle.Stop()
loop:
	for {
		select {
		case <-life.Done():
			break loop
		case err = <-done:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			break loop
		case <-idle.C:
			s.mu.Lock()
			expired := time.Since(s.lastUse) >= o.IdleTimeout
			s.mu.Unlock()
			if expired && s.clients.Load() == 0 && !s.busy() {
				cancel()
			}
		}
	}
	s.mu.Lock()
	s.closing = true
	hosts := make([]*sessionhost.Host, 0, len(s.hosts))
	for _, h := range s.hosts {
		hosts = append(hosts, h.host)
	}
	s.mu.Unlock()
	cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	var failures []error
	var wg sync.WaitGroup
	var failuresMu sync.Mutex
	for _, h := range hosts {
		wg.Add(1)
		go func(h *sessionhost.Host) {
			defer wg.Done()
			if e := h.Close(cleanup); e != nil {
				failuresMu.Lock()
				failures = append(failures, e)
				failuresMu.Unlock()
			}
		}(h)
	}
	wg.Wait()
	if e := httpServer.Shutdown(cleanup); e != nil {
		_ = httpServer.Close()
		failures = append(failures, e)
	}
	return errors.Join(failures...)
}
func (s *Server) cadence() time.Duration {
	if s.foreground.Load() > 0 {
		return 2 * time.Second
	}
	return 10 * time.Second
}

// Only background topology discovery is deferred. Launch, stop, signal and
// group-empty cleanup checks bypass this cache and retain fresh identities.
func (s *Server) ownershipCadence() time.Duration {
	if s.foreground.Load() > 0 {
		return time.Second
	}
	return 5 * time.Second
}
func (s *Server) wakeObservation() {
	select {
	case s.observeWake <- struct{}{}:
	default:
	}
}
func (s *Server) observeLoop() {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.observeWake:
		case <-timer.C:
		}
		s.mu.Lock()
		hosts := make([]*sessionhost.Host, 0, len(s.hosts))
		for _, h := range s.hosts {
			hosts = append(hosts, h.host)
		}
		s.mu.Unlock()
		s.observationMu.RLock()
		request, cancel := context.WithTimeout(s.ctx, 8*time.Second)
		var managers []*docker.Manager
		for _, h := range hosts {
			managers = append(managers, h.ObservationManagers()...)
		}
		prepared := request
		if s.dockerCache != nil {
			prepared = s.dockerCache.Prepare(request, managers)
		}
		for _, h := range hosts {
			if s.ctx.Err() != nil {
				break
			}
			h.Observe(prepared)
		}
		cancel()
		s.observationMu.RUnlock()
		timer.Reset(s.cadence())
	}
}
func (s *Server) busy() bool {
	_, processes, operations := s.counts()
	return processes > 0 || operations > 0
}
func (s *Server) counts() (int, int, int) {
	s.mu.Lock()
	hosts := make([]*sessionhost.Host, 0, len(s.hosts))
	for _, h := range s.hosts {
		hosts = append(hosts, h.host)
	}
	s.mu.Unlock()
	p, o := 0, 0
	for _, h := range hosts {
		processes, resources := h.OwnershipCounts()
		p += processes + resources
		for _, op := range h.Coordinator().Operations() {
			if op.State == "queued" || op.State == "running" {
				o++
			}
		}
	}
	return len(hosts), p, o
}

func canonicalRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("workspace root must be absolute")
	}
	p, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(p)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("workspace root must be a directory")
	}
	return filepath.Clean(p), nil
}
func (s *Server) normalize(r OpenRequest) (OpenRequest, error) {
	var err error
	r.Root, err = canonicalRoot(r.Root)
	if err != nil {
		return r, err
	}
	r.Namespace, err = web.ResolveNamespace(r.Namespace)
	if err != nil {
		return r, err
	}
	if r.Workspace != "" {
		if !filepath.IsAbs(r.Workspace) {
			r.Workspace = filepath.Join(r.Root, r.Workspace)
		}
		r.Workspace, err = filepath.EvalSymlinks(r.Workspace)
	}
	return r, err
}
func (s *Server) reserve(ctx context.Context, key string) (func(), error) {
	for {
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			return nil, fmt.Errorf("engine is stopping")
		}
		if s.opening == nil {
			s.opening = map[string]chan struct{}{}
		}
		if wait := s.opening[key]; wait != nil {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-wait:
				continue
			}
		}
		ready := make(chan struct{})
		s.opening[key] = ready
		s.mu.Unlock()
		return func() { s.mu.Lock(); delete(s.opening, key); close(ready); s.mu.Unlock() }, nil
	}
}
func (s *Server) Open(ctx context.Context, r OpenRequest) (supervisor.SessionInfo, error) {
	r, err := s.normalize(r)
	if err != nil {
		return supervisor.SessionInfo{}, err
	}
	key := workspaceKey(r.Namespace, r.Root)
	release, err := s.reserve(ctx, key)
	if err != nil {
		return supervisor.SessionInfo{}, err
	}
	defer release()
	s.mu.Lock()
	h := s.hosts[key]
	s.lastUse = time.Now()
	s.mu.Unlock()
	if h != nil {
		select {
		case <-h.host.Done():
		default:
			return h.host.SessionInfo(), nil
		}
	}
	s.state.mu.Lock()
	entry := s.state.entries[key]
	if entry != nil && entry.RecoveryRequired {
		s.state.mu.Unlock()
		return supervisor.SessionInfo{}, &control.APIError{Code: "recovery_conflict", Message: "Workspace requires explicit recovery; no startup was replayed"}
	}
	s.state.mu.Unlock()
	env := r.Environment
	if r.Shell || env == nil {
		env, err = ShellEnvironment(ctx, r.Root)
		if err != nil {
			return supervisor.SessionInfo{}, err
		}
	}
	w := discovery.Discover(ctx, discovery.Options{Root: r.Root, RootExplicit: true, WorkspaceFile: r.Workspace, Environment: env})
	_, ds := graph.Build(w.Services())
	w.Diagnostics = append(w.Diagnostics, ds...)
	if err = ctx.Err(); err != nil {
		return supervisor.SessionInfo{}, err
	}
	if w.Invalid() || len(w.Services()) == 0 {
		return supervisor.SessionInfo{}, &control.APIError{Code: "invalid_request", Message: "Workspace has no valid registered services; validate its StackHarbor YAML first"}
	}
	s.state.mu.Lock()
	if entry == nil {
		entry = &workspaceState{Record: WorkspaceRecord{Root: r.Root, Namespace: r.Namespace, Workspace: r.Workspace, State: "offline", UpdatedAt: time.Now().UTC()}, Attempts: map[string]attempt{}}
		s.state.entries[key] = entry
	}
	err = s.state.saveLocked()
	s.state.mu.Unlock()
	if err != nil {
		return supervisor.SessionInfo{}, err
	}
	journal := workspaceJournal{s.state, key}
	host, err := sessionhost.NewWithOptions(s.ctx, w, sessionhost.Options{Namespace: r.Namespace, Managed: true, Environment: env, Reader: s.owners, MetricReader: s.metrics, Ports: s.ports, Runner: runner.NewManaged(s.owners, journal), ExternalObservation: true, OnRequest: s.beginRequest, BeforeSubmit: journal.BeforeSubmit, AfterClose: journal.Closed, ObservationCache: s.dockerCache})
	if err != nil {
		return supervisor.SessionInfo{}, err
	}
	s.budget.Register(host.Controller().Logs())
	s.mu.Lock()
	s.hosts[key] = &hosted{host: host, environment: append([]string{}, env...), workspace: r.Workspace}
	s.mu.Unlock()
	s.wakeObservation()
	go func() {
		<-host.Done()
		s.budget.Unregister(host.Controller().Logs())
		s.mu.Lock()
		if current := s.hosts[key]; current != nil && current.host == host {
			delete(s.hosts, key)
		}
		s.mu.Unlock()
	}()
	return host.SessionInfo(), nil
}

func (s *Server) catalogue(namespace string) []WorkspaceRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	out := []WorkspaceRecord{}
	for key, e := range s.state.entries {
		if e.Record.Namespace != namespace {
			continue
		}
		r := e.Record
		r.Session = nil
		r.State = "offline"
		if e.RecoveryRequired {
			r.State = "recovery_required"
		}
		if h := s.hosts[key]; h != nil {
			info := h.host.SessionInfo()
			info.CacheDir = ""
			info.SocketPath = ""
			r.Session = &info
			if !e.RecoveryRequired {
				r.State = "running"
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Root < out[j].Root })
	return out
}
func engineJSON(w http.ResponseWriter, status int, data any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err != nil {
		var api *control.APIError
		if !errors.As(err, &api) {
			api = &control.APIError{Code: "unavailable", Message: err.Error()}
		}
		_ = json.NewEncoder(w).Encode(sessionapi.Response[any]{Error: api})
	} else {
		_ = json.NewEncoder(w).Encode(sessionapi.Response[any]{Data: data})
	}
}
func decodeRequest(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return &control.APIError{Code: "invalid_request", Message: "Invalid JSON request"}
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return &control.APIError{Code: "invalid_request", Message: "Exactly one JSON request required"}
	}
	return nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	done, beginErr := s.beginRequest(r)
	if beginErr != nil {
		engineJSON(w, 409, nil, beginErr)
		return
	}
	defer done()
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		ns, err := web.ResolveNamespace(r.Header.Get("X-StackHarbor-Namespace"))
		if err != nil {
			engineJSON(w, 400, nil, err)
			return
		}
		s.gateway(ns).ServeLocalHTTP(w, r)
		return
	}
	var result any
	var err error
	switch r.URL.Path {
	case "/engine/v1/identity":
		if r.Method != "GET" {
			err = &control.APIError{Code: "invalid_request", Message: "GET required"}
			break
		}
		n, p, o := s.counts()
		result = struct {
			Endpoint
			ActiveSessions    int `json:"active_sessions"`
			ActiveProcesses   int `json:"active_processes"`
			OperationsRunning int `json:"operations_running"`
		}{s.endpoint, n, p, o}
	case "/engine/v1/open":
		if r.Method != "POST" {
			err = &control.APIError{Code: "invalid_request", Message: "POST required"}
			break
		}
		var request OpenRequest
		err = decodeRequest(w, r, &request)
		if err == nil {
			result, err = s.Open(r.Context(), request)
		}
	case "/engine/v1/workspaces":
		var ns string
		ns, err = web.ResolveNamespace(r.URL.Query().Get("namespace"))
		if err == nil {
			result = map[string]any{"workspaces": s.catalogue(ns)}
		}
	case "/engine/v1/reload":
		if r.Method != "POST" {
			err = &control.APIError{Code: "invalid_request", Message: "POST required"}
			break
		}
		var request OpenRequest
		err = decodeRequest(w, r, &request)
		if err == nil {
			result, err = s.reload(r.Context(), request)
		}
	case "/engine/v1/shutdown":
		if r.Method != "POST" {
			err = &control.APIError{Code: "invalid_request", Message: "POST required"}
			break
		}
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		if s.writes.Load() > 0 || s.busy() {
			s.mu.Lock()
			s.closing = false
			s.mu.Unlock()
			err = &control.APIError{Code: "operation_conflict", Message: "Stop running projects and wait for operations before stopping engine"}
		} else {
			result = map[string]bool{"stopping": true}
			defer s.cancel()
		}
	case "/engine/v1/recovery":
		result, err = s.recovery(w, r)
	default:
		err = &control.APIError{Code: "not_found", Message: "Engine route not found"}
	}
	if err != nil {
		engineJSON(w, sessionapi.StatusCode(err), nil, err)
	} else {
		engineJSON(w, 200, result, nil)
	}
}
func (s *Server) beginRequest(r *http.Request) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, &control.APIError{Code: "session_closed", Message: "Engine is stopping"}
	}
	s.lastUse = time.Now()
	s.clients.Add(1)
	write := r.Method != "GET" && r.Method != "HEAD" && r.URL.Path != "/engine/v1/shutdown"
	if write {
		s.writes.Add(1)
	}
	return func() {
		s.clients.Add(-1)
		if write {
			s.writes.Add(-1)
		}
	}, nil
}
func (s *Server) reload(ctx context.Context, r OpenRequest) (supervisor.SessionInfo, error) {
	r, err := s.normalize(r)
	if err != nil {
		return supervisor.SessionInfo{}, err
	}
	key := workspaceKey(r.Namespace, r.Root)
	release, err := s.reserve(ctx, key)
	if err != nil {
		return supervisor.SessionInfo{}, err
	}
	defer release()
	s.mu.Lock()
	h := s.hosts[key]
	s.mu.Unlock()
	if h == nil {
		return supervisor.SessionInfo{}, fmt.Errorf("workspace has no live session")
	}
	env := r.Environment
	if r.Shell || env == nil {
		env, err = ShellEnvironment(ctx, r.Root)
		if err != nil {
			return supervisor.SessionInfo{}, err
		}
	}
	w := discovery.Discover(ctx, discovery.Options{Root: r.Root, RootExplicit: true, WorkspaceFile: h.workspace, Environment: env})
	if w.Invalid() {
		return supervisor.SessionInfo{}, fmt.Errorf("workspace validation failed")
	}
	if err = ctx.Err(); err != nil {
		return supervisor.SessionInfo{}, err
	}
	s.observationMu.Lock()
	err = h.host.ReloadEnvironment(w, env)
	s.observationMu.Unlock()
	if err != nil {
		return supervisor.SessionInfo{}, err
	}
	h.environment = append([]string{}, env...)
	s.wakeObservation()
	return h.host.SessionInfo(), nil
}

type localSource struct {
	s         *Server
	namespace string
}

func (l localSource) ListSessions(ctx context.Context) ([]supervisor.SessionInfo, error) {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	out := []supervisor.SessionInfo{}
	for _, h := range l.s.hosts {
		v := h.host.SessionInfo()
		if v.CacheDir == l.namespace {
			out = append(out, v)
		}
	}
	return out, nil
}

type localSnapshot struct{ c *control.Coordinator }

func (l localSnapshot) Snapshot(ctx context.Context) (control.Snapshot, error) {
	return l.c.Snapshot(ctx)
}
func (l localSnapshot) Close() error { return nil }
func (l localSource) Connect(ctx context.Context, info supervisor.SessionInfo) (inventory.SnapshotClient, error) {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	for _, h := range l.s.hosts {
		if h.host.SessionInfo().SessionID == info.SessionID {
			return localSnapshot{h.host.Coordinator()}, nil
		}
	}
	return nil, fmt.Errorf("session unavailable")
}
