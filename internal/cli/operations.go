package cli

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"io"
	"os"
	"path/filepath"
	"time"
)

func operationTerminal(state string) bool {
	switch state {
	case "succeeded", "failed", "partial", "canceled":
		return true
	}
	return false
}
func waitOperation(ctx context.Context, c *sessionapi.Client, info supervisor.SessionInfo, op control.Operation, timeout time.Duration) (control.Operation, bool) {
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if operationTerminal(op.State) {
			return op, true
		}
		select {
		case <-wait.Done():
			return op, false
		case <-ticker.C:
		}
		next, e := c.Operation(wait, op.ID)
		if e == nil {
			op = next
			continue
		}
		if op.Action == "close" {
			if record, e := sessionhost.ReadCompletion(info.CacheDir, info.SessionID); e == nil && record.ID == op.ID {
				return record, true
			}
		}
		// Transport loss and nonterminal/missing journals remain unresolved. Keep
		// polling within the client budget; do not infer success from disappearance.
	}
}
func readCompletedOperation(root, id string) (control.Operation, error) {
	cache := os.Getenv("STACKHARBOR_CACHE_DIR")
	if cache == "" {
		base, e := os.UserCacheDir()
		if e != nil {
			return control.Operation{}, e
		}
		cache = filepath.Join(base, "stackharbor")
	}
	cache, e := filepath.Abs(cache)
	if e == nil {
		cache, e = filepath.EvalSymlinks(cache)
	}
	if e != nil {
		return control.Operation{}, e
	}
	return sessionhost.ReadOperationCompletion(cache, root, id)
}
func runOperations(ctx context.Context, c *sessionapi.Client, id string, jsonMode bool, out, errOut io.Writer) int {
	if id != "" {
		op, e := c.Operation(ctx, id)
		if e != nil {
			return operationQueryFailure(errOut, id, e)
		}
		return writeOperation(out, errOut, op, jsonMode)
	}
	ops, e := c.Operations(ctx)
	if e != nil {
		return controlFailure(errOut, e)
	}
	if jsonMode {
		return encodeControl(out, ops)
	}
	for _, op := range ops {
		if code := writeOperationValue(out, op, false); code != 0 {
			return code
		}
	}
	return 0
}

// A failed result lookup provides no evidence of execution failure. Transport
// loss/missing results stay unresolved while protocol and input rejection keep 2.
func operationQueryFailure(errOut io.Writer, id string, e error) int {
	code := controlFailure(errOut, e)
	if code == 1 || code == 3 {
		fmt.Fprintf(errOut, "Operation %s result is unresolved; retry this result query when the session is reachable.\n", id)
		return 3
	}
	return code
}
