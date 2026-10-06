// Package sessionhost owns the foreground session and its single write authority.
package sessionhost

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/process"
	"github.com/szhjia/stackharbor/internal/runner"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

type Host struct {
	session         *supervisor.Session
	backend         *supervisor.ControlBackend
	coordinator     *control.Coordinator
	server          *sessionapi.Server
	controller      *Controller
	done            chan struct{}
	ready           chan struct{}
	mu              sync.Mutex
	result          error
	writeRecord     func(context.Context, string, control.Operation) error
	finalizeBackend func() error
}

func New(ctx context.Context, w model.Workspace) (*Host, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reader := process.NewReader()
	session, err := supervisor.New(w, runner.NewLocal(reader), observe.NewPortProbe(), observe.NewSampler(reader))
	if err != nil {
		return nil, err
	}
	info := session.SessionInfo()
	identity := control.Identity{WorkspaceID: info.WorkspaceID, SessionID: info.SessionID, ProtocolVersion: sessionapi.ProtocolVersion, Capabilities: []string{"control", "logs", "plans", "operations"}}
	h := &Host{session: session, done: make(chan struct{}), ready: make(chan struct{}), writeRecord: func(ctx context.Context, cache string, op control.Operation) error {
		return writeBoundCompletion(ctx, cache, info.WorkspaceID, op)
	}}
	h.backend = supervisor.NewControlBackend(session, identity)
	h.finalizeBackend = h.backend.Finalize
	h.coordinator = control.New(identity, h.backend, control.Options{FinalizeClose: h.finalize, OnClosed: func(op control.Operation) {
		h.mu.Lock()
		if op.Error != nil {
			h.result = op.Error
		}
		h.mu.Unlock()
		close(h.done)
	}})
	h.controller = &Controller{host: h}
	h.server, err = sessionapi.Listen(ctx, sessionapi.Registration{Info: info, PublishEndpoint: session.PublishEndpoint}, h.coordinator)
	close(h.ready)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return nil, errors.Join(err, h.Close(cleanup))
	}
	go func() {
		select {
		case <-ctx.Done():
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = h.Close(cleanup)
		case <-h.done:
		}
	}()
	return h, nil
}
func (h *Host) Controller() *Controller             { return h.controller }
func (h *Host) Coordinator() *control.Coordinator   { return h.coordinator }
func (h *Host) SessionInfo() supervisor.SessionInfo { return h.session.SessionInfo() }
func (h *Host) Done() <-chan struct{}               { return h.done }
func (h *Host) Close(ctx context.Context) error {
	if err := h.coordinator.Close(ctx); err != nil {
		return err
	}
	select {
	case <-h.done:
		return h.Result()
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (h *Host) Result() error {
	select {
	case <-h.done:
	default:
		return &control.APIError{Code: "result_unknown", Message: "Session has not finished"}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.result
}
func (h *Host) finalize(ctx context.Context, op control.Operation) error {
	<-h.ready
	cache := h.session.SessionInfo().CacheDir
	pending := op
	pending.State = "finalizing"
	// A finalizing record must precede releasing ownership. It cannot be mistaken
	// for success if the process dies or any later step fails.
	err := h.writeRecord(ctx, cache, pending)
	if h.server != nil {
		err = errors.Join(err, h.server.Close(ctx))
	}
	err = errors.Join(err, h.finalizeBackend(), ctx.Err())
	if err != nil {
		op.State = "failed"
		op.Error = &control.APIError{Code: "close_finalization_failed", Message: err.Error()}
		for i := range op.Results {
			op.Results[i].State = "failed"
			op.Results[i].Error = op.Error
		}
	}
	op.UpdatedAt = time.Now().UTC()
	return errors.Join(err, h.writeRecord(ctx, cache, op))
}
