//go:build !darwin

package process

import "context"

func platformTopology(ctx context.Context, source Reader) ([]Info, error) { return source.List(ctx) }
