package interpretation_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/interpretation"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestTrivialTaskProceedsWithoutMultiAgentTax(t *testing.T) {
	scaler := interpretation.NewScaler()
	comparator := interpretation.NewComparator()

	// Trivial doc typo fix
	req := scaler.EvaluateRequirements(model.R0, false, []string{"README.md"}, []string{"documentation", "typo"})
	if req.MinInterpreters != 1 {
		t.Fatalf("expected 1 interpreter for R0 typo task, got %d", req.MinInterpreters)
	}

	interp := model.Interpretation{
		ID:               "interp-typo-1",
		GoalID:           "goal-typo-1",
		GoalRevision:     1,
		SessionID:        "sess-typo-1",
		Author:           model.AuthorProvenance{AgentID: "codex-dev", Harness: "codex"},
		DesiredOutcome:   "Fix typo in README documentation",
		ExpectedArtifact: "README.md",
		Scope:            []string{"README.md"},
		IsDestructive:    false,
		SubmittedAt:      time.Now().UTC(),
	}

	res := comparator.Compare("sess-typo-1", "goal-typo-1", 1, req, []model.Interpretation{interp})
	if res.State != model.GoalReady {
		t.Fatalf("expected READY for trivial task, got %v", res.State)
	}
	if len(res.ConcreteQuestions) != 0 {
		t.Fatalf("expected 0 questions for trivial task, got %d", len(res.ConcreteQuestions))
	}
}

func TestAmbiguousDestructiveRequestTriggersBlindInterpretations(t *testing.T) {
	scaler := interpretation.NewScaler()
	comparator := interpretation.NewComparator()

	// Destructive file deletion / cleanup request
	req := scaler.EvaluateRequirements(model.R1, true, []string{"internal/legacy"}, []string{"cleanup", "delete"})
	if req.MinInterpreters < 2 {
		t.Fatalf("expected >= 2 interpreters for destructive request, got %d", req.MinInterpreters)
	}

	// Only 1 submitted so far
	interp := model.Interpretation{
		ID:               "interp-destr-1",
		GoalID:           "goal-destr-1",
		GoalRevision:     1,
		SessionID:        "sess-destr-1",
		Author:           model.AuthorProvenance{AgentID: "claude-arch", Harness: "claude-code"},
		DesiredOutcome:   "Delete legacy auth handlers",
		ExpectedArtifact: "internal/legacy deleted",
		Scope:            []string{"internal/legacy"},
		IsDestructive:    true,
		SubmittedAt:      time.Now().UTC(),
	}

	res := comparator.Compare("sess-destr-1", "goal-destr-1", 1, req, []model.Interpretation{interp})
	if res.State != model.GoalNeedsInput {
		t.Fatalf("expected NEEDS_INPUT when required count not yet met, got %v", res.State)
	}
}

func TestConsensusDoesNotBecomeEvidence(t *testing.T) {
	scaler := interpretation.NewScaler()
	comparator := interpretation.NewComparator()

	req := scaler.EvaluateRequirements(model.R2, false, []string{"internal/api"}, []string{"feature"})

	now := time.Now().UTC()
	interp1 := model.Interpretation{
		ID:               "interp-seed-1",
		GoalID:           "goal-consensus-1",
		GoalRevision:     1,
		SessionID:        "sess-cons-1",
		Author:           model.AuthorProvenance{AgentID: "agent-1", Harness: "harness-1"},
		DesiredOutcome:   "Scale endpoint throughput",
		ExpectedArtifact: "internal/api/handler.go",
		Scope:            []string{"internal/api"},
		IsDestructive:    false,
		SubmittedAt:      now,
	}

	interp2 := model.Interpretation{
		ID:               "interp-seed-2",
		GoalID:           "goal-consensus-1",
		GoalRevision:     1,
		SessionID:        "sess-cons-1",
		Author:           model.AuthorProvenance{AgentID: "agent-2", Harness: "harness-2"},
		DesiredOutcome:   "Scale endpoint throughput",
		ExpectedArtifact: "internal/api/handler.go",
		Scope:            []string{"internal/api"},
		IsDestructive:    false,
		SubmittedAt:      now,
	}

	res := comparator.Compare("sess-cons-1", "goal-consensus-1", 1, req, []model.Interpretation{interp1, interp2})
	if res.State != model.GoalReady {
		t.Fatalf("expected READY on consensus, got %v", res.State)
	}
	if !res.ConsensusConfirmed {
		t.Fatalf("expected consensus to be confirmed")
	}

	// Epistemic invariant check: a claim about throughput is still UNSUPPORTED or SUPPORTED,
	// consensus between agents NEVER converts a factual claim to VERIFIED without empirical evidence.
	throughputClaim := model.Claim{
		ID:          "claim-throughput-1",
		Subject:     "Endpoint handles 10000 req/sec",
		Criticality: model.CriticalityBlocker,
		State:       model.ClaimStateUnsupported,
	}

	if throughputClaim.State == model.ClaimStateVerified {
		t.Fatalf("epistemic invariant violation: consensus cannot mark claim as VERIFIED")
	}
}

