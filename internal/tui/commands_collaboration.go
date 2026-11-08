package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	session, err := a.runtime.Store().GetTeamSession(ctx, a.sessionID)
	if err != nil {
		return fmt.Sprintf("No collaboration session %s exists for this workspace; nothing was sent. Start a team session first.", a.sessionID), nil
	}
	result, err := a.MenuCollaborate(ctx, operation, args[0], strings.Join(args[1:], " "), session.TurnSequence, uuid.NewString())
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
		return fmt.Sprintf("Turn handed to %s (%s); session turn %d.", result.ActiveTurn, strings.ToLower(args[0]), result.TurnSequence), nil
	}
	to := args[0]
	if strings.EqualFold(to, "all") {
		to = "the whole team"
	}
	return fmt.Sprintf("Message %s sent to %s.", result.MessageID, to), nil
}
