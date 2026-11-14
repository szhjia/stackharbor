//go:build !darwin

package process

import "context"

func nativeIdentity(ctx context.Context, pid int32) (Info, error) { return inspect(ctx, pid, false) }
