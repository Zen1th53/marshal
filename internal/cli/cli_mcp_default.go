//go:build governed

package cli

import (
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/mcp"
)

func newStdioMCPServer(runtime *app.Runtime, role, task string) (*mcp.Server, error) {
	return mcp.NewScopedServer(runtime, role, task), nil
}

func stdioWorkerLeaseRequired() bool { return true }
