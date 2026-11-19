package tui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestMarshalProviderSwitchCarriesActiveGoalHandoff(t *testing.T) {
	setupFakeTmux(t)
	marshalSwitchEnvironment(t)
	t.Setenv("HOME", t.TempDir())
	_, w, _ := acceptanceWorkspace(t)
	cleanupTmuxWorkspace(t, w)
	root := w.providerRoot()
	w.tmuxSession = fixtureTmuxNamespace(t, root)
	w.tmuxMarshalWin = "marshal"
	w.tmuxPath = "tmux"
	w.tmuxActiveWins = make(map[string]*activeTmuxAgent)

	// Start the Marshal on Antigravity (agy), then establish an active goal,
	// confirmed requirements, an approved design record and an open decision.
	if err := saveDefaultProvider(root, "agy"); err != nil {
		t.Fatal(err)
	}
	if err := w.startMarshalChat(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	goal := model.GoalContract{
		ID:                 "GOAL-switch-1",
		SessionID:          w.sessionID,
		Revision:           1,
		OriginalRequest:    "Add OAuth2 device login to the CLI",
		DesiredOutcome:     "Support OAuth2 device-flow login with secure token refresh",
		Scope:              []string{"internal/auth", "internal/cli"},
		SuccessCriteria:    []string{"go test ./internal/auth passes"},
		Risk:               model.R1,
		AuthoritySource:    "operator:test",
		Confirmation:       model.ConfirmationApproved,
		UnderstandingState: model.GoalReady,
		UnresolvedDecisions: []model.UnresolvedDecision{
			{ID: "dec-1", Question: "Keep legacy API-key login alongside OAuth2?", Options: []string{"keep", "remove"}, RequiresUser: true},
		},
		CreatedAt: now.Add(-10 * time.Minute),
		UpdatedAt: now.Add(-10 * time.Minute),
	}
	if err := w.store.SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatalf("save active goal: %v", err)
	}

	// Simulate the owner confirming the 8-line requirements card on the Board.
	confirmed := [8]string{
		"OAuth2 device login in internal/auth and internal/cli",
		"do not touch billing or telemetry",
		"base main, land on main",
		"go test ./internal/auth passes",
		"existing CLI auth tests pass",
		"additive login subcommand only",
		"no new external dependencies",
		"standard budget",
	}
	w.req.mu.Lock()
	w.req.confirmed = &confirmed
	w.req.mu.Unlock()

	// Record an approved design artifact for this goal.
	designBody, err := json.Marshal(map[string]any{
		"provenance": map[string]any{"stage": "approved_design", "provider": "agy", "egm_version": "builtin", "path": "design/oauth.md"},
		"content":    "Device-code poller in internal/auth with OS keychain token storage",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.store.WriteMemoryV2(t.Context(), model.MemoryRecordV2{
		ID:         "MEM-design-oauth",
		ProjectID:  w.projectID,
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryCandidate,
		Title:      "EGM approved_design: design/oauth.md",
		Body:       string(designBody),
		Scope:      "project",
		ScopeID:    w.projectID,
		Source:     model.MemorySource{Kind: "egm", Reference: "egm:approved_design"},
		ObservedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("write design record: %v", err)
	}

	// Simulate an open question waiting for the owner.
	w.ask.mu.Lock()
	w.ask.card = &askCard{
		Question: "Should expired refresh tokens prompt automatically?",
		Choices:  []askChoice{{Key: "enter", Label: "prompt"}, {Key: "f", Label: "fail fast"}},
		At:       now,
	}
	w.ask.mu.Unlock()

	// Switch the Marshal from Antigravity to Codex mid-goal.
	msg, err := w.ExecuteCommand(context.Background(), "/marshal model codex")
	if err != nil || msg != "The Marshal is now Codex." {
		t.Fatalf("switch to codex: %q %v", msg, err)
	}

	w.tmuxMu.Lock()
	chat := copyAgentLocked(w.tmuxActiveWins["marshal-chat"])
	w.tmuxMu.Unlock()
	if chat == nil || chat.provider != "codex" {
		t.Fatalf("expected new codex Marshal chat, got %+v", chat)
	}
	// The visible opening cue stays neutral ("Hello again.") and never leaks
	// the handoff or protocol text into the conversation turn.
	visible := chat.args[len(chat.args)-1]
	if visible != marshalKickoffContinue {
		t.Fatalf("visible opening = %q, want %q", visible, marshalKickoffContinue)
	}

	// Extract Codex's hidden developer_instructions briefing.
	var hidden string
	for i := 0; i < len(chat.args)-1; i++ {
		if chat.args[i] == "-c" && strings.HasPrefix(chat.args[i+1], "developer_instructions=") {
			hidden = strings.TrimPrefix(chat.args[i+1], "developer_instructions=")
			break
		}
	}
	if hidden == "" {
		t.Fatalf("missing codex developer_instructions in args: %q", chat.args)
	}
	for _, want := range []string{
		app.MarshalGoalHandoffHeader,
		"Antigravity to Codex",
		"do not record a new Goal",
		goal.ID,
		goal.DesiredOutcome,
		"design approved",
		"a (scope): OAuth2 device login in internal/auth and internal/cli",
		"go test ./internal/auth passes",
		"design/oauth.md",
		"Device-code poller in internal/auth with OS keychain token storage",
		"Keep legacy API-key login alongside OAuth2?",
		"Should expired refresh tokens prompt automatically?",
	} {
		if !strings.Contains(hidden, want) {
			t.Fatalf("codex handoff briefing missing %q:\n%s", want, hidden)
		}
	}
	if strings.Contains(hidden, "current settled Goal and reviewed accepted project context are unavailable") {
		t.Fatalf("codex briefing falsely claimed no active Goal exists:\n%s", hidden)
	}

	// switchedFrom is consumed once the replacement Marshal chat starts.
	m := w.marshalSession()
	m.mu.Lock()
	leftover := m.switchedFrom
	m.mu.Unlock()
	if leftover != "" {
		t.Fatalf("switchedFrom not cleared after switch launch: %q", leftover)
	}
}

func TestMarshalGoalHandoffIdempotentAndIsolatedFromWorktree(t *testing.T) {
	ws, rt := realControlWorkspace(t, "SESSION-handoff-iso", false)
	root := rt.ProjectRoot()

	// Without an active goal, no handoff block is added.
	if brief := ws.marshalContinuityBriefing(root, "Marshal protocol."); strings.Contains(brief, app.MarshalGoalHandoffHeader) {
		t.Fatalf("unexpected handoff without active goal:\n%s", brief)
	}

	now := time.Now().UTC()
	goal := model.GoalContract{
		ID:              "GOAL-iso-1",
		SessionID:       ws.sessionID,
		Revision:        1,
		DesiredOutcome:  "Keep handoff scoped to project root",
		Risk:            model.R1,
		AuthoritySource: "operator:test",
		Confirmation:    model.ConfirmationApproved,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := ws.store.SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatalf("save goal: %v", err)
	}

	brief := ws.marshalContinuityBriefing(root, "Marshal protocol.")
	if n := strings.Count(brief, app.MarshalGoalHandoffHeader); n != 1 {
		t.Fatalf("expected handoff once, got %d in:\n%s", n, brief)
	}
	// Re-briefing must not duplicate the handoff block.
	briefAgain := ws.marshalContinuityBriefing(root, brief)
	if n := strings.Count(briefAgain, app.MarshalGoalHandoffHeader); n != 1 {
		t.Fatalf("expected handoff still once after re-brief, got %d", n)
	}
	// Worker worktrees and unrelated directories must not receive the Marshal handoff.
	wtRoot := filepath.Join(root, ".marshal", "worktrees", "wt-1")
	if wtBrief := ws.marshalContinuityBriefing(wtRoot, "Worker protocol."); strings.Contains(wtBrief, app.MarshalGoalHandoffHeader) {
		t.Fatalf("worktree briefing leaked Marshal handoff:\n%s", wtBrief)
	}
	if otherBrief := ws.marshalContinuityBriefing(t.TempDir(), "Protocol."); strings.Contains(otherBrief, app.MarshalGoalHandoffHeader) {
		t.Fatalf("other directory leaked Marshal handoff:\n%s", otherBrief)
	}
}