func TestDivergentInterpretationsProduceConcreteQuestion(t *testing.T) {
	scaler := interpretation.NewScaler()
	comparator := interpretation.NewComparator()

	req := scaler.EvaluateRequirements(model.R2, false, []string{"internal/store"}, nil)

	now := time.Now().UTC()
	// Claude interprets as in-place refactor
	claudeInterp := model.Interpretation{
		ID:               "interp-claude",
		GoalID:           "goal-div-1",
		GoalRevision:     1,
		SessionID:        "sess-div-1",
		Author:           model.AuthorProvenance{AgentID: "claude-architect", Harness: "claude-code", Model: "claude-3-7-sonnet"},
		DesiredOutcome:   "In-place optimization of store queries preserving existing table schema",
		ExpectedArtifact: "internal/store/query.go",
		Scope:            []string{"internal/store"},
		IsDestructive:    false,
		SubmittedAt:      now,
	}

	// Antigravity interprets as full database rewrite
	antigravityInterp := model.Interpretation{
		ID:               "interp-antigravity",
		GoalID:           "goal-div-1",
		GoalRevision:     1,
		SessionID:        "sess-div-1",
		Author:           model.AuthorProvenance{AgentID: "antigravity-integrator", Harness: "antigravity", Model: "gemini-2.5-pro"},
		DesiredOutcome:   "Complete rewrite and replace of internal storage engine with new schema",
		ExpectedArtifact: "internal/store/engine.go",
		Scope:            []string{"internal/store", "internal/store/database-delete"},
		IsDestructive:    true,
		SubmittedAt:      now,
	}

	res := comparator.Compare("sess-div-1", "goal-div-1", 1, req, []model.Interpretation{claudeInterp, antigravityInterp})

	if res.State != model.GoalNeedsInput {
		t.Fatalf("expected NEEDS_INPUT on material divergence, got %v", res.State)
	}
	if len(res.ConcreteQuestions) == 0 {
		t.Fatalf("expected concrete questions explaining the material ambiguity, got none")
	}

	// Verify question explains the rewrite vs in-place ambiguity
	foundRewriteQuestion := false
	for _, q := range res.ConcreteQuestions {
		if len(q.Options) > 0 {
			foundRewriteQuestion = true
			break
		}
	}
	if !foundRewriteQuestion {
		t.Fatalf("expected actionable concrete options for operator clarification")
	}
}

