package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// CollaborationCommand is an operator message or handoff. It names a
// recipient and content, never a sender: the sender is the authenticated
// principal.
type CollaborationCommand struct {
	Operation  string // "message" or "handoff"
	To         string // message recipient agent ID, empty for the whole team
	TargetRole model.Role
	Content    string
}

// CollaborationResult is the session as it stands after the command.
type CollaborationResult struct {
	MessageID    string
	ActiveTurn   string
	TurnSequence int64
}

// CommandCollaboration posts an operator message or hands the turn to the
// active participant of a role. The session must already exist; nothing is
// created on the operator's behalf. The envelope's expected version is the
// session's turn sequence, so a handoff aimed at a turn that has moved on is
// refused.
func (r *Runtime) CommandCollaboration(ctx context.Context, e CommandEnvelope, input CollaborationCommand) (CollaborationResult, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || e.ProjectID != r.ProjectIdentity() {
		return CollaborationResult{}, authz.ErrDenied
	}
	var action string
	switch input.Operation {
	case "message":
		action = "collab.message"
	case "handoff":
		action = "collab.handoff"
	default:
		return CollaborationResult{}, model.ErrInvalid
	}
	input.Content = strings.TrimSpace(input.Content)
	if e.SessionID == "" || e.TargetID != e.SessionID || e.ExpectedVersion < 0 || input.Content == "" ||
		strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 ||
		(input.Operation == "handoff" && input.TargetRole == "") {
		return CollaborationResult{}, model.ErrInvalid
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: action}
	decision, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return CollaborationResult{}, err
	}
	payload, err := json.Marshal(struct {
		Envelope CommandEnvelope
		Input    CollaborationCommand
	}{e, input})
	if err != nil {
		return CollaborationResult{}, err
	}
	digest := sha256.Sum256(payload)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	record := store.CommandRecord{ProjectID: e.ProjectID, Actor: p.ID(), Key: hex.EncodeToString(key[:]), Operation: action,
		SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: hex.EncodeToString(digest[:]),
		CapabilityGrantID: decision.CapabilityGrantID}
	if _, found, err := r.store.CommandResult(ctx, record); err != nil {
		return CollaborationResult{}, err
	} else if found {
		return r.collaborationState(ctx, e.SessionID, "")
	}

	session, err := r.store.GetTeamSession(ctx, e.SessionID)
	if err != nil {
		return CollaborationResult{}, fmt.Errorf("%w: collaboration session %s does not exist", model.ErrNotFound, e.SessionID)
	}
	now := time.Now().UTC()
	id, err := model.NewID("MSG-")
	if err != nil {
		return CollaborationResult{}, err
	}
	msg := model.AgentMessage{ID: id, SessionID: e.SessionID, From: model.AuthorProvenance{AgentID: p.ID(), Harness: "tui"},
		Kind: model.MessageQuestion, Content: execution.RedactSecrets(input.Content), CreatedAt: now}
	activeTurn := ""
	if input.Operation == "message" {
		if input.To != "" && !sessionHasParticipant(*session, input.To) {
			return CollaborationResult{}, fmt.Errorf("%w: %s is not a participant of session %s", model.ErrInvalid, input.To, e.SessionID)
		}
		msg.To = input.To
	} else {
		for _, participant := range session.Participants {
			if participant.Role == input.TargetRole && participant.IsActive {
				activeTurn = participant.AgentID
				break
			}
		}
		if activeTurn == "" {
			return CollaborationResult{}, fmt.Errorf("%w: no active participant has role %q", model.ErrInvalid, input.TargetRole)
		}
		msg.Kind = model.MessageHandoffProposal
		msg.To = activeTurn
	}
	if _, err := r.store.CommitCollaborationCommand(ctx, record, msg, activeTurn); err != nil {
		if _, found, replayErr := r.store.CommandResult(ctx, record); replayErr == nil && found {
			return r.collaborationState(ctx, e.SessionID, "")
		}
		return CollaborationResult{}, err
	}
	return r.collaborationState(ctx, e.SessionID, msg.ID)
}

// collaborationState reads the session back after a command.
func (r *Runtime) collaborationState(ctx context.Context, sessionID, messageID string) (CollaborationResult, error) {
	session, err := r.store.GetTeamSession(ctx, sessionID)
	if err != nil {
		return CollaborationResult{}, err
	}
	return CollaborationResult{MessageID: messageID, ActiveTurn: session.ActiveTurn, TurnSequence: session.TurnSequence}, nil
}

func sessionHasParticipant(session model.TeamSession, agentID string) bool {
	for _, participant := range session.Participants {
		if participant.AgentID == agentID {
			return true
		}
	}
	return false
}
