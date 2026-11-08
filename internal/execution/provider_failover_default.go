//go:build governed

package execution

import "context"

// RebindProviderFailover is unavailable in ordinary execution. Provider changes
// cannot reuse authority bound to an already selected governed harness.
func (e *Engine) RebindProviderFailover(ctx context.Context, runID string, version int64, taskID, from, next, base, receipt string, authorize func(ExecutionRun, TaskExecution) error) (*ExecutionRun, error) {
	return nil, ErrUnauthorizedWorker
}
