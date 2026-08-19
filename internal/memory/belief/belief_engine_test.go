package belief_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/belief"
)

func TestT141BeliefUncertaintyMemory(t *testing.T) {
	ctx := context.Background()
	engine := belief.NewEngine()

	// 1. Create Belief Set with 2 competing hypotheses for transient failure
	bSet, err := engine.CreateBeliefSet(ctx, "OBS-500-ERR", "Database write error during load test", []belief.Hypothesis{
		{ID: "H1", Description: "SQLite lock contention due to DELETE journal mode"},
		{ID: "H2", Description: "Exhausted file descriptors in OS"},
	})
	if err != nil {
		t.Fatalf("CreateBeliefSet: %v", err)
	}

	if len(bSet.Hypotheses) != 2 {
		t.Fatalf("expected 2 hypotheses, got: %d", len(bSet.Hypotheses))
	}
	// Initial probabilities should be balanced (0.5 each)
	if bSet.Hypotheses[0].Probability != 0.5 || bSet.Hypotheses[1].Probability != 0.5 {
		t.Fatalf("expected equal 0.5 initial probabilities, got %f and %f", bSet.Hypotheses[0].Probability, bSet.Hypotheses[1].Probability)
	}

	// 2. Add supporting evidence to H1 (SQLite logs show SQLITE_BUSY)
	updatedSet, err := engine.AddEvidence(ctx, "OBS-500-ERR", "H1", "EVID-WAL-BUSY-LOG")
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	if updatedSet.Hypotheses[0].Probability <= updatedSet.Hypotheses[1].Probability {
		t.Fatalf("expected H1 probability (%f) > H2 probability (%f) after evidence", updatedSet.Hypotheses[0].Probability, updatedSet.Hypotheses[1].Probability)
	}

	// 3. Agent assertion of 0.99 confidence cannot bypass evidence weight
	unsupportedHypo := belief.Hypothesis{
		ID:          "H3",
		Description: "Agent hallucinated cause",
		AgentClaim:  0.99, // High claim with 0 evidence
	}
	derivedProb := engine.CalculateDerivedProbability(unsupportedHypo, 2)
	if derivedProb > 0.5 {
		t.Fatalf("agent claim of 0.99 without evidence should not yield high probability: got %f", derivedProb)
	}

	// 4. Resolve belief into winning fact
	resolved, err := engine.ResolveBelief(ctx, "OBS-500-ERR", "H1", "EVID-DECISIVE-WAL")
	if err != nil {
		t.Fatalf("ResolveBelief: %v", err)
	}
	if resolved.ResolvedWinnerID != "H1" || len(resolved.PriorAlternatives) != 1 {
		t.Fatalf("expected H1 winner and preserved alternatives, got: %+v", resolved)
	}
}
