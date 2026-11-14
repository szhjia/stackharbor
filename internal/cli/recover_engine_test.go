package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/engine"
	"github.com/szhjia/stackharbor/internal/process"
)

func TestRecoveryCLIStopsAndAcknowledgesOnlySeparateRetainedPlans(t *testing.T) {
	runtimeDir, err := os.MkdirTemp("/tmp", "sh-recovery-cli-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runtimeDir)
	runtimeDir, err = filepath.EvalSymlinks(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STACKHARBOR_ENGINE_RUNTIME_DIR", runtimeDir)
	t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(runtimeDir, "namespace"))
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	birth, err := process.CreatedMillis(context.Background(), int32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	endpoint := engine.Endpoint{SocketPath: filepath.Join(runtimeDir, "engine.sock"), PID: os.Getpid(), ProcessCreatedMillis: birth, ProtocolVersion: engine.ProtocolVersion, BuildVersion: buildinfo.Version}
	listener, err := net.Listen("unix", endpoint.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = os.Chmod(endpoint.SocketPath, 0600); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(endpoint)
	if err = os.WriteFile(filepath.Join(runtimeDir, "engine.json"), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	posts := []string{}
	planID := "review-before-stop"
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var data any
		switch {
		case r.URL.Path == "/engine/v1/identity":
			data = endpoint
		case r.URL.Path == "/engine/v1/recovery" && r.Method == "GET":
			data = engine.RecoveryPlan{Root: root, Namespace: os.Getenv("STACKHARBOR_CACHE_DIR"), PlanID: planID, Warnings: []string{"Review descendants manually"}}
		case r.URL.Path == "/engine/v1/recovery" && r.Method == "POST":
			var request struct {
				Root, Namespace, Action string
				PlanID                  string `json:"plan_id"`
				ReviewedUnknown         bool   `json:"reviewed_unknown"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.PlanID != planID || request.Root != root {
				t.Errorf("request lost retained plan: %+v %v", request, err)
				w.WriteHeader(400)
				return
			}
			posts = append(posts, request.Action)
			if request.Action == "stop" {
				planID = "review-after-stop"
				data = engine.RecoveryPlan{Root: root, Namespace: request.Namespace, PlanID: planID}
			} else if request.Action == "acknowledge" && request.ReviewedUnknown {
				data = map[string]bool{"recovered": true}
			} else {
				t.Errorf("unexpected recovery request: %+v", request)
				w.WriteHeader(400)
				return
			}
		default:
			t.Errorf("unexpected engine route: %s %s", r.Method, r.URL)
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})}
	defer server.Close()
	go server.Serve(listener)
	if code, out, errs := managedCLIRun(t, "engine", "recover", "--root", root, "--dry-run", "--json"); code != 0 || !strings.Contains(out, "review-before-stop") {
		t.Fatal(code, out, errs)
	}
	mu.Lock()
	count := len(posts)
	mu.Unlock()
	if count != 0 {
		t.Fatal("recovery preview submitted a mutation")
	}
	if code, out, errs := managedCLIRun(t, "engine", "recover", "--root", root, "--yes", "--json"); code != 0 || !strings.Contains(out, "review-after-stop") {
		t.Fatal(code, out, errs)
	}
	mu.Lock()
	count = len(posts)
	first := posts[0]
	mu.Unlock()
	if count != 1 || first != "stop" {
		t.Fatal("stop implicitly acknowledged recovery", posts)
	}
	if code, _, _ := managedCLIRun(t, "engine", "recover", "--root", root, "--acknowledge", "--yes"); code != 2 {
		t.Fatal("acknowledgement skipped manual review", code)
	}
	if code, out, errs := managedCLIRun(t, "engine", "recover", "--root", root, "--acknowledge", "--reviewed-unknown", "--yes", "--json"); code != 0 || !strings.Contains(out, `"recovered":true`) {
		t.Fatal(code, out, errs)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 2 || posts[1] != "acknowledge" {
		t.Fatal("recovery requests", posts)
	}
}
