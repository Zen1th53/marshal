//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
)

// noteAgySigninExpiry is a no-op in the default build: Community never cools
// down or switches a provider automatically.
func (r *Runtime) noteAgySigninExpiry(context.Context, execution.TaskExecution, error, adapter.Result) (bool, error) {
	return false, nil
}
