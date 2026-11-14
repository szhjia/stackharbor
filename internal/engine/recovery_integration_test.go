package engine

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
)

func TestRecoveryAcknowledgementSurvivesEngineRestart(t *testing.T) {
	seed, root, ns, _ := interruptedMutation(t, true)
	runtimeDir, stateDir := privateTemp(t), seed.state.directory
	t.Setenv("STACKHARBOR_ENGINE_RUNTIME_DIR", runtimeDir)
	t.Setenv("STACKHARBOR_ENGINE_STATE_DIR", stateDir)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := func() (*Client, func()) {
		t.Helper()
		life, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		var serveErr error
		go func() {
			serveErr = Serve(life, Options{RuntimeDir: runtimeDir, StateDir: stateDir, IdleTimeout: time.Hour})
			close(done)
		}()
		var once sync.Once
		shutdown := func() {
			once.Do(func() {
				stop()
				select {
				case <-done:
					if serveErr != nil {
						t.Error(serveErr)
					}
				case <-time.After(5 * time.Second):
					t.Error("engine shutdown timed out")
				}
			})
		}
		t.Cleanup(shutdown)
		for {
			if client, err := Connect(ctx); err == nil {
				t.Cleanup(func() { _ = client.Close() })
				return client, shutdown
			}
			select {
			case <-done:
				t.Fatalf("engine exited before publishing its endpoint: %v", serveErr)
			case <-ctx.Done():
				t.Fatal("engine startup timed out", ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

	client, stop := start()
	query := url.Values{"root": {root}, "namespace": {ns}}
	recoveryPath := "/engine/v1/recovery?" + query.Encode()
	var plan RecoveryPlan
	if err := client.Do(ctx, "GET", recoveryPath, nil, &plan); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Open(ctx, OpenRequest{Root: root, Namespace: ns, Environment: []string{"PATH=/usr/bin:/bin"}}); err == nil {
		t.Fatal("unacknowledged workspace opened")
	}
	var result map[string]bool
	if err := client.Do(ctx, "POST", "/engine/v1/recovery", recoveryRequest{Root: root, Namespace: ns, Action: "acknowledge", PlanID: plan.PlanID, ReviewedUnknown: true}, &result); err != nil || !result["recovered"] {
		t.Fatal("recovery acknowledgment failed", result, err)
	}
	// Restart before opening/closing any workspace: a clean close must not be
	// needed to make the acknowledgment durable.
	stop()
	client, _ = start()
	var api *control.APIError
	if err := client.Do(ctx, "GET", recoveryPath, nil, &plan); !errors.As(err, &api) || api.Code != "not_found" {
		t.Fatal("acknowledged recovery reappeared after restart", err)
	}
	info, err := client.Open(ctx, OpenRequest{Root: root, Namespace: ns, Environment: []string{"PATH=/usr/bin:/bin"}})
	if err != nil || info.SessionID == "" {
		t.Fatal("recovered workspace could not reopen", info, err)
	}
}
