package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func collaborationFixture(t *testing.T) (*Runtime, context.Context, string) {
	t.Helper()
	r := runtimeForPlan(t)
	local, err := r.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	const session = "collab-session"
	now := time.Now().UTC()
	if err := r.Store().SaveTeamSession(context.Background(), model.TeamSession{SessionID: session, GoalID: "GOAL-collab", GoalRevision: 1,
		Participants: []model.Participant{
			{AgentID: "AGENT-dev", Role: model.RoleDeveloper, Harness: "codex", IsActive: true},
			{AgentID: "AGENT-qa", Role: model.RoleQA, Harness: "claude", IsActive: true},
		}, ActiveTurn: "AGENT-dev", TurnSequence: 3, Status: "ACTIVE", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return r, local.Context(context.Background()), session
}

func TestCollaborationMessageAndHandoff(t *testing.T) {
	r, owner, session := collaborationFixture(t)
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: session, TargetID: session, ExpectedVersion: 3, IdempotencyKey: "msg-1"}
	msg := CollaborationCommand{Operation: "message", To: "AGENT-qa", Content: "check the edge case password=hunter2"}

	if _, err := r.CommandCollaboration(context.Background(), e, msg); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous message: %v", err)
	}
	sent, err := r.CommandCollaboration(owner, e, msg)
	if err != nil || sent.MessageID == "" || sent.TurnSequence != 3 {
		t.Fatalf("message: %+v %v", sent, err)
	}
	if _, err := r.CommandCollaboration(owner, e, msg); err != nil {
		t.Fatalf("replay: %v", err)
	}
	stored, err := r.Store().ListAgentMessages(context.Background(), session, 10)
	if err != nil || len(stored) != 1 {
		t.Fatalf("replay must not duplicate: %+v %v", stored, err)
	}
	if !strings.HasPrefix(stored[0].From.AgentID, "local-uid:") || stored[0].To != "AGENT-qa" || strings.Contains(stored[0].Content, "hunter2") {
		t.Fatalf("stored message: %+v", stored[0])
	}
	msg.Content = "different text"
	if _, err := r.CommandCollaboration(owner, e, msg); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("key reuse for another message: %v", err)
	}

	stranger := CommandEnvelope{ProjectID: e.ProjectID, SessionID: session, TargetID: session, ExpectedVersion: 3, IdempotencyKey: "msg-2"}
	if _, err := r.CommandCollaboration(owner, stranger, CollaborationCommand{Operation: "message", To: "AGENT-nobody", Content: "hi"}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("non-participant recipient: %v", err)
	}

	handoff := CommandEnvelope{ProjectID: e.ProjectID, SessionID: session, TargetID: session, ExpectedVersion: 3, IdempotencyKey: "handoff-1"}
	moved, err := r.CommandCollaboration(owner, handoff, CollaborationCommand{Operation: "handoff", TargetRole: model.RoleQA, Content: "please review"})
	if err != nil || moved.ActiveTurn != "AGENT-qa" || moved.TurnSequence != 4 {
		t.Fatalf("handoff: %+v %v", moved, err)
	}
	stale := CommandEnvelope{ProjectID: e.ProjectID, SessionID: session, TargetID: session, ExpectedVersion: 3, IdempotencyKey: "handoff-2"}
	if _, err := r.CommandCollaboration(owner, stale, CollaborationCommand{Operation: "handoff", TargetRole: model.RoleDeveloper, Content: "back"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale handoff: %v", err)
	}
	after, err := r.Store().GetTeamSession(context.Background(), session)
	if err != nil || after.ActiveTurn != "AGENT-qa" || after.TurnSequence != 4 {
		t.Fatalf("stale handoff changed the session: %+v %v", after, err)
	}
	noRole := CommandEnvelope{ProjectID: e.ProjectID, SessionID: session, TargetID: session, ExpectedVersion: 4, IdempotencyKey: "handoff-3"}
	if _, err := r.CommandCollaboration(owner, noRole, CollaborationCommand{Operation: "handoff", TargetRole: model.RoleAppSec, Content: "x"}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("handoff to a role nobody holds: %v", err)
	}
}

func TestCollaborationNeverCreatesASession(t *testing.T) {
	r, owner, _ := collaborationFixture(t)
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "missing", TargetID: "missing", IdempotencyKey: "k"}
	if _, err := r.CommandCollaboration(owner, e, CollaborationCommand{Operation: "message", Content: "hello"}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	if _, err := r.Store().GetTeamSession(context.Background(), "missing"); err == nil {
		t.Fatal("a session was created on the operator's behalf")
	}
}

func TestCollaborationRequiresItsCapability(t *testing.T) {
	r, owner, session := collaborationFixture(t)
	grants, err := r.Store().ListCapabilityGrants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grants {
		if len(g.Scope.Actions) == 1 && g.Scope.Actions[0] == "collab.message" {
			if err := r.Store().RevokeCapabilityGrant(context.Background(), g.ID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: session, TargetID: session, ExpectedVersion: 3, IdempotencyKey: "k"}
	if _, err := r.CommandCollaboration(owner, e, CollaborationCommand{Operation: "message", Content: "hello"}); err == nil {
		t.Fatal("message sent after its capability was revoked")
	}
}
