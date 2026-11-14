package runner

import (
	"context"
	"errors"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
)

// ErrRecoveryRequired means bounded process cleanup could not prove that the
// managed launch group is empty. Its durable intent must remain recoverable.
var ErrRecoveryRequired = errors.New("recovery_required")

type ExitResult struct {
	Code int
	Err  error
}
type StopResult struct {
	Complete  bool
	Remaining []model.ProcessIdentity
	Errors    []string
}
type Handle interface {
	Identities() []model.ProcessIdentity
	Done() <-chan ExitResult
	Stop(context.Context, model.StopPolicy) StopResult
}
type Runner interface {
	Start(context.Context, model.Service, func(string, string)) (Handle, error)
}
type Journal interface {
	Intent(model.Service) (string, error)
	Launched(string, model.ProcessIdentity) error
	Updated(string, []model.ProcessIdentity) error
	Finished(string, bool) error
}

type local struct {
	reader  process.Reader
	journal Journal
	managed bool
}

// NewManaged gates command execution until launch intent and process identity
// have both been persisted. The reader may share a bounded ownership cache.
func NewManaged(reader process.Reader, journal Journal) Runner {
	return &local{reader: reader, journal: journal, managed: true}
}

func NewLocal(reader process.Reader) Runner { return &local{reader: reader} }
