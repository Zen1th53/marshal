package app

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/dag"
)

// ensureDAGReady gates tasks that participate in the canonical T29 graph.
// Tasks without a DAG node remain legacy/unmanaged work until the scheduler
// enrolls them; once enrolled, readiness is derived only from canonical graph
// state and never from provider or caller prose.
func (r *Runtime) ensureDAGReady(ctx context.Context, taskID string) error {
	if r.dag == nil {
		return dag.ErrNotReady
	}
	readiness, err := r.dag.Ready(ctx, dag.TaskID(taskID))
	if errors.Is(err, dag.ErrNodeNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !readiness.Ready {
		return dag.ErrNotReady
	}
	return nil
}
