//go:build linux

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

// The real binary sends an operator message and hands the turn over in an
// existing team session, and both survive a restart with the owner as sender.
func TestPTYCollaborationMessageAndHandoff(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	const session = "SESSION-pty-collab"
	ctx := context.Background()
	runtime, err := app.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := runtime.Store().SaveTeamSession(ctx, model.TeamSession{SessionID: session, GoalID: "GOAL-pty", GoalRevision: 1,
		Participants: []model.Participant{
			{AgentID: "AGENT-dev", Role: model.RoleDeveloper, Harness: "codex", IsActive: true},
			{AgentID: "AGENT-qa", Role: model.RoleQA, Harness: "claude", IsActive: true},
		}, ActiveTurn: "AGENT-dev", TurnSequence: 1, Status: "ACTIVE", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	terminal := startFrozenTUIInProject(t, 40, 180, bin, project, "tui", session)
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/msg AGENT-qa look at the retry path")
	terminal.mustSee("sent to AGENT-qa")
	terminal.sendLine("/handoff qa ready for review")
	terminal.mustSee("Turn handed to AGENT-qa (qa); session turn 2")

	reopened, err := app.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	messages, err := reopened.Store().ListAgentMessages(ctx, session, 10)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages after restart: %+v %v", messages, err)
	}
	for _, m := range messages {
		if !strings.HasPrefix(m.From.AgentID, "local-uid:") {
			t.Fatalf("sender is not the authenticated owner: %+v", m.From)
		}
	}
	stored, err := reopened.Store().GetTeamSession(ctx, session)
	if err != nil || stored.ActiveTurn != "AGENT-qa" || stored.TurnSequence != 2 {
		t.Fatalf("session after restart: %+v %v", stored, err)
	}
}
