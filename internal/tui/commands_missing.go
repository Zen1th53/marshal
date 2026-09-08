package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// handleApprovals serves the approval queue and its decision history.
//
// The capability registry advertises /approvals and /approvals history, so both
// must resolve to real canonical reads rather than to the unknown-command
// branch.
func (h *CommandHandler) handleApprovals(ctx context.Context, args []string) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	h.ws.mu.RLock()
	projectID := h.ws.state.ProjectID
	h.ws.mu.RUnlock()

	if len(args) > 0 && strings.ToLower(args[0]) == "history" {
		resolved, err := h.ws.store.ListResolvedApprovals(ctx, projectID, 50)
		if err != nil {
			return "", fmt.Errorf("list approval history: %w", err)
		}
		if len(resolved) == 0 {
			return "No approvals have been decided in this project.", nil
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("APPROVAL HISTORY (%d most recent):\n", len(resolved)))
		for _, a := range resolved {
			decided := a.ApprovedBy
			if decided == "" {
				decided = "(unrecorded)"
			}
			b.WriteString(fmt.Sprintf("  %-16s %-9s %s on %s\n",
				a.ID, a.Status, a.Operation, orNone(a.Target)))
			b.WriteString(fmt.Sprintf("    decided by %s | requested by %s | %s\n",
				decided, a.RequestedBy, a.CreatedAt.Format(time.RFC3339)))
		}
		return b.String(), nil
	}

	pending, err := h.ws.store.ListPendingApprovals(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("list pending approvals: %w", err)
	}
	if len(pending) == 0 {
		return "No approvals are awaiting an operator decision.\nUse /approvals history to review past decisions.", nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("PENDING APPROVALS (%d):\n", len(pending)))
	for _, a := range pending {
		b.WriteString(fmt.Sprintf("  %-16s %s on %s\n", a.ID, a.Operation, orNone(a.Target)))
		b.WriteString(fmt.Sprintf("    scope %s | requested by %s | %s\n",
			a.Scope, a.RequestedBy, a.CreatedAt.Format(time.RFC3339)))
	}
	b.WriteString("\nDecision mutation is unavailable in TUI; use an authenticated runtime control surface. Inspect with /approval inspect <id>.")
	return b.String(), nil
}

// handleApproval inspects a single approval record, optionally showing the diff
// context the request carries.
func (h *CommandHandler) handleApproval(ctx context.Context, args []string) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}
	if len(args) < 2 {
		return "Usage: /approval [inspect|diff] <approval_id>", nil
	}

	sub := strings.ToLower(args[0])
	id := args[1]

	approval, err := h.ws.store.GetApproval(ctx, id)
	if err != nil {
		return "", fmt.Errorf("approval %s: %w", id, err)
	}

	switch sub {
	case "inspect":
		return renderApproval(approval), nil

	case "diff":
		// An approval names the commit it was requested against. Show that
		// binding plus the live working tree, rather than implying MARSHAL
		// stored a diff snapshot it does not keep.
		var b strings.Builder
		b.WriteString(fmt.Sprintf("APPROVAL DIFF CONTEXT — %s\n", approval.ID))
		b.WriteString(fmt.Sprintf("  Operation: %s\n", approval.Operation))
		b.WriteString(fmt.Sprintf("  Scope:     %s\n", approval.Scope))
		b.WriteString(fmt.Sprintf("  Target:    %s\n", orNone(approval.Target)))
		if approval.Commit == "" {
			b.WriteString("  Commit:    (none recorded on this approval)\n")
		} else {
			b.WriteString(fmt.Sprintf("  Commit:    %s\n", approval.Commit))
		}
		b.WriteString("\nWorking tree at this moment:\n")
		diff, derr := h.handleDiff(ctx)
		if derr != nil {
			b.WriteString(fmt.Sprintf("  (diff unavailable: %v)\n", derr))
		} else {
			b.WriteString(diff)
		}
		return b.String(), nil

	default:
		return "Usage: /approval [inspect|diff] <approval_id>", nil
	}
}

// handleTermination reports the canonical termination state of the active goal.
func (h *CommandHandler) handleTermination(ctx context.Context) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	h.ws.mu.RLock()
	goal := h.ws.state.Goal
	sessionID := h.ws.sessionID
	h.ws.mu.RUnlock()

	if goal.ID == "" {
		return "No active goal: nothing can have terminated yet.", nil
	}

	// A goal that has not terminated has no termination row. That is the normal
	// running state, not a failure, so ErrNotFound is reported as RUNNING rather
	// than surfaced to the operator as an error.
	term, err := h.ws.store.GetGoalTermination(ctx, sessionID, goal.ID, goal.Revision)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return "", fmt.Errorf("read termination: %w", err)
	}
	if err != nil || term == nil {
		return fmt.Sprintf("TERMINATION STATUS — %s [rev %d]\n  State:  RUNNING\n  No terminal state has been recorded for this goal revision.",
			goal.ID, goal.Revision), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("TERMINATION STATUS — %s [rev %d]\n", goal.ID, goal.Revision))
	b.WriteString(fmt.Sprintf("  State:   %s\n", term.State))
	b.WriteString(fmt.Sprintf("  Reason:  %s\n", term.ReasonCode))
	if term.ReasonDetail != "" {
		b.WriteString(fmt.Sprintf("  Detail:  %s\n", RedactContent(term.ReasonDetail, nil)))
	}
	if !term.CompletedAt.IsZero() {
		b.WriteString(fmt.Sprintf("  At:      %s\n", term.CompletedAt.Format(time.RFC3339)))
	}
	return b.String(), nil
}

// handleContext shows an advisory context strategy that Runtime does not apply.
//
// The strategy is not stored as free text: it is whatever the router computes
// for the current role and risk, so a set request is validated against the
// strategies the routing layer can actually produce.
func (h *CommandHandler) handleContext(ctx context.Context, args []string) (string, error) {
	if h.ws.router == nil {
		return "Advisory router unavailable", nil
	}

	h.ws.mu.RLock()
	goal := h.ws.state.Goal
	claims := h.ws.state.Claims
	h.ws.mu.RUnlock()

	req := model.ULTRARouteRequest{
		GoalID:       goal.ID,
		GoalRevision: goal.Revision,
		FixedRole:    model.RoleDeveloper,
		Risk:         model.R1,
	}
	if goal.Risk != "" {
		req.Risk = goal.Risk
	}
	for _, c := range claims {
		if c.Criticality.IsCritical() {
			req.HasCriticalClaims = true
			break
		}
	}

	plan, err := h.ws.router.Route(ctx, req)
	if err != nil {
		return "", fmt.Errorf("advisory route: %w", err)
	}

	if len(args) == 0 || strings.ToLower(args[0]) == "show" {
		return fmt.Sprintf("ADVISORY CONTEXT STRATEGY (NOT APPLIED):\n  Current:  %s\n  Derived from role %s at risk %s (critical claims: %t).",
			orNone(plan.ContextStrategy), plan.Role, req.Risk, req.HasCriticalClaims), nil
	}

	if strings.ToLower(args[0]) == "strategy" {
		if len(args) < 2 {
			return fmt.Sprintf("Advisory context strategy: %s (NOT APPLIED TO RUNTIME).",
				orNone(plan.ContextStrategy)), nil
		}
		return fmt.Sprintf("Context strategy was NOT applied.\n  Advisory value: %s\n  Runtime execution-profile integration is unavailable.",
			orNone(plan.ContextStrategy)), nil
	}

	return "Usage: /context [show|strategy]", nil
}
