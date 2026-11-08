//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/model"
)

// outsideReadMounts: Community workers read only their worktree; folders
// outside the project stay behind the host-side operator read decision.
func (r *Runtime) outsideReadMounts(context.Context, string, string, string) []model.OutsideRead {
	return nil
}

// outsideReadDirArgs: Community Marshal turns read only their directory.
func (*MarshalCLI) outsideReadDirArgs(context.Context, string) []string { return nil }
