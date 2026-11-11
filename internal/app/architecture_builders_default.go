//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/architecturereview"
)

// Community plans by role; it has no per-part builder.
func (r *Runtime) architectureBuilders() func(context.Context) []architecturereview.BuilderOption {
	return nil
}
