//go:build governed

package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// Community never binds a dispatch to approved-plan authority.
func TestCommunityApprovedPlanExecutionIsNoOp(t *testing.T) {
	ctx := context.Background()
	run := marshal.Run{PlanID: "plan", PlanVersion: 1, Process05Bound: true}
	if (&Runtime{}).privateApprovedPlanExecution(ctx, run, &execution.ExecutionRun{RunID: "run", PlanID: "plan", PlanVersion: 1}, "task") != ctx {
		t.Fatal("Community dispatch gained an approved-plan binding")
	}
}
