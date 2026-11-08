//go:build !governed

package cloud

import "context"

const ExecutionOnAtStartup = true

type buildGateState struct{ localGrant bool }

// Authorize supplies the build's local grant without creating a cloud client.
func Authorize(context.Context, Config, string, string) Authorization {
	return Authorization{Gate: &Gate{buildGateState: buildGateState{localGrant: true}}}
}

func (g *Gate) localEntitlement() bool { return g != nil && g.localGrant }
