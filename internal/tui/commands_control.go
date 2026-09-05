package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
)

// defaultApprovalWindow bounds how long an operator approval granted from the TUI
// stays valid. The approvals table forbids an approved record without an expiry,
// so a granted approval is always time boxed.
const defaultApprovalWindow = time.Hour

// handleStatus renders the canonical session, runtime, goal, team, claim, budget
// and termination status from refreshed store state. It reuses RenderScreen so the
// command and the always-on dashboard can never drift apart, then appends the
// termination and routing detail the one-screen header only summarises.
func (h *CommandHandler) handleStatus(ctx context.Context) (string, error) {
	if err := h.ws.RefreshState(ctx); err != nil {
		return "", fmt.Errorf("refresh status: %w", err)
	}

	h.ws.mu.RLock()
	state := h.ws.state
	mode := h.ws.mode
	h.ws.mu.RUnlock()

	var b strings.Builder
	b.WriteString(RenderScreen(state, 90))
	b.WriteString("\nCANONICAL STATUS DETAIL:\n")
	b.WriteString(fmt.Sprintf("  Project:      %s\n", orNone(state.ProjectID)))
	b.WriteString(fmt.Sprintf("  Session:      %s\n", orNone(state.SessionID)))
	b.WriteString(fmt.Sprintf("  Runtime mode: %s\n", strings.ToUpper(mode)))

	if state.Goal.ID == "" {
		b.WriteString("  Goal:         (none set)\n")
	} else {
		b.WriteString(fmt.Sprintf("  Goal:         %s [rev %d] %s\n",
			state.Goal.ID, state.Goal.Revision, RedactContent(state.Goal.DesiredOutcome, nil)))
		b.WriteString(fmt.Sprintf("  Understanding:%s\n", " "+string(state.UnderstandingState)))
	}

	termination := string(state.TerminationState)
	if termination == "" {
		termination = "RUNNING (no terminal state recorded)"
	}
	b.WriteString(fmt.Sprintf("  Termination:  %s\n", termination))

	verified, contested := 0, 0
	for _, c := range state.Claims {
		switch c.State {
		case model.ClaimStateVerified:
			verified++
		case model.ClaimStateContested:
			contested++
		}
	}
	b.WriteString(fmt.Sprintf("  Claims:       %d total | %d verified | %d contested\n",
		len(state.Claims), verified, contested))
	b.WriteString(fmt.Sprintf("  Team:         %d participants | active turn: %s\n",
		len(state.Participants), orNone(state.ActiveTurn)))

	tokens := "0"
	if state.BudgetConsumed.TotalTokens != nil {
		tokens = fmt.Sprintf("%d", *state.BudgetConsumed.TotalTokens)
	}
	cost := "$0.00"
	if state.BudgetConsumed.CostUSD != nil {
		cost = fmt.Sprintf("$%.4f", *state.BudgetConsumed.CostUSD)
	}
	b.WriteString(fmt.Sprintf("  Budget:       tokens=%s cost=%s calls=%d handoffs=%d\n",
		tokens, cost, state.BudgetConsumed.ModelCalls, state.BudgetConsumed.Handoffs))

	if h.ws.store != nil {
		pending, err := h.ws.store.ListPendingApprovals(ctx, state.ProjectID)
		if err == nil {
			b.WriteString(fmt.Sprintf("  Approvals:    %d pending\n", len(pending)))
		}
	}

	return b.String(), nil
}

// handleInspect resolves an identifier against canonical store records. The kind
// may be given explicitly, otherwise every supported record type is probed so the
// operator can paste an identifier without first knowing what it refers to.
func (h *CommandHandler) handleInspect(ctx context.Context, kind, id string) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	kind = strings.ToLower(strings.TrimSpace(kind))
	// Evidence is probed last: its lookup falls back to a ledger acknowledgement
	// for any unrecognised id, so it would otherwise shadow every other kind.
	order := []string{"claim", "checkpoint", "task", "handoff", "approval", "evidence"}
	if kind != "" {
		order = []string{kind}
	}

	var attempts []string
	for _, k := range order {
		out, err := h.inspectOne(ctx, k, id)
		if err == nil && out != "" {
			return out, nil
		}
		attempts = append(attempts, k)
	}

	if kind != "" {
		return fmt.Sprintf("No %s found with identifier %q.", kind, id), nil
	}
	return fmt.Sprintf("No canonical record found for %q (searched: %s).",
		id, strings.Join(attempts, ", ")), nil
}

