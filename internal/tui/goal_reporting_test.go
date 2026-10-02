package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestGoalReportsCanonicalHistoryAndErrors(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-goal-reports")
	ctx := context.Background()
	for _, command := range []string{"/goal version", "/goal diff", "/goal criteria", "/goal donotdo", "/goal progress"} {
		out, err := ws.ExecuteCommand(ctx, command)
		if err != nil || !strings.Contains(out, "No active goal") {
			t.Fatalf("%s: %s %v", command, out, err)
		}
	}
	first := model.GoalContract{ID: "goal-reports", SessionID: ws.sessionID, ProjectID: runtime.ProjectIdentity(), Revision: 1, DesiredOutcome: "before", SuccessCriteria: []string{"test passes"}, DoNotDo: []string{"no network"}, Risk: model.Risk("R1"), AuthoritySource: "owner"}
	if err := runtime.Store().SaveGoalContract(ctx, first, 0); err != nil {
		t.Fatal(err)
	}
	out, err := ws.ExecuteCommand(ctx, "/goal diff")
	if err != nil || !strings.Contains(out, "No previous goal revision") {
		t.Fatalf("first revision diff: %s %v", out, err)
	}
	second := first
	second.Revision = 2
	second.DesiredOutcome = "after"
	second.SuccessCriteria = []string{"test passes", "review passes"}
	if err := runtime.Store().SaveGoalContract(ctx, second, 1); err != nil {
		t.Fatal(err)
	}
	// Workspace cache deliberately disagrees with durable state.
	ws.state.Goal = first
	now := time.Now().UTC()
	session := verification.Session{
		ID: "historical-verification", Version: 1, State: verification.Blocked, CreatedAt: now, UpdatedAt: now,
		Binding:  verification.Binding{ProjectID: first.ProjectID, GoalID: first.ID, GoalRevision: 1, PlanID: "historical-plan", PlanVersion: 1, RunID: "historical-run", RunVersion: 1, TreeDigest: "tree", EnvironmentDigest: "env"},
		Criteria: []verification.Criterion{{ID: "test passes", Mandatory: true, ClaimIDs: []string{"test-claim"}}},
		Claims:   []verification.Claim{{ID: "test-claim", CriterionID: "test passes", SemanticScope: []string{"tests"}, EvidenceIDs: []string{"historical-evidence"}}},
		Evidence: []verification.Evidence{{ID: "historical-evidence", ClaimID: "test-claim", Status: verification.StatusPass, ContentDigest: "digest", TreeDigest: "tree", EnvironmentDigest: "env", Attempts: 1, Passes: 1}},
	}
	if err := runtime.Store().CreateVerificationSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ command, want string }{
		{"/goal version 1", "before"}, {"/goal version", "after"},
		{"/goal diff 1 2", "success_criteria_changed\": true"}, {"/goal diff", "before"},
		{"/goal criteria", "review passes"}, {"/goal donotdo", "no network"},
		{"/goal progress", "NOT_RUN"},
		{"/goal progress", "test passes: UNKNOWN — stale goal revision; evidence: [historical-evidence]"},
	} {
		out, err := ws.ExecuteCommand(ctx, tc.command)
		if err != nil || !strings.Contains(out, tc.want) {
			t.Fatalf("%s: %s %v", tc.command, out, err)
		}
	}
	for _, command := range []string{"/goal version 99", "/goal diff 1 99"} {
		_, err := ws.ExecuteCommand(ctx, command)
		if err == nil || !strings.Contains(err.Error(), "revision 99") {
			t.Fatalf("%s: %v", command, err)
		}
	}
	for _, command := range []string{"/goal version zero", "/goal version 0", "/goal version -1", "/goal version 9223372036854775808", "/goal version 1 2", "/goal diff 1", "/goal diff bad 2", "/goal diff 1 2 3", "/goal criteria extra", "/goal donotdo extra", "/goal progress extra", "/goal unknown overwrite"} {
		out, err := ws.ExecuteCommand(ctx, command)
		if err != nil || !strings.Contains(out, "Usage:") {
			t.Fatalf("%s: %s %v", command, out, err)
		}
	}
	revisions, err := runtime.Store().ListGoalRevisions(ctx, first.ID)
	if err != nil || len(revisions) != 2 {
		t.Fatalf("reports mutated history: %+v %v", revisions, err)
	}
	current, err := runtime.Store().GetActiveGoalContract(ctx, ws.sessionID)
	if err != nil || current.Revision != 2 || current.DesiredOutcome != "after" {
		t.Fatalf("reports mutated goal: %+v %v", current, err)
	}
}
