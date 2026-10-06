package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/szhjia/stackharbor/internal/config"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/inventory"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

func controlCommand(command string) bool {
	switch command {
	case "status", "start", "stop", "restart", "release", "kill", "logs", "operations":
		return true
	}
	return false
}

// extractCommand preserves flags before a command without treating their values
// as command names. The remaining arguments are still validated by FlagSet.
func extractCommand(args []string) (string, []string) {
	value := false
	for i, a := range args {
		if value {
			value = false
			continue
		}
		name, _, equal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if strings.HasPrefix(a, "-") {
			if !equal {
				switch name {
				case "root", "workspace", "target", "focus", "project", "port", "timeout", "id", "after", "limit":
					value = true
				}
			}
			continue
		}
		return a, append(append([]string{}, args[:i]...), args[i+1:]...)
	}
	return "run", args
}
func resolveControlRoot(root, workspace string) (string, error) {
	explicit := root != ""
	if root == "" {
		var e error
		root, e = os.Getwd()
		if e != nil {
			return "", e
		}
	}
	real, e := filepath.Abs(root)
	if e == nil {
		real, e = filepath.EvalSymlinks(real)
	}
	if e != nil {
		return "", e
	}
	// An explicit root (or a directly reachable current-root owner) is sufficient
	// authority for selecting an existing host, even if registration changed.
	if workspace == "" {
		if explicit {
			return real, nil
		}
		if _, found, e := selectControlSession(real); e != nil {
			return "", e
		} else if found {
			return real, nil
		}
	}
	if workspace == "" {
		candidate := filepath.Join(real, "stackharbor.workspace.yaml")
		if _, e := os.Stat(candidate); e == nil {
			workspace = candidate
		}
	}
	if workspace != "" {
		file, e := filepath.Abs(workspace)
		if e != nil {
			return "", e
		}
		cfg, ds := config.LoadWorkspace(file)
		if len(ds) > 0 {
			return "", fmt.Errorf("Invalid workspace root configuration: %s", ds[0].Message)
		}
		if cfg.RootExplicit {
			target := cfg.Root
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(file), target)
			}
			target, e = filepath.EvalSymlinks(target)
			if e != nil {
				return "", e
			}
			if explicit && target != real {
				return "", fmt.Errorf("--root conflicts with workspace root")
			}
			real = target
		}
	}
	return real, nil
}
func selectControlSession(root string) (supervisor.SessionInfo, bool, error) {
	all, e := supervisor.ListSessions()
	if e != nil {
		return supervisor.SessionInfo{}, false, e
	}
	for _, s := range all {
		if s.Root == root {
			return s, true, nil
		}
	}
	return supervisor.SessionInfo{}, false, nil
}
func runControl(ctx context.Context, command string, args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(errOut)
	root := fs.String("root", "", "Workspace root")
	workspace := fs.String("workspace", "", "Workspace root configuration")
	target := fs.String("target", "", "Registered node ID")
	jsonMode := fs.Bool("json", false, "JSON output")
	dry := fs.Bool("dry-run", false, "Return plan only")
	yes := fs.Bool("yes", false, "Confirm calculated scope")
	timeout := fs.Duration("timeout", 10*time.Minute, "Client wait budget")
	all := fs.Bool("all", false, "All sessions")
	port := fs.Int("port", 0, "Declared port to release")
	follow := fs.Bool("follow", false, "Follow logs")
	after := fs.Uint64("after", 0, "Log sequence cursor")
	limit := fs.Int("limit", 500, "Log page size")
	id := fs.String("id", "", "Operation ID")
	help := fs.Bool("help", false, "Help")
	fs.BoolVar(help, "h", false, "Help")
	if e := fs.Parse(args); e != nil {
		return 2
	}
	if *help {
		fmt.Fprint(out, usage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errOut, "Unexpected extra arguments")
		return 2
	}
	mutation := command == "start" || command == "stop" || command == "restart" || command == "release" || command == "kill"
	allowed := map[string]bool{"root": true, "json": true, "help": true, "h": true}
	if mutation {
		for _, name := range []string{"workspace", "dry-run", "yes", "timeout"} {
			allowed[name] = true
		}
		if command != "kill" {
			allowed["target"] = true
		}
	}
	if command == "status" {
		allowed["all"] = true
	}
	if command == "logs" {
		for _, name := range []string{"target", "follow", "after", "limit"} {
			allowed[name] = true
		}
	}
	if command == "operations" {
		allowed["id"] = true
	}
	if command == "release" {
		allowed["port"] = true
	}
	valid := true
	fs.Visit(func(f *flag.Flag) {
		if !allowed[f.Name] {
			fmt.Fprintf(errOut, "--%s is not supported for %s\n", f.Name, command)
			valid = false
		}
	})
	if !valid {
		return 2
	}
	if command == "release" && (*port < 1 || *port > 65535) {
		fmt.Fprintln(errOut, "release requires --port between 1 and 65535")
		return 2
	}
	if *timeout <= 0 || *port < 0 || *port > 65535 || *limit < 1 || *limit > 500 {
		fmt.Fprintln(errOut, "Invalid timeout, port, or log limit")
		return 2
	}
	if *all && *root != "" {
		fmt.Fprintln(errOut, "status --all does not accept --root")
		return 2
	}
	if mutation && command != "kill" && strings.TrimSpace(*target) == "" {
		fmt.Fprintln(errOut, "--target is required")
		return 2
	}
	if mutation && !*dry && !*yes && (!terminal(in) || !terminal(out)) {
		fmt.Fprintln(errOut, "Noninteractive mutation requires --yes (or --dry-run)")
		return 2
	}
	if command == "status" && *all {
		inv, e := inventory.New(nil, inventory.Options{}).Collect(ctx)
		if e != nil {
			return controlFailure(errOut, e)
		}
		if e = writeInventory(out, inv, *jsonMode); e != nil {
			return controlFailure(errOut, e)
		}
		return 0
	}
	canonical, e := resolveControlRoot(*root, *workspace)
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 2
	}
	info, found, e := selectControlSession(canonical)
	if e != nil {
		return controlFailure(errOut, e)
	}
	if command == "operations" && *id != "" {
		if op, e := readCompletedOperation(canonical, *id); e == nil {
			return writeOperation(out, errOut, op, *jsonMode)
		}
	}
	if !found {
		if command == "kill" {
			if *jsonMode {
				return encodeControl(out, map[string]any{"root": canonical, "state": "no_session"})
			}
			fmt.Fprintf(out, "No active StackHarbor session for %q.\n", canonical)
			return 0
		}
		fmt.Fprintf(errOut, "No active StackHarbor session for %q.\n", canonical)
		if command == "operations" {
			return 3
		}
		return 1
	}
	interactionOut := out
	if *jsonMode {
		interactionOut = errOut
	}
	if command == "kill" && info.ProtocolVersion == 0 && info.SocketPath == "" {
		if *dry {
			return encodeControl(out, control.Plan{SessionID: info.SessionID, Action: "close", Warnings: []string{"Legacy session: identity-checked signal shutdown; no operation journal"}, Affected: []string{}, Targets: []string{}})
		}
		if !*yes && *jsonMode {
			preview := control.Plan{SessionID: info.SessionID, Action: "close", Targets: []string{}, Affected: []string{}, Warnings: []string{fmt.Sprintf("Close legacy workspace %q, PID %d via identity-checked signal; no operation journal", canonical, info.PID)}}
			if code := writePlan(interactionOut, errOut, preview); code != 0 {
				return code
			}
		}
		if !*yes && !confirmControl(in, interactionOut, "Close legacy session?") {
			fmt.Fprintln(errOut, "Confirmation declined")
			return 2
		}
		return runKill(ctx, canonical, *timeout, *jsonMode, out, errOut)
	}
	client, e := sessionapi.Connect(ctx, info)
	if e != nil {
		if command == "operations" && *id != "" {
			return operationQueryFailure(errOut, *id, e)
		}
		return controlFailure(errOut, e)
	}
	defer client.Close()
	switch command {
	case "status":
		snap, e := client.Snapshot(ctx)
		if e != nil {
			return controlFailure(errOut, e)
		}
		return writeSnapshot(out, errOut, snap, *jsonMode)
	case "logs":
		return runLogs(ctx, client, *target, *after, *limit, *follow, *jsonMode, out, errOut)
	case "operations":
		return runOperations(ctx, client, *id, *jsonMode, out, errOut)
	}
	action := command
	if command == "kill" {
		action = "close"
	}
	targets := []string{}
	if *target != "" {
		targets = append(targets, *target)
	}
	plan, e := client.Plan(ctx, control.PlanRequest{Action: action, Targets: targets, Port: *port})
	if e != nil {
		return controlFailure(errOut, e)
	}
	impact, e := collectControlImpact(ctx, info, plan)
	if e != nil {
		return controlFailure(errOut, e)
	}
	addImpactWarnings(&plan, impact)
	if *dry {
		if *jsonMode {
			return encodeControl(out, plan)
		}
		return writePlan(out, errOut, plan)
	}
	if e = checkImpact(impact); e != nil {
		return controlFailure(errOut, e)
	}
	if !*jsonMode || !*yes {
		if code := writePlan(interactionOut, errOut, plan); code != 0 {
			return code
		}
	}
	if !*yes && !confirmControl(in, interactionOut, "Execute this plan? [y/N]") {
		fmt.Fprintln(errOut, "Confirmation declined")
		return 2
	}
	// Confirmation may take time. Refresh cross-workspace consumers immediately
	// before writing; server plan expiry/fingerprint and resource locks still guard.
	impact, e = collectControlImpact(ctx, info, plan)
	if e != nil {
		return controlFailure(errOut, e)
	}
	if e = checkImpact(impact); e != nil {
		return controlFailure(errOut, e)
	}
	if impact.Partial {
		fmt.Fprintln(errOut, "Resource dependency coverage is partial; some sessions are unreachable or identities unknown.")
	}
	op, e := client.Submit(ctx, control.SubmitRequest{PlanID: plan.ID, IdempotencyKey: control.NewIdempotencyKey(plan.ID)})
	if e != nil {
		var api *control.APIError
		if errors.As(e, &api) && api.Code != "unavailable" {
			return controlFailure(errOut, e)
		}
		fmt.Fprintf(errOut, "Submission result unknown; plan %s. Do not reexecute without querying operations. %s\n", plan.ID, e)
		return 3
	}
	waited, known := waitOperation(ctx, client, info, op, *timeout)
	if !known {
		if code := writeOperationValue(out, waited, *jsonMode); code != 0 {
			return code
		}
		fmt.Fprintf(errOut, "Operation %s result is unresolved (%s); query operations --root %q --id %s.\n", waited.ID, waited.State, canonical, waited.ID)
		return 3
	}
	return writeOperation(out, errOut, waited, *jsonMode)
}
func confirmControl(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprintln(out, prompt)
	line, e := bufio.NewReader(in).ReadString('\n')
	return (e == nil || e == io.EOF) && (strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes"))
}
func collectControlImpact(ctx context.Context, info supervisor.SessionInfo, plan control.Plan) (inventory.Impact, error) {
	inv, e := inventory.New(nil, inventory.Options{}).Collect(ctx)
	return inventory.SharedImpact(inv, info.SessionID, plan.Action, plan.Affected), e
}
func addImpactWarnings(plan *control.Plan, impact inventory.Impact) {
	if impact.Partial {
		plan.Warnings = append(plan.Warnings, "Resource dependency coverage is partial")
	}
	if e := checkImpact(impact); e != nil {
		plan.Warnings = append(plan.Warnings, e.Error())
	}
	for _, r := range impact.Resources {
		for _, ref := range r.References {
			if ref.SessionID != plan.SessionID {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("Shared resource %s: workspace %s, session %s, nodes %v", r.ID, ref.WorkspaceID, ref.SessionID, ref.Nodes))
			}
		}
	}
}
func checkImpact(impact inventory.Impact) error {
	if len(impact.Blockers) == 0 {
		return nil
	}
	parts := []string{}
	for _, b := range impact.Blockers {
		parts = append(parts, fmt.Sprintf("%q (%s): %v", b.Root, b.WorkspaceID, b.NodeIDs))
	}
	return &control.APIError{Code: "shared_consumers", Message: "Stop active managed consumers in affected workspaces, then request a new plan: " + strings.Join(parts, "; ")}
}
func controlFailure(out io.Writer, e error) int {
	fmt.Fprintln(out, e)
	var api *control.APIError
	if errors.As(e, &api) {
		switch api.Code {
		case "unsupported_protocol", "invalid_request", "forbidden", "identity_conflict", "idempotency_conflict", "plan_expired", "plan_conflict", "shared_consumers":
			return 2
		case "result_unknown", "operation_expired":
			return 3
		}
	}
	return 1
}
