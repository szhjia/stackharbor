//go:build !linux

package process

import (
	"context"

	gp "github.com/shirou/gopsutil/v4/process"
)

func CreatedMillis(ctx context.Context, pid int32) (int64, error) {
	p, err := gp.NewProcessWithContext(ctx, pid)
	if err != nil {
		return 0, err
	}
	return p.CreateTimeWithContext(ctx)
}
