//go:build governed

package cli

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/mcp"
	"github.com/Zen1th53/marshal/internal/model"
)

func newStdioMCPServer(runtime *app.Runtime, role, task string) (*mcp.Server, error) {
	srv := mcp.NewScopedServer(runtime, role, task)
	if role == "marshal" {
		if service := runtime.Marshal(); service != nil {
			id, _, err := runtime.Store().LatestMarshalRun(context.Background(), service.ProjectID)
			if err != nil && !errors.Is(err, model.ErrNotFound) {
				return nil, err
			}
			srv.SetBoundRunID(id)
		}
	}
	return srv, nil
}

func stdioWorkerLeaseRequired() bool { return true }
