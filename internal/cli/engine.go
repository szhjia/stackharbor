package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/discovery"
	"github.com/szhjia/stackharbor/internal/engine"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"github.com/szhjia/stackharbor/internal/tui"
)

func runEngine(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	action, args := extractCommand(args)
	if action == "recover" {
		return runEngineRecovery(ctx, args, in, out, errOut)
	}
	fs := flag.NewFlagSet("engine "+action, flag.ContinueOnError)
	fs.SetOutput(errOut)
	stateDir := fs.String("state-dir", "", "Private engine state directory")
	runtimeDir := fs.String("runtime-dir", "", "Private socket directory")
	idle := fs.Duration("idle-timeout", 60*time.Second, "Idle shutdown interval")
	jsonMode := fs.Bool("json", false, "JSON output")
	help := fs.Bool("help", false, "Help")
	fs.BoolVar(help, "h", false, "Help")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *help {
		fmt.Fprintln(out, "Usage: stackharbor engine serve|ensure|status|stop [--json]\nserve: [--state-dir PATH] [--runtime-dir PATH] [--idle-timeout 60s]\nstop exits an idle engine; close individual workspaces with kill --root PATH --yes.\nRecover an unclean workspace: engine recover --root PATH --dry-run; review before --yes.\nAcknowledge separately after manual process review: --acknowledge --reviewed-unknown --yes.")
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errOut, "Unexpected engine arguments")
		return 2
	}
	if action != "serve" && action != "ensure" && action != "status" && action != "stop" {
		fmt.Fprintln(errOut, "engine requires serve, ensure, status or stop")
		return 2
	}
	valid := true
	fs.Visit(func(f *flag.Flag) {
		if action != "serve" && (f.Name == "state-dir" || f.Name == "runtime-dir" || f.Name == "idle-timeout") {
			fmt.Fprintf(errOut, "--%s is only supported for engine serve\n", f.Name)
			valid = false
		}
	})
	if !valid || *idle <= 0 {
		return 2
	}
	if action == "serve" {
		if err := engine.Serve(ctx, engine.Options{StateDir: *stateDir, RuntimeDir: *runtimeDir, IdleTimeout: *idle, Out: out}); err != nil {
			return controlFailure(errOut, err)
		}
		return 0
	}
	connect := engine.Connect
	if action == "ensure" {
		connect = engine.Ensure
	}
	client, err := connect(ctx)
	if err != nil {
		return controlFailure(errOut, err)
	}
	defer client.Close()
	if action == "stop" {
		if err := client.Do(ctx, "POST", "/engine/v1/shutdown", struct{}{}, nil); err != nil {
			return controlFailure(errOut, err)
		}
		if *jsonMode {
			return encodeControl(out, map[string]string{"state": "stopped"})
		}
		fmt.Fprintln(out, "Stopped the idle StackHarbor engine.")
		return 0
	}
	if action == "status" {
		var status struct {
			engine.Endpoint
			ActiveSessions    int `json:"active_sessions"`
			ActiveProcesses   int `json:"active_processes"`
			OperationsRunning int `json:"operations_running"`
		}
		if err := client.Do(ctx, "GET", "/engine/v1/identity", nil, &status); err != nil {
			return controlFailure(errOut, err)
		}
		if *jsonMode {
			return encodeControl(out, status)
		}
		fmt.Fprintf(out, "StackHarbor engine is available (PID %d, %d sessions, %d processes, %d operations).\n", status.PID, status.ActiveSessions, status.ActiveProcesses, status.OperationsRunning)
		return 0
	}
	if *jsonMode {
		if err := json.NewEncoder(out).Encode(client.Endpoint); err != nil {
			return controlFailure(errOut, err)
		}
		return 0
	}
	fmt.Fprintln(out, "StackHarbor engine is available.")
	return 0
}

