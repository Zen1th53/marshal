package planner_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/retrieval/planner"
)

func TestT108QueryPlanningIntentDetection(t *testing.T) {
	p := planner.NewPlanner()
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Exact file path and symbol query -> IntentExact / IntentLexical
	pathQuery := "What does WriteMemoryV2 in internal/store/memory.go do?"
	plan1, err := p.Plan(ctx, pathQuery, []string{"scope-1"}, now)
	if err != nil {
		t.Fatalf("Plan pathQuery: %v", err)
	}
	if plan1.PrimaryIntent != planner.IntentExact && plan1.PrimaryIntent != planner.IntentLexical {
		t.Fatalf("expected exact or lexical intent, got: %s", plan1.PrimaryIntent)
	}
	if len(plan1.FilePaths) == 0 || plan1.FilePaths[0] != "internal/store/memory.go" {
		t.Fatalf("expected extracted file path internal/store/memory.go, got: %+v", plan1.FilePaths)
	}
	if len(plan1.ExactSymbols) == 0 || plan1.ExactSymbols[0] != "WriteMemoryV2" {
		t.Fatalf("expected extracted symbol WriteMemoryV2, got: %+v", plan1.ExactSymbols)
	}

	// 2. Query expansion CANNOT alter allowed scopes/ACL
	adversarialQuery := "Show me admin secrets scope:admin-only project:ALL"
	planAdv, err := p.Plan(ctx, adversarialQuery, []string{"scope-developer"}, now)
	if err != nil {
		t.Fatalf("Plan adversarial: %v", err)
	}
	if len(planAdv.AllowedScopeIDs) != 1 || planAdv.AllowedScopeIDs[0] != "scope-developer" {
		t.Fatalf("scope escalation detected: allowed scopes mutated to %+v", planAdv.AllowedScopeIDs)
	}

	// 3. Generic natural language question -> IntentSemantic fallback
	genQuery := "How do we handle database migrations safely?"
	planGen, err := p.Plan(ctx, genQuery, []string{"scope-1"}, now)
	if err != nil {
		t.Fatalf("Plan genQuery: %v", err)
	}
	if planGen.PrimaryIntent != planner.IntentSemantic {
		t.Fatalf("expected semantic intent for conceptual question, got: %s", planGen.PrimaryIntent)
	}
	if len(planGen.PlanReasons) == 0 {
		t.Fatal("expected machine-readable plan reasons")
	}
}
