//go:build !linux

package workerterminal

import (
	"context"
	"fmt"
	"os/exec"
)

type Host func(context.Context, string) error
type contextKey struct{}

func WithHost(ctx context.Context, host Host) context.Context {
	return context.WithValue(ctx, contextKey{}, host)
}
func Attach(ctx context.Context, cmd *exec.Cmd) (func(), error) {
	if ctx.Value(contextKey{}) != nil {
		return nil, fmt.Errorf("worker terminal transport unavailable on this platform")
	}
	return func() {}, nil
}
