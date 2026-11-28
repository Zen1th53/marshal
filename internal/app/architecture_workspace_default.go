//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/architectureworkspace"
)

func (r *Runtime) ArchitectureWorkspace(context.Context, ...architectureworkspace.Finding) (architectureworkspace.Snapshot, error) {
	return architectureworkspace.Snapshot{Notice: "Accepted project contracts are unavailable in this edition."}, nil
}
