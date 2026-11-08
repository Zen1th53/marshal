//go:build governed

package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
)

// Community retains the original mock worker and every pause assertion.
func pausePlanFixture(t *testing.T) (*Runtime, CreatePlanRequest, func(chan struct{}, chan struct{}, chan struct{})) {
	t.Helper()
	r := runtimeForPlan(t)
	register := func(started, cancelled, settle chan struct{}) {
		r.Execution().RegisterHarness(execution.NewMockHarness("test-harness", func(ctx context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, _ string) (execution.TaskResult, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-settle
			return execution.TaskResult{TaskID: task.TaskID, ErrorMessage: "settled pause"}, ctx.Err()
		}))
	}
	return r, planCreateRequest(), register
}
