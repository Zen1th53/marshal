//go:build governed

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

type providerTransportState struct{}

func (r *Runtime) reviewProviderTransport(ctx context.Context, _ execution.TaskExecution, _ execution.ConstraintPackage, _ model.Task, _ string, _ string, _ string, _ string) (context.Context, *execution.TaskResult, error) {
	return ctx, nil, nil
}
func (r *Runtime) authorizeReviewedProviderTransport(context.Context, string, string, string, string, model.Task, string, string, model.Role, string) (bool, error) {
	return false, nil
}

func (r *Runtime) consumeReviewedProviderTransport(context.Context, string, string) error { return nil }

// Community preserves the existing constraint header and approval semantics.
func (r *Runtime) providerConstraintPrompt(_ context.Context, _ execution.TaskExecution, pkg execution.ConstraintPackage, _ string) (string, error) {
	return pkg.FormatPromptHeader(), nil
}
