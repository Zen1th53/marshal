//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/execution"
)

type requiredIntegrationSourceRegistry struct{}

func (r *Runtime) resolveRequiredCompletionSource(ctx context.Context, root execution.ExecutionRun) (string, string, string, error) {
	tree, err := execution.WorkspaceTreeDigest(r.layout.Root)
	return r.layout.Root, "", tree, err
}
