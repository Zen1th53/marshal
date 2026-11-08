//go:build !governed && unix

package cli

import (
	"context"
	"github.com/Zen1th53/marshal/internal/mcp"
)

func withWorkerHost(ctx context.Context) context.Context { return mcp.WithWorkerHost(ctx) }
