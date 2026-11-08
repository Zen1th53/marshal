package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// TestRefreshStateReappliesAuthUnavailable proves RefreshState re-marks an
// unsigned-in provider as UNAVAILABLE after the TeamSession reload resets its
// activity, so text surfaces such as /agents keep reporting it truthfully.
func TestRefreshStateReappliesAuthUnavailable(t *testing.T) {
	// A fake installed codex keeps the harness probe from downgrading it on its
	// own, so the only way codex becomes active again after being marked
	// unsigned-in is the TeamSession reload in RefreshState.
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	InvalidateProbeCache()
	t.Cleanup(InvalidateProbeCache)
	t.Cleanup(func() {
		authUnavailableMu.Lock()
		authUnavailableState = make(map[string]map[string]string)
		authUnavailableMu.Unlock()
	})

	st, ws, ctx := newControlWorkspace(t)

	now := time.Now().UTC()
	if err := st.SaveTeamSession(ctx, model.TeamSession{
		SessionID:    ws.sessionID,
		GoalID:       "GOAL-auth-refresh",
		GoalRevision: 1,
		Participants: []model.Participant{
			{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex", Model: UnknownModel, IsActive: true},
		},
		ActiveTurn:   "codex",
		TurnSequence: 1,
		Status:       "ACTIVE",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	ws.mu.Lock()
	ws.state.Participants = []model.Participant{
		{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex", Model: UnknownModel, IsActive: true},
	}
	ws.mu.Unlock()

	ws.markProviderAuthUnavailable("codex", providerNotSignedInMessage("codex"))

	if err := ws.RefreshState(ctx); err != nil {
		t.Fatal(err)
	}

	p, ok := authNontmuxParticipant(ws, "codex")
	if !ok {
		t.Fatal("codex participant missing after refresh")
	}
	if p.IsActive || p.Model != UnavailableModel {
		t.Fatalf("expected codex UNAVAILABLE after refresh, got IsActive=%v Model=%q", p.IsActive, p.Model)
	}
}
