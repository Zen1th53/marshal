//go:build governed

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
)

func (*Runtime) preflightRequiredProcess05Context(context.Context, execution.TaskExecution, string, string, string, execution.ConstraintPackage) error {
	return nil
}
func (*Runtime) prepareRequiredProcess05Request(ctx context.Context, task execution.TaskExecution, _, _, _ string, request *adapter.Request, optional string) error {
	request.TrustedContext += process05BriefFor(ctx, task) + optional
	return nil
}

func (r *Runtime) prepareWatchedRequiredProcess05Request(ctx context.Context, task execution.TaskExecution, agent, provider, modelName string, request *adapter.Request, optional string) (context.Context, func() error, error) {
	if err := r.prepareRequiredProcess05Request(ctx, task, agent, provider, modelName, request, optional); err != nil {
		return nil, nil, err
	}
	return ctx, func() error { return nil }, nil
}
