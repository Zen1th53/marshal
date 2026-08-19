package adaptive_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/adaptive"
)

func TestT143AdaptiveMemoryController(t *testing.T) {
	ctx := context.Background()
	ctrl := adaptive.NewController(adaptive.Config{
		EnableBandit: true,
	})

	// 1. Clean, trivial early task -> ActionNoOp
	act1 := ctrl.DecideAction(ctx, adaptive.TaskState{
		TaskID:         "TASK-CLEAN",
		StepIndex:      0,
		FailureCount:   0,
		HasKnownSkill:  false,
		BudgetRemaining: 4096,
	})
	if act1.Type != adaptive.ActionNoOp {
		t.Fatalf("expected ActionNoOp for fresh early task, got: %s", act1.Type)
	}

	// 2. Repeated goal with established skill -> ActionInjectProcedure
	act2 := ctrl.DecideAction(ctx, adaptive.TaskState{
		TaskID:         "TASK-SKILL-RUN",
		StepIndex:      1,
		FailureCount:   0,
		HasKnownSkill:  true,
		BudgetRemaining: 3000,
	})
	if act2.Type != adaptive.ActionInjectProcedure {
		t.Fatalf("expected ActionInjectProcedure for known skill, got: %s", act2.Type)
	}

	// 3. Stuck / failing task -> ActionReQuery
	act3 := ctrl.DecideAction(ctx, adaptive.TaskState{
		TaskID:         "TASK-STUCK",
		StepIndex:      4,
		FailureCount:   2,
		BudgetRemaining: 2000,
	})
	if act3.Type != adaptive.ActionReQuery {
		t.Fatalf("expected ActionReQuery for stuck task with failures, got: %s", act3.Type)
	}

	// 4. Kill switch forces deterministic fallback
	ctrl.DisableBandit()
	actFallback := ctrl.DecideAction(ctx, adaptive.TaskState{
		TaskID:       "TASK-STUCK-FALLBACK",
		FailureCount: 1,
	})
	if actFallback.Type != adaptive.ActionRecall {
		t.Fatalf("expected deterministic fallback ActionRecall, got: %s", actFallback.Type)
	}
}
