package tui

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestOneScreenObservabilityRendering(t *testing.T) {
	tokens := int64(12450)
	cost := 0.0425
	state := UIState{
		ProjectID:          "test-proj",
		SessionID:          "sess-12345",
		SessionMode:        "ULTRA",
		UnderstandingState: model.GoalReady,
		TerminationState:   model.StateSuccess,
		Goal: model.GoalContract{
			ID:             "goal-alpha",
			Revision:       2,
			DesiredOutcome: "Complete end-to-end integration and release gate",
			Constraints: []model.Constraint{
				{ID: "c1", Text: "Preserve backward compatibility"},
				{ID: "c2", Text: "Fail closed on security error"},
			},
		},
		Participants: []model.Participant{
			{AgentID: "claude", Role: model.RoleArchitect, Harness: "claude-code", Model: "claude-3-7-sonnet", IsActive: true},
			{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex", Model: "gpt-4o", IsActive: true},
		},
		ActiveTurn: "codex",
		Claims: []model.Claim{
			{ID: "cl-1", State: model.ClaimStateVerified, NormalizedText: "Zero dependency core"},
			{ID: "cl-2", State: model.ClaimStateContested, NormalizedText: "Network access allowed"},
		},
		RecentMessages: []model.AgentMessage{
			{
				ID:        "m1",
				From:      model.AuthorProvenance{AgentID: "claude", Harness: "claude-code"},
				Kind:      model.MessageFinding,
				Content:   "Architecture invariant preserved",
				CreatedAt: time.Now().UTC(),
			},
		},
		BudgetConsumed: model.ConsumedBudget{
			TotalTokens: &tokens,
			CostUSD:     &cost,
			ModelCalls:  4,
			Handoffs:    1,
			Duration:    15 * time.Second,
		},
		RouteExplanation: "codex selected: gpt-4o with strict sandboxing",
	}

	rendered := RenderScreen(state, 100)

	// 1. Goal revision & outcome
	if !strings.Contains(rendered, "GOAL [v2]") || !strings.Contains(rendered, "Complete end-to-end integration") {
		t.Fatalf("expected goal outcome in screen:\n%s", rendered)
	}

	// 2. Session mode & state
	if !strings.Contains(rendered, "[ULTRA]") || !strings.Contains(rendered, "READY / SUCCESS") {
		t.Fatalf("expected mode and state in screen:\n%s", rendered)
	}

	// 3. Participants & fixed roles
	if !strings.Contains(rendered, "claude") || !strings.Contains(rendered, "architect") ||
		!strings.Contains(rendered, "codex") || !strings.Contains(rendered, "developer") {
		t.Fatalf("expected participants with fixed roles in screen:\n%s", rendered)
	}

	// 4. Claims coverage
	if !strings.Contains(rendered, "Verified: 1") || !strings.Contains(rendered, "Contested: 1") {
		t.Fatalf("expected claim coverage counts in screen:\n%s", rendered)
	}

	// 5. Budget summary
	if !strings.Contains(rendered, "12450") || !strings.Contains(rendered, "$0.0425") {
		t.Fatalf("expected budget summary in screen:\n%s", rendered)
	}

	// 6. Activity stream
	if !strings.Contains(rendered, "[FINDING]") || !strings.Contains(rendered, "Architecture invariant preserved") {
		t.Fatalf("expected activity stream in screen:\n%s", rendered)
	}

	// 7. Route explanation
	if !strings.Contains(rendered, "codex selected") {
		t.Fatalf("expected route explanation in screen:\n%s", rendered)
	}
}

func TestSilenceByDefaultMessageFiltering(t *testing.T) {
	msgs := []model.AgentMessage{
		{ID: "1", Kind: model.MessageQuestion, Content: "Chatter question 1"},
		{ID: "2", Kind: model.MessageAnswer, Content: "Chatter answer 1"},
		{ID: "3", Kind: model.MessageFinding, Content: "Crucial finding on schema"},
		{ID: "4", Kind: model.MessageHandoffProposal, Content: "Handoff to QA for testing"},
		{ID: "5", Kind: model.MessageClaimChallenge, Content: "Disagreement on claim X"},
	}

	filtered := filterMeaningfulMessages(msgs, 5)

	if len(filtered) != 3 {
		t.Fatalf("expected 3 meaningful messages filtered, got %d", len(filtered))
	}
	for _, m := range filtered {
		if m.Kind == model.MessageQuestion || m.Kind == model.MessageAnswer {
			t.Fatalf("raw chatter %s was not silenced", m.Kind)
		}
	}
}

func TestSecretRedaction(t *testing.T) {
	raw := "Agent sent token Bearer eyJhbGciOiJIUzI1NiJ9.test and secret sk-1234567890abcdef123456 and api_key: 'supersecretpass'"
	redacted := RedactContent(raw, []string{"supersecretpass"})

	if strings.Contains(redacted, "eyJhbGciOiJIUzI1NiJ9.test") {
		t.Fatalf("bearer token leaked: %s", redacted)
	}
	if strings.Contains(redacted, "sk-1234567890abcdef123456") {
		t.Fatalf("sk token leaked: %s", redacted)
	}
	if strings.Contains(redacted, "supersecretpass") {
		t.Fatalf("explicit secret leaked: %s", redacted)
	}
	if !strings.Contains(redacted, "Bearer [REDACTED]") {
		t.Fatalf("expected Bearer [REDACTED] in output: %s", redacted)
	}
	if !strings.Contains(redacted, "sk-[REDACTED]") {
		t.Fatalf("expected sk-[REDACTED] in output: %s", redacted)
	}
}

func TestInteractiveWorkspaceAndCommands(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	sessionID := "sess-interactive-test"
	ws := NewWorkspace(st, "proj-1", sessionID)

	// Test 1: Set goal
	out, err := ws.ExecuteCommand(ctx, "/goal Deliver MARSHAL v1.5.0 terminal TUI")
	if err != nil {
		t.Fatalf("execute /goal error: %v", err)
	}
	if !strings.Contains(out, "Active Goal updated to revision 1") {
		t.Fatalf("unexpected goal output: %s", out)
	}

	// Test 2: Mode switch
	out, err = ws.ExecuteCommand(ctx, "/mode manual")
	if err != nil {
		t.Fatalf("execute /mode error: %v", err)
	}
	if !strings.Contains(out, "MANUAL") {
		t.Fatalf("unexpected mode output: %s", out)
	}

	// Test 3: List agents
	out, err = ws.ExecuteCommand(ctx, "/agents")
	if err != nil {
		t.Fatalf("execute /agents error: %v", err)
	}
	if !strings.Contains(out, "claude") || !strings.Contains(out, "codex") {
		t.Fatalf("unexpected agents output: %s", out)
	}

	// Test 4: Checkpoint creation
	out, err = ws.ExecuteCommand(ctx, "/checkpoint")
	if err != nil {
		t.Fatalf("execute /checkpoint error: %v", err)
	}
	if !strings.Contains(out, "Durable checkpoint created: cp-tui-") {
		t.Fatalf("unexpected checkpoint output: %s", out)
	}
	cpID := strings.TrimSpace(strings.TrimPrefix(out, "Durable checkpoint created: "))

	// Test 5: Rollback
	out, err = ws.ExecuteCommand(ctx, "/rollback "+cpID)
	if err != nil {
		t.Fatalf("execute /rollback error: %v", err)
	}
	if !strings.Contains(out, "Successfully rolled back to checkpoint") {
		t.Fatalf("unexpected rollback output: %s", out)
	}

	// Test 6: Why explanation
	out, err = ws.ExecuteCommand(ctx, "/why")
	if err != nil {
		t.Fatalf("execute /why error: %v", err)
	}
	if !strings.Contains(out, "codex selected for developer") {
		t.Fatalf("unexpected why output: %s", out)
	}

	// Test 7: Budget inspect
	out, err = ws.ExecuteCommand(ctx, "/budget")
	if err != nil {
		t.Fatalf("execute /budget error: %v", err)
	}
	if !strings.Contains(out, "BUDGET CONSUMED") {
		t.Fatalf("unexpected budget output: %s", out)
	}

	// Test 8: Workspace Run loop with /help and /quit
	in := strings.NewReader("/help\n/mode auto\n/quit\n")
	var outBuf bytes.Buffer
	err = ws.Run(ctx, in, &outBuf)
	if err != nil {
		t.Fatalf("Run loop failed: %v", err)
	}
	runOutput := outBuf.String()
	if !strings.Contains(runOutput, "MARSHAL Terminal Workspace Commands") {
		t.Fatalf("expected /help text in Run output:\n%s", runOutput)
	}
	if !strings.Contains(runOutput, "Operating mode switched to AUTO") {
		t.Fatalf("expected mode switch output in Run:\n%s", runOutput)
	}
	if !strings.Contains(runOutput, "Exiting MARSHAL terminal workspace") {
		t.Fatalf("expected exit notice in Run output:\n%s", runOutput)
	}
}
