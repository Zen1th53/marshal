//go:build !governed && unix

package mcp

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

// WithWorkerHost scopes the transport factory to the trusted CLI launch.
// The app holds the factory; no worker tool argument can replace it.
func WithWorkerHost(ctx context.Context) context.Context {
	return sessionmcp.WithWorkerServerFactory(ctx, func(spec sessionmcp.WorkerHostSpec) (sessionmcp.WorkerServer, error) {
		runtime, ok := spec.Runtime.(*app.Runtime)
		if !ok || runtime == nil || spec.PrincipalID == "" || spec.TaskID == "" || spec.Validate == nil {
			return nil, fmt.Errorf("%w: invalid worker host binding", model.ErrPolicyDenied)
		}
		actual, err := filepath.EvalSymlinks(runtime.ProjectRoot())
		if err != nil || actual != spec.Root {
			return nil, model.ErrPolicyDenied
		}
		server := NewExtremeServer(runtime, "worker", spec.TaskID)
		server.workerCaller = &auth.Principal{ID: spec.PrincipalID, Name: spec.PrincipalID, Kind: auth.KindMCPClient, Capabilities: []string{string(auth.CapStatusRead), string(auth.CapTaskRead)}}
		server.workerValidate = spec.Validate
		return func(callCtx context.Context, in io.Reader, out io.Writer) error {
			if spec.RequestContext != nil {
				callCtx = spec.RequestContext(callCtx)
			}
			return server.ServeStdio(callCtx, in, out)
		}, nil
	})
}
