// Package inventory collects session DTOs and projects distinct physical resources.
package inventory

import (
	"context"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"sync"
	"time"
)

type SnapshotClient interface {
	Snapshot(context.Context) (control.Snapshot, error)
	Close() error
}
type Source interface {
	ListSessions(context.Context) ([]supervisor.SessionInfo, error)
	Connect(context.Context, supervisor.SessionInfo) (SnapshotClient, error)
}
type ProductionSource struct{}

func (ProductionSource) ListSessions(ctx context.Context) ([]supervisor.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return supervisor.ListSessions()
}
func (ProductionSource) Connect(ctx context.Context, i supervisor.SessionInfo) (SnapshotClient, error) {
	return sessionapi.Connect(ctx, i)
}

type Options struct {
	Concurrency    int
	SessionTimeout time.Duration
}
type Collector struct {
	source  Source
	options Options
}

func New(source Source, options Options) *Collector {
	if source == nil {
		source = ProductionSource{}
	}
	if options.Concurrency <= 0 || options.Concurrency > 8 {
		options.Concurrency = 8
	}
	if options.SessionTimeout <= 0 || options.SessionTimeout > 2*time.Second {
		options.SessionTimeout = 2 * time.Second
	}
	return &Collector{source, options}
}

// Session contains presentation metadata only; transport paths and namespaces are internal.
type Session struct {
	Identity  control.Identity  `json:"identity"`
	Root      string            `json:"root"`
	PID       int               `json:"pid"`
	TTY       string            `json:"tty,omitempty"`
	Terminal  string            `json:"terminal,omitempty"`
	StartedAt time.Time         `json:"started_at"`
	Available bool              `json:"available"`
	Stale     bool              `json:"stale"`
	LastSeen  *time.Time        `json:"last_seen"`
	Snapshot  *control.Snapshot `json:"snapshot,omitempty"`
	Error     *control.APIError `json:"error,omitempty"`
}
type Inventory struct {
	Sessions        []Session         `json:"sessions"`
	Resources       []Resource        `json:"resources"`
	Totals          Totals            `json:"totals"`
	Partial         bool              `json:"partial"`
	CollectionError *control.APIError `json:"collection_error,omitempty"`
	CollectedAt     time.Time         `json:"collected_at"`
}

func (c *Collector) Collect(ctx context.Context) (Inventory, error) {
	out := Inventory{Sessions: []Session{}, Resources: []Resource{}, CollectedAt: time.Now().UTC()}
	infos, err := c.source.ListSessions(ctx)
	if err != nil {
		return out, err
	}
	out.Sessions = make([]Session, len(infos))
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := c.options.Concurrency
	if workers > len(infos) {
		workers = len(infos)
	}
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				info := infos[index]
				row := Session{Identity: control.Identity{WorkspaceID: info.WorkspaceID, SessionID: info.SessionID, ProtocolVersion: info.ProtocolVersion, Capabilities: append([]string{}, info.Capabilities...)}, Root: info.Root, PID: info.PID, TTY: info.TTY, Terminal: info.Terminal, StartedAt: info.StartedAt}
				request, cancel := context.WithTimeout(ctx, c.options.SessionTimeout)
				client, err := c.source.Connect(request, info)
				if err == nil {
					var snap control.Snapshot
					snap, err = client.Snapshot(request)
					_ = client.Close()
					if err == nil && (snap.Identity.SessionID != info.SessionID || info.WorkspaceID != "" && snap.Identity.WorkspaceID != info.WorkspaceID) {
						err = &control.APIError{Code: "identity_conflict", Message: "Session identity changed"}
					}
					if err == nil {
						now := time.Now().UTC()
						row.Identity = snap.Identity
						row.Available = true
						row.LastSeen = &now
						row.Snapshot = &snap
					}
				}
				cancel()
				if err != nil {
					code := "unavailable"
					if e, ok := err.(*control.APIError); ok {
						code = e.Code
					}
					row.Error = &control.APIError{Code: code, Message: "Session snapshot unavailable"}
				}
				out.Sessions[index] = row
			}
		}()
	}
	for index := range infos {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	return Project(out), nil
}