func runEngineRecovery(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("engine recover", flag.ContinueOnError)
	fs.SetOutput(errOut)
	root := fs.String("root", "", "Workspace root requiring recovery")
	namespace := fs.String("namespace", os.Getenv("STACKHARBOR_CACHE_DIR"), "Registry namespace")
	dry := fs.Bool("dry-run", false, "Inspect recovery without signalling or acknowledging")
	yes := fs.Bool("yes", false, "Confirm the retained recovery scope")
	acknowledge := fs.Bool("acknowledge", false, "Acknowledge recovery after manual process review")
	reviewed := fs.Bool("reviewed-unknown", false, "Confirm manual review of unrecorded descendants")
	jsonMode := fs.Bool("json", false, "JSON output")
	timeout := fs.Duration("timeout", 30*time.Second, "Recovery request budget")
	help := fs.Bool("help", false, "Help")
	fs.BoolVar(help, "h", false, "Help")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *help {
		fmt.Fprintln(out, "Usage: stackharbor engine recover --root PATH [--namespace PATH] --dry-run [--json]\nStop only recorded, verified processes: --yes\nAfter reviewing unknown descendants, acknowledge separately: --acknowledge --reviewed-unknown --yes\nRecovery never restarts services.")
		return 0
	}
	if fs.NArg() != 0 || *root == "" || *timeout <= 0 {
		fmt.Fprintln(errOut, "engine recover requires --root PATH and a positive --timeout")
		return 2
	}
	if *reviewed && !*acknowledge {
		fmt.Fprintln(errOut, "--reviewed-unknown requires --acknowledge")
		return 2
	}
	if *acknowledge && !*reviewed && !*dry {
		fmt.Fprintln(errOut, "Acknowledgement requires --reviewed-unknown after manual process review")
		return 2
	}
	if !*dry && !*yes && (!terminal(in) || !terminal(out)) {
		fmt.Fprintln(errOut, "Noninteractive recovery requires --yes (or --dry-run)")
		return 2
	}
	canonical, err := resolveControlRoot(*root, "")
	if err != nil {
		return controlFailure(errOut, err)
	}
	if *namespace != "" {
		*namespace, err = filepath.Abs(*namespace)
		if err != nil {
			return controlFailure(errOut, err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	client, err := engine.Ensure(ctx)
	if err != nil {
		return controlFailure(errOut, err)
	}
	defer client.Close()
	query := url.Values{"root": {canonical}, "namespace": {*namespace}}
	var plan engine.RecoveryPlan
	if err = client.Do(ctx, "GET", "/engine/v1/recovery?"+query.Encode(), nil, &plan); err != nil {
		return controlFailure(errOut, err)
	}
	if plan.Root != canonical || plan.PlanID == "" {
		fmt.Fprintln(errOut, "Recovery plan identity changed; no action taken")
		return 2
	}
	interactionOut := out
	if *jsonMode && !*dry {
		interactionOut = errOut
	}
	if *dry && *jsonMode {
		return encodeControl(out, plan)
	}
	if !*jsonMode || !*yes || *dry {
		fmt.Fprintf(interactionOut, "Recovery plan %s for %q\n", plan.PlanID, plan.Root)
		for _, warning := range plan.Warnings {
			fmt.Fprintln(interactionOut, warning)
		}
		for _, process := range plan.Processes {
			fmt.Fprintf(interactionOut, "PID %d · created %d · %s\n", process.Identity.PID, process.Identity.CreatedMillis, process.State)
		}
	}
	if *dry {
		return 0
	}
	action := "stop"
	if *acknowledge {
		action = "acknowledge"
	}
	if !*yes && !confirmControl(in, interactionOut, "Execute this recovery "+action+" plan? [y/N]") {
		fmt.Fprintln(errOut, "Confirmation declined")
		return 2
	}
	request := struct {
		Root            string `json:"root"`
		Namespace       string `json:"namespace"`
		Action          string `json:"action"`
		PlanID          string `json:"plan_id"`
		ReviewedUnknown bool   `json:"reviewed_unknown"`
	}{plan.Root, plan.Namespace, action, plan.PlanID, *reviewed}
	var result json.RawMessage
	if err = client.Do(ctx, "POST", "/engine/v1/recovery", request, &result); err != nil {
		var api *control.APIError
		if errors.As(err, &api) {
			return controlFailure(errOut, err)
		}
		fmt.Fprintf(errOut, "Recovery %s result unresolved; inspect engine recover --root %q --dry-run before retrying: %s\n", action, canonical, err)
		return 3
	}
	if *jsonMode {
		return encodeControl(out, result)
	}
	if *acknowledge {
		fmt.Fprintln(out, "Recovery acknowledged. Open the workspace explicitly when ready.")
	} else {
		fmt.Fprintln(out, "Recorded processes stopped. Review unknown descendants, then acknowledge recovery separately; services remain stopped.")
	}
	return 0
}

func openManagedSession(ctx context.Context, root, workspace string) (supervisor.SessionInfo, error) {
	canonical, err := resolveControlRoot(root, workspace)
	if err != nil {
		return supervisor.SessionInfo{}, err
	}
	if workspace != "" {
		workspace, err = filepath.Abs(workspace)
		if err != nil {
			return supervisor.SessionInfo{}, err
		}
	}
	namespace := os.Getenv("STACKHARBOR_CACHE_DIR")
	if namespace != "" {
		namespace, err = filepath.Abs(namespace)
		if err != nil {
			return supervisor.SessionInfo{}, err
		}
	}
	client, err := engine.Ensure(ctx)
	if err != nil {
		return supervisor.SessionInfo{}, err
	}
	defer client.Close()
	return client.Open(ctx, engine.OpenRequest{Root: canonical, Namespace: namespace, Workspace: workspace, Environment: os.Environ()})
}

func runManaged(ctx context.Context, root, workspace string, in io.Reader, out, errOut io.Writer) int {
	info, err := openManagedSession(ctx, root, workspace)
	if err != nil {
		return controlFailure(errOut, err)
	}
	return attachSession(ctx, info, workspace, in, out, errOut)
}

func runManagedTask(ctx context.Context, root, workspace, target string, jsonMode bool, in io.Reader, out, errOut io.Writer) int {
	info, err := openManagedSession(ctx, root, workspace)
	if err != nil {
		return controlFailure(errOut, err)
	}
	client, err := sessionapi.Connect(ctx, info)
	if err != nil {
		return controlFailure(errOut, err)
	}
	snapshot, err := client.Snapshot(ctx)
	_ = client.Close()
	if err != nil {
		return controlFailure(errOut, err)
	}
	matched := false
	for _, node := range snapshot.Nodes {
		if node.ID == target && node.Kind == "task" {
			matched = true
			break
		}
	}
	if !matched {
		fmt.Fprintln(errOut, "Target is not a registered task")
		return 2
	}
	// "task run TASK" is explicit authorization for this task. Dependency and
	// outcome handling still use the normal retained control plan. This command
	// does not authorize implicit termination of external port listeners;
	// this client never owns or closes the task's workspace dependencies.
	args := []string{"--root", info.Root, "--target", target, "--yes"}
	if jsonMode {
		args = append(args, "--json")
	}
	return runControlWithInteraction(ctx, "start", args, in, out, errOut, controlInteraction{expected: &info, noImplicitRelease: true})
}

func attachSession(ctx context.Context, info supervisor.SessionInfo, workspace string, in io.Reader, out, errOut io.Writer) int {
	if !terminal(in) || !terminal(out) {
		fmt.Fprintln(errOut, "Interactive mode requires a terminal; use stackharbor status or logs.")
		return 2
	}
	w := discovery.Discover(ctx, discovery.Options{Root: info.Root, WorkspaceFile: workspace, RootExplicit: true})
	// Registration may have changed; only the host's snapshot controls the live
	// session. Local discovery supplies descriptions for the remote read model.
	w.Root = info.Root
	remote, err := sessionhost.NewRemote(ctx, w, info)
	if err != nil {
		return controlFailure(errOut, err)
	}
	defer remote.Shutdown(context.Background())
	if err = tui.Run(ctx, remote, in, out); err != nil {
		return controlFailure(errOut, err)
	}
	return 0
}
