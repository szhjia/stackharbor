package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/charmbracelet/x/term"
	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/discovery"
	"github.com/szhjia/stackharbor/internal/graph"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"github.com/szhjia/stackharbor/internal/tui"
	"io"
	"path/filepath"
	"strings"
	"time"
)

const usage = `StackHarbor — terminal console for monorepos
Usage: stackharbor [run|discover|validate|init|plan|doctor|task|history|sessions|status|start|stop|restart|release|logs|operations|kill|web] [options]
  web [--port PORT] [--no-open]           Loopback gateway (16800; 0 chooses a free port)
  web --dev-origin http://127.0.0.1:5173  Explicit Vite proxy development mode
  --root PATH          Project root (default: current directory)
  --workspace FILE     Explicit workspace YAML
  --json               Machine-readable discover/validate/plan/sessions output
  plan start|stop|restart --target NODE    Read-only dependency plan
  doctor --target TASK                    Run read-only checks
  task run TASK                          Run an explicit task and its dependencies
  history                                Read redacted action and task history (JSON)
  sessions [--json]                       List active workspace sessions
  sessions --focus PID                    Locate an existing macOS Terminal tab
  status [--all] [--json]                 Query existing sessions
  start|stop|restart --target NODE        Control an existing workspace session
  release --target NODE --port PORT      Release declared conflicts without starting
  logs --target NODE [--follow]           Read redacted session logs
  operations [--id ID] [--json]           Query operation results
  --dry-run / --yes / --timeout 10m       Mutation plan / confirmation / wait budget
  kill                                   Close existing session (noninteractive: --yes)
  --version            Show version
  init --dry-run       Preview candidate registration drafts
  init --write         Write unambiguous drafts without overwriting files
  --project PATH       Limit init to the selected candidate directory
Services do not start automatically. Interactive mode: s start, x stop, ? help, q quit.
`

