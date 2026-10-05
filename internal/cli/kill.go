package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/szhjia/stackharbor/internal/supervisor"
)

func runKill(ctx context.Context, root string, out, errOut io.Writer) int {
	fmt.Fprintf(out, "Closing StackHarbor session for %q...\n", root)
	budget, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	info, stopped, err := supervisor.TerminateSession(budget, root)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if !stopped {
		fmt.Fprintf(out, "No active StackHarbor session for %q.\n", root)
		return 0
	}
	fmt.Fprintf(out, "Closed session PID %d for %q.\n", info.PID, root)
	return 0
}