func (h *CommandHandler) inspectOne(ctx context.Context, kind, id string) (string, error) {
	switch kind {
	case "claim":
		claim, err := h.ws.store.GetClaim(ctx, id)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("CLAIM %s\n", claim.ID))
		b.WriteString(fmt.Sprintf("  State:       %s\n", claim.State))
		b.WriteString(fmt.Sprintf("  Criticality: %s\n", claim.Criticality))
		b.WriteString(fmt.Sprintf("  Goal:        %s [rev %d]\n", claim.GoalID, claim.GoalRevision))
		b.WriteString(fmt.Sprintf("  Text:        %s\n", RedactContent(claim.NormalizedText, nil)))
		b.WriteString(fmt.Sprintf("  Supporting:  %d evidence item(s)\n", len(claim.SupportingEvidence)))
		for _, ev := range claim.SupportingEvidence {
			b.WriteString(fmt.Sprintf("    + %s (tool: %s)\n", ev.EvidenceID, ev.Tool))
		}
		b.WriteString(fmt.Sprintf("  Contradicting: %d evidence item(s)\n", len(claim.ContradictingEvidence)))
		for _, ev := range claim.ContradictingEvidence {
			b.WriteString(fmt.Sprintf("    - %s (tool: %s)\n", ev.EvidenceID, ev.Tool))
		}
		if transitions, err := h.ws.store.GetClaimTransitions(ctx, claim.ID); err == nil && len(transitions) > 0 {
			b.WriteString(fmt.Sprintf("  Transitions: %d recorded\n", len(transitions)))
		}
		return b.String(), nil

	case "evidence":
		return h.handleEvidence(ctx, id)

	case "checkpoint":
		cp, err := h.ws.store.GetHandoffCheckpoint(ctx, id)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("CHECKPOINT %s\n  Version:  %d\n  Session:  %s\n  Task:     %s\n  Goal:     %s [rev %d]\n  Role:     %s\n  Author:   %s (%s)\n  Reason:   %s\n  Created:  %s\n",
			cp.ID, cp.Version, cp.SessionID, cp.TaskID, cp.GoalID, cp.GoalRevision,
			cp.Role, cp.Author.AgentID, cp.Author.Harness,
			RedactContent(cp.Reason, nil), cp.CreatedAt.Format(time.RFC3339)), nil

	case "task":
		task, err := h.ws.store.GetTask(ctx, id)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("TASK %s\n  Title:    %s\n  Status:   %s\n  Risk:     %s\n  Revision: %d\n",
			task.ID, RedactContent(task.Title, nil), task.Status, task.Risk, task.Revision), nil

	case "handoff":
		ho, err := h.ws.store.GetHandoff(ctx, protocol.HandoffID(id))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("HANDOFF %s\n  Task:        %s\n  From agent:  %s\n  To role:     %s\n  Status:      %s\n  Evidence:    %d item(s)\n  Constraints: %d reference(s)\n",
			ho.ID, ho.TaskID, ho.FromAgent, ho.ToRole, ho.Status,
			len(ho.EvidenceIDs), len(ho.ConstraintRefs)), nil

	case "approval":
		approval, err := h.ws.store.GetApproval(ctx, id)
		if err != nil {
			return "", err
		}
		return renderApproval(approval), nil
	}

	return "", fmt.Errorf("unsupported inspect kind %q", kind)
}

// handleApprove resolves a real pending approval through the canonical approval
// store. When no identifier is supplied and exactly one approval is pending, that
// one is resolved; if several are pending the operator must disambiguate, so an
// unattended approval is never granted by accident.
func (h *CommandHandler) handleApprove(ctx context.Context, approvalID string) (string, error) {
	return h.resolveApproval(ctx, approvalID, true)
}

// handleReject denies a real pending approval and records the decision durably.
func (h *CommandHandler) handleReject(ctx context.Context, approvalID string) (string, error) {
	return h.resolveApproval(ctx, approvalID, false)
}

func (h *CommandHandler) resolveApproval(ctx context.Context, approvalID string, approve bool) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	verb := "reject"
	if approve {
		verb = "approve"
	}

	h.ws.mu.RLock()
	projectID := h.ws.state.ProjectID
	h.ws.mu.RUnlock()

	if approvalID == "" {
		pending, err := h.ws.store.ListPendingApprovals(ctx, projectID)
		if err != nil {
			return "", fmt.Errorf("list pending approvals: %w", err)
		}
		switch len(pending) {
		case 0:
			return "No pending approvals require an operator decision.", nil
		case 1:
			approvalID = pending[0].ID
		default:
			var b strings.Builder
			b.WriteString(fmt.Sprintf("%d approvals pending — specify one with /%s <approval_id>:\n", len(pending), verb))
			for _, a := range pending {
				b.WriteString(fmt.Sprintf("  %s | %s on %s (requested by %s)\n",
					a.ID, a.Operation, orNone(a.Target), a.RequestedBy))
			}
			return b.String(), nil
		}
	}

	current, err := h.ws.store.GetApproval(ctx, approvalID)
	if err != nil {
		return "", fmt.Errorf("approval %s: %w", approvalID, err)
	}
	if current.Status != model.ApprovalRequested {
		return fmt.Sprintf("Approval %s cannot be resolved: status is already %s.",
			approvalID, current.Status), nil
	}

	var expiry *time.Time
	if approve {
		deadline := time.Now().UTC().Add(defaultApprovalWindow)
		expiry = &deadline
	}

	resolved, err := h.ws.store.ResolveApproval(ctx, approvalID, "operator", approve, expiry, current.Revision)
	if err != nil {
		return "", fmt.Errorf("%s approval %s: %w", verb, approvalID, err)
	}

	if approve {
		return fmt.Sprintf("Approval %s granted for %s on %s (valid until %s, revision %d).",
			resolved.ID, resolved.Operation, orNone(resolved.Target),
			resolved.ExpiresAt.Format(time.RFC3339), resolved.Revision), nil
	}
	return fmt.Sprintf("Approval %s rejected for %s on %s; decision recorded durably (revision %d).",
		resolved.ID, resolved.Operation, orNone(resolved.Target), resolved.Revision), nil
}

