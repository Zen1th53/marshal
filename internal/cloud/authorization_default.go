//go:build governed

package cloud

import "context"

const ExecutionOnAtStartup = false

type buildGateState struct{}

func (g *Gate) localEntitlement() bool { return false }

// Authorize uses the cloud entitlement protocol in the default build.
func Authorize(ctx context.Context, cfg Config, runtimeDir, clientVersion string) Authorization {
	return authorizeCloud(ctx, cfg, runtimeDir, clientVersion)
}
