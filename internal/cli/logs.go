package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"io"
	"time"
)

func runLogs(ctx context.Context, c *sessionapi.Client, target string, after uint64, limit int, follow, jsonMode bool, out, errOut io.Writer) int {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		page, e := c.Logs(ctx, target, after, limit)
		if e != nil {
			if ctx.Err() != nil && follow {
				return 0
			}
			return controlFailure(errOut, e)
		}
		if jsonMode {
			if e = json.NewEncoder(out).Encode(page); e != nil {
				return controlFailure(errOut, e)
			}
		} else {
			if page.Gap {
				fmt.Fprintf(errOut, "Log gap: %d entries dropped before cursor %d.\n", page.Dropped, after)
			}
			for _, entry := range page.Entries {
				if _, e = fmt.Fprintf(out, "%s %s [%s] %s\n", entry.Time.Format(time.RFC3339Nano), entry.ServiceID, entry.Stream, entry.Text); e != nil {
					return controlFailure(errOut, e)
				}
			}
		}
		after = page.NextCursor
		if !follow {
			return 0
		}
		if len(page.Entries) == limit {
			continue
		}
		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
		}
	}
}
