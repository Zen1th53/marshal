//go:build !unix

package sessionmcp

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/model"
	"io"
)

type workerScopeKey struct{}

func WithWorker(ctx context.Context, root, task, lease string) context.Context {
	return context.WithValue(ctx, workerScopeKey{}, true)
}

func WithBoundWorker(context.Context, string, string, WorkerServer) (context.Context, error) {
	return nil, fmt.Errorf("%w: bound worker MCP requires a Unix host", model.ErrUnavailable)
}
func Configure(ctx context.Context, provider string, args, env []string) ([]string, []string, func(), error) {
	if ctx.Value(workerScopeKey{}) != nil {
		return nil, nil, func() {}, fmt.Errorf("%w: per-session MCP relay requires a Unix build", model.ErrUnavailable)
	}
	return args, env, func() {}, nil
}

func SandboxRequest(ctx context.Context, request model.SandboxRequest) (model.SandboxRequest, error) {
	return request, nil
}

func Relay(ctx context.Context, requests, responses string, in io.Reader, out io.Writer) error {
	return fmt.Errorf("%w: per-session MCP relay requires a Unix build", model.ErrUnavailable)
}
