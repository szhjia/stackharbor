package control

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// PlanState is backend-owned execution data. Data must be an immutable private
// value; it is never returned through the public protocol.
type PlanState struct {
	Fingerprint string
	Affected    []string
	Warnings    []string
	Data        any
}
type Backend interface {
	Snapshot(context.Context) (Snapshot, error)
	Logs(context.Context, string, uint64, int) (LogPage, error)
	PlanState(context.Context, PlanRequest) (PlanState, error)
	Execute(context.Context, PlanRequest, PlanState) ([]TargetResult, error)
}
type Options struct {
	Now   func() time.Time
	NewID func() string
	// OnClosed runs after the close result is stored, without the coordinator lock.
	// It is notification/finalization, never a request to recursively clean up.
	OnClosed func(Operation)
	// FinalizeClose persists the proposed close outcome and finalizes the backend.
	// The public close operation stays running until this hook returns.
	FinalizeClose func(context.Context, Operation) error
}
type storedPlan struct {
	public                   Plan
	request                  PlanRequest
	state                    PlanState
	acceptedKey, operationID string
}
type operationRecord struct {
	public Operation
	plan   storedPlan
	key    string
	cancel context.CancelFunc
}
type Coordinator struct {
	mu                 sync.Mutex
	identity           Identity
	backend            Backend
	opts               Options
	plans              map[string]storedPlan
	records            map[string]*operationRecord
	queue              []string
	terminals          []string
	wake               chan struct{}
	ctx                context.Context
	cancel             context.CancelFunc
	closing            bool
	closeID            string
	closeBudget        time.Duration
	closeContext       context.Context
	closeContextCancel context.CancelFunc
	closeError         *APIError
	closeDone          chan struct{}
}

func New(identity Identity, backend Backend, options Options) *Coordinator {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = randomID
	}
	identity.Capabilities = append([]string{}, identity.Capabilities...)
	ctx, cancel := context.WithCancel(context.Background())
	c := &Coordinator{identity: identity, backend: backend, opts: options, plans: map[string]storedPlan{}, records: map[string]*operationRecord{}, wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel, closeDone: make(chan struct{}), closeBudget: 30 * time.Second}
	go c.work()
	return c
}
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("control: random ID unavailable")
	}
	return fmt.Sprintf("%x", b)
}
func (c *Coordinator) Identity() Identity {
	v := c.identity
	v.Capabilities = append([]string{}, v.Capabilities...)
	return v
}
func (c *Coordinator) Snapshot(ctx context.Context) (Snapshot, error) { return c.backend.Snapshot(ctx) }
func (c *Coordinator) Logs(ctx context.Context, target string, after uint64, limit int) (LogPage, error) {
	return c.backend.Logs(ctx, target, after, limit)
}
func apiError(code, message string) *APIError { return &APIError{Code: code, Message: message} }
func copyPlan(p Plan) Plan {
	p.Targets = append([]string{}, p.Targets...)
	p.Affected = append([]string{}, p.Affected...)
	p.Warnings = append([]string{}, p.Warnings...)
	return p
}
func copyOperation(o Operation) Operation {
	o.Results = append([]TargetResult{}, o.Results...)
	if o.Error != nil {
		v := *o.Error
		o.Error = &v
	}
	for i := range o.Results {
		if o.Results[i].Error != nil {
			v := *o.Results[i].Error
			o.Results[i].Error = &v
		}
	}
	return o
}
func (c *Coordinator) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
