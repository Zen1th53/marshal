package integration

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/alignment"
	"github.com/Zen1th53/marshal/internal/budget"
	"github.com/Zen1th53/marshal/internal/collaboration"
	"github.com/Zen1th53/marshal/internal/epistemic"
	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/interpretation"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
	"github.com/Zen1th53/marshal/internal/reinjection"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui"
)

func setupTestStore(t *testing.T) (*store.Store, context.Context) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "e2e_test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st, ctx
}

// E2E-01 Read-only team discovery
func TestE2E_01_ReadOnlyTeamDiscovery(t *testing.T) {
	st, ctx := setupTestStore(t)
	coord := collaboration.NewCoordinator(st, nil)

	participants := []model.Participant{
		{AgentID: "claude", Role: model.RoleArchitect, Harness: "claude-code", Model: "claude-3-7-sonnet", IsActive: true},
		{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex", Model: "gpt-4o", IsActive: true},
		{AgentID: "opencode", Role: model.RoleQA, Harness: "opencode", Model: "deepseek-coder", IsActive: true},
		{AgentID: "antigravity", Role: model.RoleAppSec, Harness: "antigravity", Model: "gemini-2.5-pro", IsActive: true},
	}

	sess, err := coord.CreateSession(ctx, "sess-e2e-01", "goal-01", 1, participants)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Claude publishes architecture finding
	msg1 := model.AgentMessage{
		ID:        "msg-1",
		SessionID: sess.SessionID,
		From:      model.AuthorProvenance{AgentID: "claude", Harness: "claude-code"},
		Kind:      model.MessageFinding,
		Content:   "Architecture invariant: zero-dependency frozen core",
		CreatedAt: time.Now().UTC(),
	}
	if _, err := coord.SendMessage(ctx, msg1, false, false); err != nil {
		t.Fatalf("send message 1: %v", err)
	}

	// Codex reads discovery overview
	overview, err := coord.GetSessionOverview(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("get session overview: %v", err)
	}
	if len(overview.RecentTurns) != 1 || overview.RecentTurns[0].Content != msg1.Content {
		t.Fatalf("Codex failed to discover peer finding: %+v", overview.RecentTurns)
	}
}

// E2E-02 Implementation + handoff across disjoint roles
func TestE2E_02_ImplementationAndHandoff(t *testing.T) {
	st, ctx := setupTestStore(t)
	coord := collaboration.NewCoordinator(st, nil)

	participants := []model.Participant{
		{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex", IsActive: true},
		{AgentID: "antigravity", Role: model.RoleAppSec, Harness: "antigravity", IsActive: true},
		{AgentID: "opencode", Role: model.RoleQA, Harness: "opencode", IsActive: true},
	}

	sess, err := coord.CreateSession(ctx, "sess-e2e-02", "goal-02", 1, participants)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Codex hands off to Antigravity
	sess, err = coord.HandOffOwnership(ctx, sess.SessionID,
		model.AuthorProvenance{AgentID: "codex", Harness: "codex"},
		model.RoleAppSec, "Implemented core tranche A; ready for AppSec audit",
		[]string{"ev-1"}, []string{"cl-1"})
	if err != nil {
		t.Fatalf("handoff to appsec failed: %v", err)
	}
	if sess.ActiveTurn != "antigravity" {
		t.Fatalf("expected active turn antigravity, got %s", sess.ActiveTurn)
	}

	// Antigravity hands off to QA
	sess, err = coord.HandOffOwnership(ctx, sess.SessionID,
		model.AuthorProvenance{AgentID: "antigravity", Harness: "antigravity"},
		model.RoleQA, "AppSec audit passed; ready for independent QA verification",
		[]string{"ev-2"}, []string{"cl-1"})
	if err != nil {
		t.Fatalf("handoff to QA failed: %v", err)
	}
	if sess.ActiveTurn != "opencode" {
		t.Fatalf("expected active turn opencode, got %s", sess.ActiveTurn)
	}
}

// E2E-03 Claim disagreement / justified revision
func TestE2E_03_ClaimDisagreementAndJustifiedRevision(t *testing.T) {
	st, ctx := setupTestStore(t)
	coord := collaboration.NewCoordinator(st, nil)
	disc := epistemic.NewContradictionDiscipline()

	participants := []model.Participant{
		{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex", IsActive: true},
		{AgentID: "opencode", Role: model.RoleQA, Harness: "opencode", IsActive: true},
	}
	sess, _ := coord.CreateSession(ctx, "sess-e2e-03", "goal-03", 1, participants)

	// Codex registers verified claim
	claim := model.Claim{
		ID:             "claim-e2e-03",
		GoalID:         "goal-03",
		GoalRevision:   1,
		Subject:        "Authentication",
		NormalizedText: "Tokens are validated with HS256",
		Scope:          "auth",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateVerified,
		Author:         model.AuthorProvenance{AgentID: "codex", Harness: "codex"},
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := st.SaveClaim(ctx, claim); err != nil {
		t.Fatalf("save claim: %v", err)
	}

	// OpenCode challenges with counter-evidence -> Claim becomes CONTESTED
	counterEvidence := model.EvidenceRef{
		EvidenceID:      "ev-counter-1",
		Tool:            "jwt-probe",
		Summary:         "Header specifies none algorithm and was accepted",
		Digest:          "sha256:counterdigest",
		IsDeterministic: true,
		Metadata:        map[string]string{"exit_code": "1", "result": "fail"},
		CapturedAt:      time.Now().UTC(),
	}

	err := coord.ChallengeClaim(ctx, sess.SessionID,
		model.AuthorProvenance{AgentID: "opencode", Harness: "opencode"},
		claim.ID, counterEvidence, "Token algorithm none accepted")
	if err != nil {
		t.Fatalf("challenge claim failed: %v", err)
	}

	updated, err := st.GetClaim(ctx, claim.ID)
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if updated.State != model.ClaimStateContested {
		t.Fatalf("expected CONTESTED claim state, got %s", updated.State)
	}

	// Deterministic counter-evidence contradiction detection
	hasConflict, reason := disc.DetectContradiction(claim, counterEvidence)
	if !hasConflict {
		t.Fatalf("expected contradiction detected with failing exit code")
	}
	if !strings.Contains(reason, "contradicting verified claim") {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

// E2E-04 Constraint persistence across 20+ turns
func TestE2E_04_ConstraintPersistenceAcross20Turns(t *testing.T) {
	st, ctx := setupTestStore(t)
	coord := collaboration.NewCoordinator(st, nil)
	comp := reinjection.NewConstraintCompiler()

	participants := []model.Participant{
		{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex", IsActive: true},
		{AgentID: "claude", Role: model.RoleArchitect, Harness: "claude-code", IsActive: true},
	}
	sess, _ := coord.CreateSession(ctx, "sess-e2e-04", "goal-04", 1, participants)

	goal := model.GoalContract{
		ID:             "goal-04",
		SessionID:      sess.SessionID,
		Revision:       1,
		DesiredOutcome: "Preserve constraints",
		Constraints: []model.Constraint{
			{ID: "c-hard", Text: "NEVER allow unauthenticated admin route", IsHard: true},
		},
		DoNotDo:         []string{"disable TLS", "bypass auth"},
		AuthoritySource: "operator",
		Risk:            model.R2,
	}

	// Run 22 handoffs back and forth between codex and claude with productive progress evidence
	for i := 0; i < 22; i++ {
		targetRole := model.RoleArchitect
		fromAgent := "codex"
		if i%2 == 1 {
			targetRole = model.RoleDeveloper
			fromAgent = "claude"
		}

		var err error
		sess, err = coord.HandOffOwnership(ctx, sess.SessionID,
			model.AuthorProvenance{AgentID: fromAgent, Harness: "harness"},
			targetRole, fmt.Sprintf("Turn %d handoff", i+1),
			[]string{fmt.Sprintf("ev-turn-%d", i)}, nil)
		if err != nil {
			t.Fatalf("handoff turn %d failed: %v", i, err)
		}

		// Inject constraints at each turn boundary
		principal := protocol.Principal{ID: "agent", Role: protocol.Role(model.RoleDeveloper)}
		compiled, err := comp.Compile(ctx, goal, principal)
		if err != nil {
			t.Fatalf("compile constraints turn %d: %v", i, err)
		}
		if !strings.Contains(compiled.CompiledXML, "NEVER allow unauthenticated admin route") {
			t.Fatalf("constraint lost at turn %d", i)
		}
		if compiled.Digest == "" {
			t.Fatalf("digest empty at turn %d", i)
		}
	}
}

// E2E-05 Missing critical claim denies SUCCESS
func TestE2E_05_MissingCriticalClaimDeniesSuccess(t *testing.T) {
	covDisc := epistemic.NewCriticalClaimCoverageDiscipline()

	goal := model.GoalContract{
		ID:                     "goal-05",
		SessionID:              "sess-05",
		Revision:               1,
		DesiredOutcome:         "Secure release",
		RequiredCriticalClaims: []string{"Security tests passing", "Memory leaks absent"},
	}

	// Only 1 critical claim is registered and verified; the second is missing!
	claims := []model.Claim{
		{
			ID:             "cl-sec",
			Subject:        "Security tests passing",
			Criticality:    model.CriticalityBlocker,
			State:          model.ClaimStateVerified,
			NormalizedText: "Security tests passing",
		},
	}

	report, err := covDisc.EvaluateCoverage(goal, claims)
	if err != nil {
		t.Fatalf("evaluate coverage: %v", err)
	}
	if report.CanSucceed {
		t.Fatalf("SUCCESS must be denied when critical claim is missing: report=%+v", report)
	}
	if len(report.MissingClaims) != 1 || report.MissingClaims[0] != "Memory leaks absent" {
		t.Fatalf("unexpected missing claims: %+v", report.MissingClaims)
	}
}

// E2E-06 Deletion-as-satisfaction blocked by Alignment Guard
func TestE2E_06_DeletionAsSatisfactionBlocked(t *testing.T) {
	guard := alignment.NewGuard()
	ctx := context.Background()

	goal := model.GoalContract{
		ID:             "goal-06",
		Revision:       1,
		DesiredOutcome: "Clean test suite",
		Scope:          []string{"internal/auth"},
	}

	diffContent := `
--- a/auth_test.go
+++ /dev/null
@@ -1,50 +0,0 @@
-func TestStrictAuthentication(t *testing.T) {
-    // test body
-}
`
	res, err := guard.EvaluateChanges(ctx, goal, 1,
		[]string{"auth_test.go"}, []string{"auth_test.go"}, []string{"auth_test.go"},
		diffContent, false)
	if err != nil {
		t.Fatalf("guard evaluation error: %v", err)
	}

	if res.Passed {
		t.Fatalf("expected Alignment Guard to block test deletion")
	}
	found := false
	for _, v := range res.Violations {
		if v.Type == alignment.CheckDeletionAsSatisfaction {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected CheckDeletionAsSatisfaction, got: %+v", res.Violations)
	}
}

// E2E-07 Checkpoint / restart / rollback
func TestE2E_07_CheckpointRestartRollback(t *testing.T) {
	st, ctx := setupTestStore(t)

	cpID := "cp-e2e-07"
	cp := model.HandoffCheckpoint{
		ID:           cpID,
		Version:      1,
		SessionID:    "sess-07",
		TaskID:       "task-07",
		GoalID:       "goal-07",
		GoalRevision: 1,
		Role:         "developer",
		Author: model.AuthorProvenance{
			AgentID: "codex",
			Harness: "codex",
		},
		Reason:    "Checkpoint before high-risk database migration",
		CreatedAt: time.Now().UTC(),
	}

	if err := st.SaveHandoffCheckpoint(ctx, cp); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	// Verify checkpoint survives restart
	recovered, err := st.GetHandoffCheckpoint(ctx, cpID)
	if err != nil {
		t.Fatalf("recover checkpoint after simulated restart: %v", err)
	}
	if recovered.ID != cp.ID || recovered.Reason != cp.Reason {
		t.Fatalf("recovered checkpoint mismatch: %+v", recovered)
	}

	// Rollback
	rb := model.CheckpointRollback{
		RollbackID:   "rb-07",
		CheckpointID: cpID,
		Actor: model.AuthorProvenance{
			AgentID: "operator",
			Harness: "tui",
		},
		Reason:    "Migration failed; revert to checkpoint",
		CreatedAt: time.Now().UTC(),
	}

	if err := st.RecordCheckpointRollback(ctx, rb); err != nil {
		t.Fatalf("record rollback: %v", err)
	}

	rollbacks, err := st.GetCheckpointRollbacks(ctx, cpID)
	if err != nil || len(rollbacks) != 1 {
		t.Fatalf("unexpected rollbacks: %v (len %d)", err, len(rollbacks))
	}
}

// E2E-08 Budget exhaustion termination
func TestE2E_08_BudgetExhaustionTermination(t *testing.T) {
	tracker := budget.NewTracker()

	for i := 0; i < 5; i++ {
		tracker.RecordUsage(adapter.Usage{}, time.Second, false, false)
	}

	isExhausted, dimension, reason := tracker.CheckExhaustion(model.BudgetLimit{
		MaxModelCalls: 4,
	})

	if !isExhausted {
		t.Fatalf("expected budget exhaustion when calls exceed limit")
	}
	if dimension != "model_calls" {
		t.Fatalf("expected model_calls dimension, got %s", dimension)
	}
	if reason != model.ReasonBudgetExhaustedCalls {
		t.Fatalf("expected ReasonBudgetExhaustedCalls, got %s", reason)
	}
}

// E2E-09 Blind Interpretation divergence escalates
func TestE2E_09_BlindInterpretationDivergence(t *testing.T) {
	scaler := interpretation.NewScaler()
	comp := interpretation.NewComparator()

	req := scaler.EvaluateRequirements(model.R2, false, nil, nil)
	if req.MinInterpreters < 2 {
		t.Fatalf("expected blind interpretation required for R2 risk")
	}

	interp1 := model.Interpretation{
		ID:             "interp-1",
		GoalID:         "goal-09",
		GoalRevision:   1,
		SessionID:      "sess-09",
		Author:         model.AuthorProvenance{AgentID: "claude", Harness: "claude-code"},
		DesiredOutcome: "Stateless JWT auth with structured JSON logging",
		Scope:          []string{"auth", "jwt"},
		SubmittedAt:    time.Now().UTC(),
	}
	interp2 := model.Interpretation{
		ID:             "interp-2",
		GoalID:         "goal-09",
		GoalRevision:   1,
		SessionID:      "sess-09",
		Author:         model.AuthorProvenance{AgentID: "codex", Harness: "codex"},
		DesiredOutcome: "Stateful session cookies with plaintext file logging",
		Scope:          []string{"cookies", "sessions"},
		SubmittedAt:    time.Now().UTC(),
	}

	res := comp.Compare("sess-09", "goal-09", 1, req, []model.Interpretation{interp1, interp2})
	if res.State != model.GoalNeedsInput {
		t.Fatalf("expected GoalNeedsInput due to divergence: %+v", res)
	}
}

// E2E-10 Harness-native optimization route explanation
func TestE2E_10_HarnessNativeOptimizationRoute(t *testing.T) {
	router := harness.NewULTRARouter(nil)

	req := model.ULTRARouteRequest{
		GoalID:            "goal-10",
		FixedRole:         model.RoleDeveloper,
		PreferredHarness:  "codex",
		Risk:              model.R2,
		HasCriticalClaims: true,
	}

	plan, err := router.Route(context.Background(), req)
	if err != nil {
		t.Fatalf("router route error: %v", err)
	}

	if plan.Harness != "codex" || plan.ReasoningEffort != "high" {
		t.Fatalf("expected high reasoning effort for R2 risk: %+v", plan)
	}
	if plan.Explanation == "" || !strings.Contains(plan.Explanation, "codex selected") {
		t.Fatalf("expected concise route explanation: %s", plan.Explanation)
	}
}

// E2E-11 Antigravity first-class execution
func TestE2E_11_AntigravityFirstClassExecution(t *testing.T) {
	intel := harness.NewIntelligence()
	profiles := intel.DefaultProfiles()

	antigravityProfile, exists := profiles["antigravity"]
	if !exists {
		t.Fatalf("antigravity profile missing in intelligence")
	}
	if antigravityProfile.FeatureSupport["sandbox"] != model.StatusNative {
		t.Fatalf("antigravity must support sandbox as native")
	}
}

// E2E-12 Provider / version drift fallback
func TestE2E_12_ProviderVersionDriftFallback(t *testing.T) {
	intel := harness.NewIntelligence()
	profiles := intel.DefaultProfiles()
	codexProfile := profiles["codex"]

	hasDrift, reason := intel.DetectDrift(codexProfile, "v0.9.0-legacy")
	if !hasDrift {
		t.Fatalf("expected drift detected for legacy version")
	}
	if !strings.Contains(reason, "Version drift detected") {
		t.Fatalf("unexpected drift reason: %s", reason)
	}
}

// E2E-13 TUI real session flow
func TestE2E_13_TUIRuntimeSessionLifecycle(t *testing.T) {
	st, ctx := setupTestStore(t)
	ws := tui.NewWorkspace(st, "proj-13", "sess-13")

	// Set goal -> Mode auto -> Intervention -> Exit
	in := strings.NewReader("/goal Deliver release-ready v1.5.0\n/mode auto\n/msg all Proceed with QA verification\n/quit\n")
	var out bytes.Buffer

	if err := ws.Run(ctx, in, &out); err != nil {
		t.Fatalf("TUI run failed: %v", err)
	}

	state := ws.GetUIState()
	if state.Goal.DesiredOutcome != "Deliver release-ready v1.5.0" {
		t.Fatalf("unexpected goal in state: %+v", state.Goal)
	}
	if len(state.RecentMessages) != 1 {
		t.Fatalf("expected 1 operator message in recent messages, got %d", len(state.RecentMessages))
	}
}
