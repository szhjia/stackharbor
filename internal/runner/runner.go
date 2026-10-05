package runner

import (
	"context"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
)

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
type local struct{ reader process.Reader }

func NewLocal(reader process.Reader) Runner { return &local{reader} }
