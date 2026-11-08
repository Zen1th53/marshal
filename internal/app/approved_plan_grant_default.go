//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// Community binds no approved-plan authority: every approval keeps its wait.
func (r *Runtime) privateApprovedPlanExecution(ctx context.Context, _ marshal.Run, _ *execution.ExecutionRun, _ string) context.Context {
	return ctx
}
