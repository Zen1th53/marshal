package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/google/uuid"
)

// handleCollaboration sends an operator message or hands the turn over through
// the authenticated LocalControl boundary. The sender is the local owner
// principal, and the session must already exist: nothing is created or named
// on the operator's behalf.
func (h *CommandHandler) handleCollaboration(ctx context.Context, operation string, args []string) (string, error) {
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil || a.localControl == nil {
		return "Collaboration is unavailable in TUI: authenticated runtime authorization is required.", nil
	}
	input := app.CollaborationCommand{Operation: operation, Content: RedactContent(strings.Join(args[1:], " "), nil)}
	if operation == "message" {
		if !strings.EqualFold(args[0], "all") {
			input.To = args[0]
		}
	} else {
		input.TargetRole = model.Role(strings.ToLower(args[0]))
	}
	session, err := a.runtime.Store().GetTeamSession(ctx, a.sessionID)
	if err != nil {
		return fmt.Sprintf("No collaboration session %s exists for this workspace; nothing was sent. Start a team session first.", a.sessionID), nil
	}
	e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, TargetID: a.sessionID,
		ExpectedVersion: session.TurnSequence, IdempotencyKey: uuid.NewString()}
	result, err := a.runtime.CommandCollaboration(a.localControl.Context(ctx), e, input)
	if errors.Is(err, model.ErrConflict) {
		return "Nothing was sent: the session moved on or is not active. Check /status and retry.", nil
	}
	if err != nil {
		return "", err
	}
	h.ws.mu.Lock()
	h.ws.state.ActiveTurn = result.ActiveTurn
	h.ws.mu.Unlock()
	if operation == "handoff" {
		return fmt.Sprintf("Turn handed to %s (%s); session turn %d.", result.ActiveTurn, input.TargetRole, result.TurnSequence), nil
	}
	to := input.To
	if to == "" {
		to = "the whole team"
	}
	return fmt.Sprintf("Message %s sent to %s.", result.MessageID, to), nil
}