func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	commandName, controlArgs := extractCommand(args)
	if commandName == "web" {
		return runWeb(ctx, controlArgs, out, errOut)
	}
	if controlCommand(commandName) {
		return runControl(ctx, commandName, controlArgs, in, out, errOut)
	}
	fs := flag.NewFlagSet("stackharbor", flag.ContinueOnError)
	fs.SetOutput(errOut)
	root := fs.String("root", "", "root")
	workspace := fs.String("workspace", "", "workspace")
	jsonMode := fs.Bool("json", false, "JSON")
	version := fs.Bool("version", false, "version")
	focus := fs.Int("focus", 0, "session PID to focus")
	help := fs.Bool("help", false, "help")
	fs.BoolVar(help, "h", false, "help")
	dry := fs.Bool("dry-run", false, "dry run")
	write := fs.Bool("write", false, "write")
	target := fs.String("target", "", "node target")
	planAction := "start"
	if len(args) >= 3 && args[0] == "task" && args[1] == "run" && !strings.HasPrefix(args[2], "-") {
		args = append([]string{"task", "--target", args[2]}, args[3:]...)
	} else if len(args) > 0 && args[0] == "task" {
		fmt.Fprintln(errOut, "Usage: stackharbor task run <task-id> [--root/--workspace]")
		return 2
	}
	if len(args) > 0 && args[0] == "plan" {
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			planAction = args[1]
			args = append([]string{args[0]}, args[2:]...)
		}
	}
	project := fs.String("project", "", "project")
	command := "run"
	filtered := []string{}
	valueFlag, foundCommand := false, false
	for _, a := range args {
		if valueFlag {
			filtered = append(filtered, a)
			valueFlag = false
			continue
		}
		if a == "--focus" || a == "-focus" || a == "--target" || a == "--root" || a == "--workspace" || a == "--project" || a == "-root" || a == "-workspace" || a == "-project" {
			filtered = append(filtered, a)
			valueFlag = true
			continue
		}
		if !strings.HasPrefix(a, "-") && !foundCommand {
			command = a
			foundCommand = true
			continue
		}
		filtered = append(filtered, a)
	}
	if e := fs.Parse(filtered); e != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errOut, "Unexpected extra arguments")
		return 2
	}
	if *version {
		fmt.Fprintln(out, "StackHarbor", buildinfo.Version)
		return 0
	}
	if *help {
		fmt.Fprint(out, usage)
		return 0
	}
	if command != "sessions" && command != "task" && command != "history" && command != "plan" && command != "doctor" && command != "run" && command != "discover" && command != "validate" && command != "init" {
		fmt.Fprintln(errOut, "Unknown command:", command)
		return 2
	}
	focusSet := false
	fs.Visit(func(f *flag.Flag) { focusSet = focusSet || f.Name == "focus" })
	if focusSet && (command != "sessions" || *focus <= 0 || *jsonMode) {
		fmt.Fprintln(errOut, "Use sessions --focus <positive PID> without --json")
		return 2
	}
	if command != "init" && (*dry || *write || *project != "") {
		fmt.Fprintln(errOut, "init options are only valid for init")
		return 2
	}
	if command == "run" && *jsonMode {
		fmt.Fprintln(errOut, "Use discover --json or validate --json")
		return 2
	}
	if command == "init" && (*dry == *write || *jsonMode) {
		fmt.Fprintln(errOut, "init requires --dry-run or --write")
		return 2
	}
	if command == "sessions" {
		if *workspace != "" || *target != "" {
			fmt.Fprintln(errOut, "sessions accepts --root, --json or --focus PID")
			return 2
		}
		return runSessions(ctx, *root, *jsonMode, *focus, out, errOut)
	}
	if command == "run" && (!terminal(in) || !terminal(out)) {
		fmt.Fprintln(errOut, "Interactive mode requires a terminal; use stackharbor discover or validate.")
		return 2
	}
	w := discovery.Discover(ctx, discovery.Options{Root: *root, WorkspaceFile: *workspace, RootExplicit: *root != ""})
	_, ds := graph.Build(w.Services())
	w.Diagnostics = append(w.Diagnostics, ds...)
	if command == "history" {
		events, e := supervisor.ReadHistory(w.Root)
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 1
		}
		if e = json.NewEncoder(out).Encode(events); e != nil {
			return 1
		}
		return 0
	}
	if command == "task" {
		if w.Invalid() {
			_ = WriteDiscovery(out, w, *jsonMode)
			return 2
		}
		matched := false
		for _, n := range w.Services() {
			if string(n.ID) == *target && n.Kind == "task" {
				matched = true
			}
		}
		if !matched {
			fmt.Fprintln(errOut, "Target is not a registered task")
			return 2
		}
		host, e := sessionhost.New(ctx, w)
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 1
		}
		e = host.Controller().Start(ctx, []model.ServiceID{model.ServiceID(*target)})
		for _, v := range host.Controller().Snapshot().Services {
			if string(v.Spec.ID) == *target {
				_ = json.NewEncoder(out).Encode(map[string]any{"id": v.Spec.ID, "state": v.State, "reason": supervisor.Redact(v.Spec, v.Reason)})
			}
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		shutdown := host.Close(cleanup)
		if e != nil || shutdown != nil {
			fmt.Fprintln(errOut, errors.Join(e, shutdown))
			return 1
		}
		return 0
	}
	if command == "plan" {
		if w.Invalid() {
			_ = WriteDiscovery(out, w, *jsonMode)
			return 2
		}
		g, _ := graph.Build(w.Services())
		ids := []model.ServiceID{}
		if *target != "" {
			ids = append(ids, model.ServiceID(*target))
		} else {
			for _, n := range w.Services() {
				if n.Kind != "task" && n.Kind != "resource" && n.Control != "observe" {
					ids = append(ids, n.ID)
				}
			}
		}
		p, e := g.Plan(w, planAction, ids)
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if e = enc.Encode(p); e != nil {
			return 1
		}
		return 0
	}
	if command == "doctor" {
		if w.Invalid() {
			_ = WriteDiscovery(out, w, *jsonMode)
			return 2
		}
		return Doctor(ctx, w, *target, out, errOut)
	}
	if command == "discover" || command == "validate" {
		if e := WriteDiscovery(out, w, *jsonMode); e != nil {
			fmt.Fprintln(errOut, e)
			return 1
		}
		if w.Invalid() {
			return 2
		}
		return 0
	}
	if command == "init" {
		if w.Invalid() {
			_ = WriteDiscovery(out, w, false)
			return 2
		}
		if *project != "" {
			path, e := filepath.Abs(*project)
			if e == nil {
				path, e = filepath.EvalSymlinks(path)
			}
			cs := []model.Candidate{}
			for _, c := range w.Candidates {
				if c.Cwd == path {
					cs = append(cs, c)
				}
			}
			if e != nil || len(cs) == 0 {
				fmt.Fprintln(errOut, "Selected directory is not an unregistered project candidate")
				return 2
			}
			w.Candidates = cs
		}
		drafts := PlanInit(w)
		if *dry {
			for _, d := range drafts {
				fmt.Fprintf(out, "--- %s\n%s", d.Path, d.Content)
				if len(d.NeedsInput) > 0 {
					fmt.Fprintln(out, "Input required:", d.NeedsInput)
				}
			}
			return 0
		}
		w.Diagnostics = append(w.Diagnostics, WriteDrafts(w.Root, drafts)...)
		next := discovery.Discover(ctx, discovery.Options{Root: *root, WorkspaceFile: *workspace, RootExplicit: *root != ""})
		_, check := graph.Build(next.Services())
		w.Diagnostics = append(w.Diagnostics, next.Diagnostics...)
		w.Diagnostics = append(w.Diagnostics, check...)
		_ = WriteDiscovery(out, w, false)
		if w.Invalid() {
			return 2
		}
		return 0
	}
	if w.Invalid() {
		readonly := &readOnly{snapshot: model.Snapshot{Root: w.Root, Projects: w.Projects, Candidates: w.Candidates, Diagnostics: w.Diagnostics}, store: logs.NewStore()}
		if e := tui.Run(ctx, readonly, in, out); e != nil {
			fmt.Fprintln(errOut, e)
			return 1
		}
		return 2
	}
	host, e := sessionhost.New(ctx, w)
	if e != nil {
		var conflict *supervisor.SessionConflict
		if errors.As(e, &conflict) {
			return locateConflict(ctx, conflict, out, errOut)
		}
		fmt.Fprintln(errOut, e)
		return 1
	}
	if e = tui.Run(ctx, host.Controller(), in, out); e != nil {
		fmt.Fprintln(errOut, e)
		return 1
	}
	return 0
}
func terminal(v any) bool {
	if f, ok := v.(interface{ Fd() uintptr }); ok {
		return term.IsTerminal(f.Fd())
	}
	return false
}

type readOnly struct {
	snapshot model.Snapshot
	store    *logs.Store
}

func (r *readOnly) Snapshot() model.Snapshot { return r.snapshot }
func (r *readOnly) Logs() *logs.Store        { return r.store }
func (r *readOnly) Start(context.Context, []model.ServiceID) error {
	return errors.New("Configuration errors; start disabled")
}
func (r *readOnly) Stop(context.Context, []model.ServiceID) error {
	return errors.New("Configuration errors; session is read-only")
}
func (r *readOnly) Restart(context.Context, []model.ServiceID) error {
	return errors.New("Configuration errors; session is read-only")
}
func (r *readOnly) Affected([]model.ServiceID) []model.ServiceID { return nil }
func (r *readOnly) Shutdown(context.Context) error               { return nil }