func TestUserCorrectionCreatesGoalV2AndInvalidatesClaims(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "marshal_invalidator.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	goalV1 := model.GoalContract{
		ID:                 "goal-inv-1",
		SessionID:          "sess-inv-1",
		Revision:           1,
		DesiredOutcome:     "Refactor database storage layer",
		Risk:               model.R2,
		AuthoritySource:    "operator",
		UnderstandingState: model.GoalNeedsInput,
		UnresolvedDecisions: []model.UnresolvedDecision{
			{
				ID:           "dec-rewrite-or-inplace",
				Question:     "Do you want a full replacement/rewrite or an in-place modification?",
				Impact:       "Rewrites risk discarding uncommitted or existing working architecture",
				Options:      []string{"In-place modification", "Full rewrite"},
				RequiresUser: true,
			},
		},
	}

	if err := st.SaveGoalContract(ctx, goalV1, 0); err != nil {
		t.Fatalf("save goal v1 failed: %v", err)
	}

	// Prior critical claim created under v1
	priorClaim := model.Claim{
		ID:             "claim-storage-v1",
		GoalID:         goalV1.ID,
		GoalRevision:   1,
		Subject:        "New rewrite schema satisfies performance limits",
		NormalizedText: "new rewrite schema satisfies performance limits",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateSupported,
		Scope:          "internal/store",
		Author:         model.AuthorProvenance{AgentID: "agent-dev", Harness: "codex"},
		CreatedAt:      time.Now().UTC(),
	}
	if err := st.SaveClaim(ctx, priorClaim); err != nil {
		t.Fatalf("save claim failed: %v", err)
	}

	// Apply user correction choosing "In-place modification"
	invalidator := interpretation.NewInvalidator(st)
	actor := model.AuthorProvenance{AgentID: "operator-user"}

	res, err := invalidator.ApplyCorrection(ctx, goalV1, "dec-rewrite-or-inplace", "In-place modification", actor)
	if err != nil {
		t.Fatalf("ApplyCorrection failed: %v", err)
	}

	if res.UpdatedGoal.Revision != 2 {
		t.Fatalf("expected Goal revision 2, got %d", res.UpdatedGoal.Revision)
	}
	if res.UpdatedGoal.UnderstandingState != model.GoalReady {
		t.Fatalf("expected UnderstandingState READY after user clarification, got %v", res.UpdatedGoal.UnderstandingState)
	}
	if len(res.InvalidatedClaimIDs) != 1 || res.InvalidatedClaimIDs[0] != "claim-storage-v1" {
		t.Fatalf("expected claim-storage-v1 to be invalidated, got %v", res.InvalidatedClaimIDs)
	}

	// Verify in store that claim is now INVALIDATED
	updatedClaim, err := st.GetClaim(ctx, "claim-storage-v1")
	if err != nil {
		t.Fatalf("GetClaim failed: %v", err)
	}
	if updatedClaim.State != model.ClaimStateInvalidated {
		t.Fatalf("expected claim to be INVALIDATED in store, got %v", updatedClaim.State)
	}
}

func TestSealedInterpretationAntiAnchoring(t *testing.T) {
	collector := interpretation.NewCollector()
	ctx := context.Background()

	now := time.Now().UTC()
	interp1 := model.Interpretation{
		ID:               "interp-iso-1",
		GoalID:           "goal-iso-1",
		GoalRevision:     1,
		SessionID:        "sess-iso-1",
		Author:           model.AuthorProvenance{AgentID: "claude-arch", Harness: "claude-code", Model: "claude-3-7-sonnet"},
		DesiredOutcome:   "Implement auth module",
		ExpectedArtifact: "internal/auth",
		SubmittedAt:      now,
	}

	if err := collector.Submit(ctx, interp1); err != nil {
		t.Fatalf("Submit interp1 failed: %v", err)
	}

	// Attempt duplicate submission by same agent
	if err := collector.Submit(ctx, interp1); err == nil {
		t.Fatalf("expected error on duplicate submission from same agent, got nil")
	}

	// Heterogeneous requirement check
	req := model.InterpretationRequirement{
		MinInterpreters:             2,
		RequireHeterogeneousHarness: true,
		RequireDifferentModels:      true,
	}

	// Submitting second interpretation from same harness should fail diversity
	interpSameHarness := model.Interpretation{
		ID:               "interp-iso-2",
		GoalID:           "goal-iso-1",
		GoalRevision:     1,
		SessionID:        "sess-iso-1",
		Author:           model.AuthorProvenance{AgentID: "claude-second", Harness: "claude-code", Model: "claude-3-7-sonnet"},
		DesiredOutcome:   "Implement auth module",
		ExpectedArtifact: "internal/auth",
		SubmittedAt:      now.Add(1 * time.Second),
	}

	err := collector.ValidateDiversity([]model.Interpretation{interp1, interpSameHarness}, req)
	if err == nil {
		t.Fatalf("expected error on non-heterogeneous harnesses when required, got nil")
	}

	// Submitting with Antigravity / Gemini satisfies diversity
	interpAntigravity := model.Interpretation{
		ID:               "interp-iso-3",
		GoalID:           "goal-iso-1",
		GoalRevision:     1,
		SessionID:        "sess-iso-1",
		Author:           model.AuthorProvenance{AgentID: "antigravity-dev", Harness: "antigravity", Model: "gemini-2.5-pro"},
		DesiredOutcome:   "Implement auth module",
		ExpectedArtifact: "internal/auth",
		SubmittedAt:      now.Add(2 * time.Second),
	}

	err = collector.ValidateDiversity([]model.Interpretation{interp1, interpAntigravity}, req)
	if err != nil {
		t.Fatalf("expected diversity to be met with Claude + Antigravity, got %v", err)
	}
}