// handleRoute shows or alters routing strictly through the ULTRA routing layer.
// With no arguments it reports the plan the router computes for current state;
// with arguments it re-routes under the operator's constraints and persists the
// resulting explanation, so the decision is the router's and never cosmetic.
func (h *CommandHandler) handleRoute(ctx context.Context, args []string) (string, error) {
	if h.ws.router == nil {
		return "ULTRA router unavailable", nil
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

	var overrides []string
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return routeUsage(), nil
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "role":
			role := model.Role(strings.ToLower(value))
			switch role {
			case model.RoleArchitect, model.RoleDeveloper, model.RoleQA, model.RoleAppSec:
				req.FixedRole = role
				overrides = append(overrides, "role="+value)
			default:
				return fmt.Sprintf("Invalid role %q. Supported: architect, developer, qa, appsec.", value), nil
			}
		case "harness":
			req.PreferredHarness = strings.ToLower(value)
			overrides = append(overrides, "harness="+value)
		case "risk":
			risk := model.Risk(strings.ToUpper(value))
			switch risk {
			case model.R0, model.R1, model.R2, model.R3:
				req.Risk = risk
				overrides = append(overrides, "risk="+value)
			default:
				return fmt.Sprintf("Invalid risk %q. Supported: R0, R1, R2, R3.", value), nil
			}
		default:
			return routeUsage(), nil
		}
	}

	plan, err := h.ws.router.Route(ctx, req)
	if err != nil {
		return "", fmt.Errorf("ultra route: %w", err)
	}

	// Persist the explanation so /why and the dashboard reflect this decision.
	h.ws.mu.Lock()
	h.ws.state.RouteExplanation = plan.Explanation
	h.ws.mu.Unlock()

	var b strings.Builder
	if len(overrides) > 0 {
		b.WriteString(fmt.Sprintf("ULTRA ROUTE RECOMPUTED (%s):\n", strings.Join(overrides, ", ")))
	} else {
		b.WriteString("ULTRA ROUTE (current state):\n")
	}
	b.WriteString(fmt.Sprintf("  Role:         %s\n", plan.Role))
	b.WriteString(fmt.Sprintf("  Harness:      %s\n", plan.Harness))
	b.WriteString(fmt.Sprintf("  Model:        %s\n", plan.Model))
	b.WriteString(fmt.Sprintf("  Native mode:  %s\n", orNone(plan.NativeMode)))
	b.WriteString(fmt.Sprintf("  Effort:       %s\n", orNone(plan.ReasoningEffort)))
	b.WriteString(fmt.Sprintf("  Subagents:    %t\n", plan.UseSubagents))
	b.WriteString(fmt.Sprintf("  Tool policy:  %s\n", orNone(plan.ToolPolicy)))
	b.WriteString(fmt.Sprintf("  Context:      %s\n", orNone(plan.ContextStrategy)))
	b.WriteString(fmt.Sprintf("  Verification: %s\n", orNone(plan.VerificationPolicy)))
	b.WriteString(fmt.Sprintf("  Risk input:   %s (critical claims: %t)\n", req.Risk, req.HasCriticalClaims))
	if plan.Explanation != "" {
		b.WriteString(fmt.Sprintf("  Explanation:  %s\n", plan.Explanation))
	}
	return b.String(), nil
}

func routeUsage() string {
	return "Usage: /route [role=<architect|developer|qa|appsec>] [harness=<name>] [risk=<R0|R1|R2|R3>]"
}

func renderApproval(a model.Approval) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("APPROVAL %s\n", a.ID))
	b.WriteString(fmt.Sprintf("  Project:   %s\n", a.ProjectID))
	b.WriteString(fmt.Sprintf("  Operation: %s\n", a.Operation))
	b.WriteString(fmt.Sprintf("  Scope:     %s\n", a.Scope))
	b.WriteString(fmt.Sprintf("  Target:    %s\n", orNone(a.Target)))
	b.WriteString(fmt.Sprintf("  Requested: %s at %s\n", a.RequestedBy, a.CreatedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("  Status:    %s\n", a.Status))
	if a.ApprovedBy != "" {
		b.WriteString(fmt.Sprintf("  Decided:   %s\n", a.ApprovedBy))
	}
	if a.ExpiresAt != nil {
		b.WriteString(fmt.Sprintf("  Expires:   %s\n", a.ExpiresAt.Format(time.RFC3339)))
	}
	b.WriteString(fmt.Sprintf("  Revision:  %d\n", a.Revision))
	return b.String()
}

func orNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}
	return value
}
