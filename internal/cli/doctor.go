package cli

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"github.com/szhjia/stackharbor/internal/runner"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"io"
	"strings"
	"sync"
	"time"
)

func Doctor(ctx context.Context, w model.Workspace, target string, out, errOut io.Writer) int {
	matched := false
	for _, n := range w.Services() {
		if n.Task == nil || target != "" && string(n.ID) != target {
			continue
		}
		args := n.Command
		if n.Task.Policy == "when-needed" {
			args = n.Task.Check.Command
		} else if n.Task.Effect != "read-only" {
			continue
		}
		matched = true
		timeout := time.Duration(n.Task.TimeoutSeconds) * time.Second
		if timeout == 0 {
			timeout = 120 * time.Second
		}
		c, cancel := context.WithTimeout(ctx, timeout)
		n.Command = args
		var mu sync.Mutex
		var stdout strings.Builder
		h, err := runner.NewLocal(process.NewReader()).Start(c, n, func(stream, line string) {
			mu.Lock()
			defer mu.Unlock()
			line = supervisor.Redact(n, line)
			if stream == "stdout" {
				if stdout.Len() < 65536 {
					stdout.WriteString(line)
					stdout.WriteByte('\n')
				}
			} else {
				fmt.Fprintln(errOut, line)
			}
		})
		if err != nil {
			cancel()
			fmt.Fprintln(errOut, "Unable to start check command")
			return 1
		}
		select {
		case r := <-h.Done():
			cancel()
			text := stdout.String()
			fmt.Fprint(out, text)
			if n.Task.Policy == "when-needed" {
				if e := supervisor.ValidateTaskStatus(r.Code, text, n.Task.Check.RequiredScope); e != nil {
					fmt.Fprintln(errOut, e)
					return 1
				}
			}
			if r.Code != 0 || r.Err != nil {
				return 1
			}
		case <-c.Done():
			cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
			h.Stop(cleanup, n.Stop)
			stop()
			cancel()
			return 1
		}
	}
	if !matched {
		fmt.Fprintln(errOut, "No matching read-only check task")
		return 2
	}
	return 0
}
