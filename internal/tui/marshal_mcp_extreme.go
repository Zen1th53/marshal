//go:build !governed

package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// ConfigureWorkerMCP exports per-session MCP launch configuration for workers with a read-only subset.
func ConfigureWorkerMCP(provider, root, taskID string, args, env []string) ([]string, []string, func(), error) {
	dir, err := newBriefingDir(root, "worker-"+provider)
	if err != nil {
		return nil, nil, nil, err
	}
	cleanup := func() {
		dir.remove()
	}
	newArgs, newEnv, _, err := configureMarshalMCP(provider, root, args, env, dir, "worker", taskID)
	if err != nil {
		cleanup()
		return nil, nil, nil, err
	}
	return newArgs, newEnv, cleanup, nil
}

func configureWorkerDriverExtreme(d driver.Driver, root string, agent *activeTmuxAgent) driver.Driver {
	nativeDrv, ok := d.(driver.Native)
	if !ok || nativeDrv.ConfigureLaunch != nil {
		return d
	}
	nativeDrv.ConfigureLaunch = func(_ context.Context, r driver.Request, args, env []string) ([]string, []string, func(), error) {
		taskID := agent.canonicalTaskID
		if taskID == "" {
			taskID = r.Task.PlanTaskID
		}
		return ConfigureWorkerMCP(r.Task.Worker, root, taskID, args, env)
	}
	return nativeDrv
}

// nativeMarshalAuthority is true in Extreme: the Marshal keeps the full local
// MCP authority.
const nativeMarshalAuthority = true
