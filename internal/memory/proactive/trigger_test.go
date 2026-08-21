package proactive_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/proactive"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT139ProactiveMemoryTriggeringAndNavigableRecall(t *testing.T) {
	ctx := context.Background()
	engine := proactive.NewEngine(proactive.Config{
		MaxNavigationDepth: 2,
		MaxBranchingFactor: 3,
	})

	// 1. Trigger test: Repeated failure triggers proactive recall
	taskWithFailure := proactive.TaskContext{
		TaskID:          "TASK-FAIL-01",
		Prompt:          "Run test suite",
		FailureCount:    2,
		LastStderr:      "database is locked (5) (SQLITE_BUSY)",
		AllowedScopeIDs: []string{"scope-1"},
	}
	decision := engine.EvaluateTrigger(ctx, taskWithFailure)
	if !decision.ShouldRecall || decision.Reason != proactive.TriggerReasonRepeatedFailure {
		t.Fatalf("expected proactive recall triggered by repeated failure, got: %+v", decision)
	}

	// 2. Trigger test: Simple fresh task skips recall
	simpleTask := proactive.TaskContext{
		TaskID:          "TASK-FRESH-01",
		Prompt:          "echo hello world",
		FailureCount:    0,
		AllowedScopeIDs: []string{"scope-1"},
	}
	decisionFresh := engine.EvaluateTrigger(ctx, simpleTask)
	if decisionFresh.ShouldRecall {
		t.Fatalf("expected fresh simple task to skip recall, got: %+v", decisionFresh)
	}

	// 3. Navigable recall: Depth cap and scope isolation
	nodesMap := map[string]model.MemoryRecordV2{
		"MEM-1": {
			ID:          "MEM-1",
			ScopeID:     "scope-1",
			Title:       "SQLite Busy Timeout",
			Lifecycle:   model.MemoryDurable,
			EvidenceIDs: []string{"EVID-1"},
		},
		"MEM-2": {
			ID:        "MEM-2",
			ScopeID:   "scope-unauthorized", // Cross-scope
			Title:     "Unauthorized Cross-Scope Doc",
			Lifecycle: model.MemoryDurable,
		},
		"MEM-REVOKED": {
			ID:        "MEM-REVOKED",
			ScopeID:   "scope-1",
			Title:     "Revoked Obsolete Pragma",
			Lifecycle: model.MemoryTombstoned, // Tombstoned
		},
	}

	links := map[string][]string{
		"MEM-1": {"MEM-2", "MEM-REVOKED"},
	}

	navigated, err := engine.Navigate(ctx, "MEM-1", []string{"scope-1"}, func(id string) (model.MemoryRecordV2, []string, bool) {
		rec, ok := nodesMap[id]
		return rec, links[id], ok
	})
	if err != nil {
		t.Fatalf("Navigate: %v", err)
	}

	// Only MEM-1 should be returned, MEM-2 (cross-scope) and MEM-REVOKED (tombstoned) must be filtered out
	if len(navigated) != 1 || navigated[0].ID != "MEM-1" {
		t.Fatalf("expected only MEM-1 in navigated results, got: %+v", navigated)
	}
}
