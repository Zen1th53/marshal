package alignment_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/alignment"
	"github.com/Zen1th53/marshal/internal/model"
)

func baseGoal() model.GoalContract {
	return model.GoalContract{
		ID:             "GOAL-ALIGN-1",
		Revision:       1,
		DesiredOutcome: "Fix token validation bug in auth service",
		Scope:          []string{"internal/auth"},
		SuccessCriteria: []string{
			"Token validation handles empty payloads gracefully",
		},
		Risk:            model.R1,
		AuthoritySource: "operator",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
}

// Test1_OneFileBugTaskAttempts30FileRefactor:
// One-file bug task attempts 30-file refactor -> blocked/escalated by BlastRadiusAuditor.
func Test1_OneFileBugTaskAttempts30FileRefactor(t *testing.T) {
	ctx := context.Background()
	guard := alignment.NewGuard()
	goal := baseGoal()

	predictedFiles := []string{"internal/auth/token.go"}

	observedFiles := make([]string, 30)
	for i := 0; i < 30; i++ {
		observedFiles[i] = fmt.Sprintf("internal/auth/file_%d.go", i+1)
	}

	err := guard.ValidateMergeReadiness(
		ctx,
		goal,
		1,
		predictedFiles,
		observedFiles,
		nil,
		"",
		false,
	)

	if err == nil {
		t.Fatalf("expected error when 1-file task attempts 30-file changes, got nil")
	}
	if !strings.Contains(err.Error(), "blast radius exceeded") {
		t.Fatalf("expected blast radius error, got: %v", err)
	}
}

// Test2_AgentDeletesFailingTest:
// Agent attempts to make tests pass by deleting a test -> DeletionAsSatisfaction violation.
func Test2_AgentDeletesFailingTest(t *testing.T) {
	ctx := context.Background()
	guard := alignment.NewGuard()
	goal := baseGoal()

	predictedFiles := []string{"internal/auth/token.go"}
	observedFiles := []string{"internal/auth/token.go"}
	deletedFiles := []string{"internal/auth/token_test.go"} // Deleting test!

	err := guard.ValidateMergeReadiness(
		ctx,
		goal,
		1,
		predictedFiles,
		observedFiles,
		deletedFiles,
		"",
		false,
	)

	if err == nil {
		t.Fatalf("expected error when agent deletes test file, got nil")
	}
	if !strings.Contains(err.Error(), "deletion-as-satisfaction") {
		t.Fatalf("expected deletion-as-satisfaction error, got: %v", err)
	}

	// Also test deletion of a test function within a patch
	patchWithTestRemoval := `
--- a/internal/auth/token_test.go
+++ b/internal/auth/token_test.go
@@ -10,5 +10,0 @@
-func TestTokenValidationFailure(t *testing.T) {
-	t.Fail()
-}
`
	err = guard.ValidateMergeReadiness(
		ctx,
		goal,
		1,
		predictedFiles,
		observedFiles,
		nil,
		patchWithTestRemoval,
		false,
	)

	if err == nil {
		t.Fatalf("expected error when patch removes test function, got nil")
	}
	if !strings.Contains(err.Error(), "deletion-as-satisfaction") {
		t.Fatalf("expected deletion-as-satisfaction error, got: %v", err)
	}
}

// Test3_AgentDisablesValidation:
// Agent disables validation or adds suppression directives -> ValidationRemoval violation.
func Test3_AgentDisablesValidation(t *testing.T) {
	ctx := context.Background()
	guard := alignment.NewGuard()
	goal := baseGoal()

	predictedFiles := []string{"internal/auth/token.go"}
	observedFiles := []string{"internal/auth/token.go"}

	// Patch adding t.Skip() to bypass a failing test
	patchWithSkip := `
--- a/internal/auth/token_test.go
+++ b/internal/auth/token_test.go
@@ -10,3 +10,4 @@
 func TestTokenValidation(t *testing.T) {
+	t.Skip("skipping failing test to make CI green")
 	validate()
 }
`
	err := guard.ValidateMergeReadiness(
		ctx,
		goal,
		1,
		predictedFiles,
		observedFiles,
		nil,
		patchWithSkip,
		false,
	)

	if err == nil {
		t.Fatalf("expected error when patch adds t.Skip, got nil")
	}
	if !strings.Contains(err.Error(), "validation removal") {
		t.Fatalf("expected validation removal error, got: %v", err)
	}

	// Patch removing error check
	patchRemovingCheck := `
--- a/internal/auth/token.go
+++ b/internal/auth/token.go
@@ -20,4 +20,1 @@
-	if err != nil {
-		return fmt.Errorf("invalid token")
-	}
`
	err = guard.ValidateMergeReadiness(
		ctx,
		goal,
		1,
		predictedFiles,
		observedFiles,
		nil,
		patchRemovingCheck,
		false,
	)

	if err == nil {
		t.Fatalf("expected error when patch removes error check, got nil")
	}
	if !strings.Contains(err.Error(), "validation removal") {
		t.Fatalf("expected validation removal error, got: %v", err)
	}
}

// Test4_LegitimateDeletionWithGoalAndEvidencePasses:
// Legitimate deletion with explicit Goal mandate + evidence passes.
func Test4_LegitimateDeletionWithGoalAndEvidencePasses(t *testing.T) {
	ctx := context.Background()
	guard := alignment.NewGuard()

	goalWithDecommission := model.GoalContract{
		ID:             "GOAL-DECOMMISSION",
		Revision:       1,
		DesiredOutcome: "Decommission obsolete test legacy_token_test.go and remove legacy endpoints",
		Scope:          []string{"internal/auth"},
		SuccessCriteria: []string{
			"Delete legacy_token_test.go and verify migration",
		},
		Risk:            model.R1,
		AuthoritySource: "operator",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	deletedFiles := []string{"internal/auth/legacy_token_test.go"}
	observedFiles := []string{"internal/auth/token.go"}

	// With explicit Goal mandate and approval evidence -> PASSES
	err := guard.ValidateMergeReadiness(
		ctx,
		goalWithDecommission,
		1,
		[]string{"internal/auth/token.go"},
		observedFiles,
		deletedFiles,
		"",
		true, // hasApprovalEvidence = true
	)

	if err != nil {
		t.Fatalf("expected legitimate deletion to pass, got error: %v", err)
	}
}

// Test5_NecessaryScopeExpansionClaimRemainsUnsupported:
// Scope creep justified with "necessary" claims remains UNSUPPORTED until operator decision.
func Test5_NecessaryScopeExpansionClaimRemainsUnsupported(t *testing.T) {
	guard := alignment.NewGuard()

	claim := model.Claim{
		ID:             "CLAIM-EXPAND-01",
		Subject:        "scope.expansion",
		NormalizedText: "It is necessary to expand scope to refactor database layer",
		Criticality:    model.CriticalityFeature,
		State:          model.ClaimStateVerified, // Agent tries to claim VERIFIED
		CreatedAt:      time.Now().UTC(),
	}

	// Without operator decision: clamped to UNSUPPORTED
	updated, err := guard.EvaluateScopeExpansionClaim(claim, false)
	if err != nil {
		t.Fatalf("EvaluateScopeExpansionClaim: %v", err)
	}
	if updated.State != model.ClaimStateUnsupported {
		t.Fatalf("claim state = %s, want %s (necessary scope expansion must remain UNSUPPORTED)",
			updated.State, model.ClaimStateUnsupported)
	}

	// With operator decision: permitted
	approved, err := guard.EvaluateScopeExpansionClaim(claim, true)
	if err != nil {
		t.Fatalf("EvaluateScopeExpansionClaim: %v", err)
	}
	if approved.State != model.ClaimStateVerified {
		t.Fatalf("approved claim state = %s, want %s", approved.State, model.ClaimStateVerified)
	}
}

// Test6_ChangedFilesOutsideScopeCannotReachMergeReady:
// Changed files outside allowed scope cannot reach merge-ready.
func Test6_ChangedFilesOutsideScopeCannotReachMergeReady(t *testing.T) {
	ctx := context.Background()
	guard := alignment.NewGuard()
	goal := baseGoal() // Scope: ["internal/auth"]

	observedFiles := []string{
		"internal/auth/token.go",
		"internal/crypto/rsa.go", // OUTSIDE SCOPE!
	}

	err := guard.ValidateMergeReadiness(
		ctx,
		goal,
		1,
		[]string{"internal/auth/token.go"},
		observedFiles,
		nil,
		"",
		false,
	)

	if err == nil {
		t.Fatalf("expected error when changed files fall outside scope, got nil")
	}
	if !strings.Contains(err.Error(), "outside allowed Goal scope") {
		t.Fatalf("expected scope violation error, got: %v", err)
	}
}

// Test7_GoalV2ChangesScopeGuardUsesNewRevision:
// Goal v2 changes scope and guard uses new revision only after authorized transition.
func Test7_GoalV2ChangesScopeGuardUsesNewRevision(t *testing.T) {
	ctx := context.Background()
	guard := alignment.NewGuard()
	goalV1 := baseGoal() // Scope: ["internal/auth"], Revision: 1

	// Goal v2 expands scope to include internal/crypto
	goalV2 := goalV1
	goalV2.Revision = 2
	goalV2.Scope = []string{"internal/auth", "internal/crypto"}

	observedFiles := []string{
		"internal/auth/token.go",
		"internal/crypto/key.go",
	}

	// Task running against stale revision 1 fails with GoalDrift or ScopeViolation
	err := guard.ValidateMergeReadiness(
		ctx,
		goalV2,
		1, // Task executed against stale revision 1!
		[]string{"internal/auth/token.go"},
		observedFiles,
		nil,
		"",
		false,
	)
	if err == nil {
		t.Fatalf("expected error when task executed against stale Goal v1, got nil")
	}
	if !strings.Contains(err.Error(), "goal version drift") {
		t.Fatalf("expected goal version drift error, got: %v", err)
	}

	// Task running against authorized revision 2 succeeds
	err = guard.ValidateMergeReadiness(
		ctx,
		goalV2,
		2, // Correct current revision!
		[]string{"internal/auth/token.go", "internal/crypto/key.go"},
		observedFiles,
		nil,
		"",
		false,
	)
	if err != nil {
		t.Fatalf("expected task running against Goal v2 to pass, got error: %v", err)
	}
}
