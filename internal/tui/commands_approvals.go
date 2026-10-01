package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/google/uuid"
)

func (h *CommandHandler) handleApprovalDecision(ctx context.Context, args []string, approve bool) (string, error) {
	authority, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || authority == nil || authority.runtime == nil || authority.localControl == nil {
		return "Approval mutation is unavailable in TUI: authenticated runtime authorization is required.", nil
	}
	if len(args) == 0 || (approve && len(args) != 1) {
		return "Usage: /approve <typed-id|id> | /reject <typed-id|id> [reason]", nil
	}
	if authority.localControlErr != nil {
		return "", authority.localControlErr
	}
	record, err := authority.runtime.ResolveDecision(ctx, authority.sessionID, args[0])
	if err != nil {
		return "", err
	}
	reason := "owner decision from composer"
	if len(args) > 1 {
		reason = strings.Join(args[1:], " ")
	}
	result, err := authority.runtime.CommandDecideApproval(authority.localControl.Context(ctx), app.ApprovalDecision{Envelope: app.CommandEnvelope{ProjectID: authority.runtime.ProjectIdentity(), SessionID: authority.sessionID, TargetID: record.ID, ExpectedVersion: record.Version, IdempotencyKey: uuid.NewString()}, Digest: record.Digest, Approve: approve, Reason: reason})
	if err != nil {
		return "", err
	}
	if result.Kind == "goal" {
		g, err := authority.CurrentGoal(ctx)
		if err != nil {
			return "", err
		}
		h.ws.mu.Lock()
		h.ws.state.Goal = g
		h.ws.mu.Unlock()
	}
	status := result.Status
	if result.Kind == "goal" && status == "APPROVED" {
		status = "CONFIRMED (APPROVED)"
	}
	return fmt.Sprintf("%s %s (revision %d)", result.ID, status, result.Version), nil
}
func (h *CommandHandler) handleCanonicalApprovals(ctx context.Context, a *runtimeControlAuthority, args []string) (string, error) {
	pending := true
	if len(args) > 0 {
		if len(args) != 1 || args[0] != "history" {
			return "Usage: /approvals [history]", nil
		}
		pending = false
	}
	records, err := a.runtime.DecisionRecords(ctx, a.sessionID, pending)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "No approvals are awaiting an operator decision or recorded in this view.", nil
	}
	var b strings.Builder
	b.WriteString("APPROVALS:\n")
	for _, record := range records {
		fmt.Fprintf(&b, "  %s [%s] %s\n", record.ID, record.Kind, record.Status)
	}
	if pending {
		b.WriteString("Use /approve <typed-id> or /reject <typed-id> [reason].")
	}
	return b.String(), nil
}
