package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"io"
	"time"
)

// runKill preserves only the legacy, identity-checked signal path. Modern hosts
// always close through a retained plan/operation and durable completion journal.
func runKill(ctx context.Context, root string, timeout time.Duration, jsonMode bool, out, errOut io.Writer) int {
	if !jsonMode {
		fmt.Fprintf(out, "Closing legacy StackHarbor session for %q...\n", root)
	}
	budget, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	info, stopped, e := supervisor.TerminateSession(budget, root)
	if e != nil {
		fmt.Fprintln(errOut, e)
		if errors.Is(e, context.DeadlineExceeded) || errors.Is(e, context.Canceled) {
			fmt.Fprintln(errOut, "Legacy shutdown result is unresolved; no operation ID is available. Check sessions before retrying.")
			return 3
		}
		return 1
	}
	state := "no_session"
	if stopped {
		state = "closed"
	}
	if jsonMode {
		return encodeControl(out, map[string]any{"root": root, "pid": info.PID, "state": state, "legacy": true})
	}
	if !stopped {
		fmt.Fprintf(out, "No active StackHarbor session for %q.\n", root)
	} else {
		fmt.Fprintf(out, "Closed legacy session PID %d for %q.\n", info.PID, root)
	}
	return 0
}
