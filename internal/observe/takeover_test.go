package observe

import (
	"context"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"testing"
)

type takeoverProbe struct{ status, command string }

func (p takeoverProbe) Observe(context.Context, []model.Port, []model.ProcessIdentity) []model.PortObservation {
	return []model.PortObservation{{Port: 8100, Status: p.status, Listeners: []model.Listener{{PID: 999999, Command: p.command}}}}
}
func (p takeoverProbe) CheckStart(context.Context, []model.Port) error { return nil }

type takeoverReader struct{}

func (takeoverReader) Read(context.Context, int32) (process.Info, error) {
	return process.Info{Identity: model.ProcessIdentity{PID: 999999, CreatedMillis: 200}}, nil
}
func (takeoverReader) List(context.Context) ([]process.Info, error) { return nil, nil }
func TestTakeoverRejectsPIDReuseAndUnknown(t *testing.T) {
	ctx := context.Background()
	probe := takeoverProbe{status: "external", command: "node"}
	err := ReleaseConflicts(ctx, probe, takeoverReader{}, []model.PortConflict{{Port: 8100, Identity: model.ProcessIdentity{PID: 999999, CreatedMillis: 100}}})
	if err == nil {
		t.Fatal("PID reuse accepted")
	}
	probe.status = "unknown"
	if _, err := FindConflicts(ctx, probe, takeoverReader{}, []model.Port{{Number: 8100}}, nil); err == nil {
		t.Fatal("unknown accepted")
	}
	probe.status = "external"
	probe.command = "com.docker"
	if _, err := FindConflicts(ctx, probe, takeoverReader{}, []model.Port{{Number: 8100}}, nil); err == nil {
		t.Fatal("Docker forwarder accepted")
	}
}
