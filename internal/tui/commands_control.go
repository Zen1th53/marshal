package tui

import (
	"context"
	"errors"
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
	state := h.ws.liveStateLocked()
	h.ws.mu.RUnlock()

	var b strings.Builder
	b.WriteString(RenderScreen(state, 90))
	b.WriteString("\nCANONICAL STATUS DETAIL:\n")
	if h.ws.store == nil {
		b.WriteString("  Store:        UNAVAILABLE (initialize a project with marshal init, then reopen TUI)\n")
	}
	b.WriteString(fmt.Sprintf("  Project:      %s\n", orNone(state.ProjectID)))
	b.WriteString(fmt.Sprintf("  Session:      %s\n", orNone(state.SessionID)))
	b.WriteString(fmt.Sprintf("  Runtime mode: %s\n", state.SessionMode))

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
		if state.Goal.ID == "" {
			termination = "NO ACTIVE GOAL (execution not verified)"
		}
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

	tokens := "UNKNOWN"
	if state.BudgetConsumed.TotalTokens != nil {
		tokens = fmt.Sprintf("%d", *state.BudgetConsumed.TotalTokens)
	}
	cost := "UNKNOWN"
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

func (h *CommandHandler) handleVerification(ctx context.Context, id string) (string, error) {
	if h.ws.store == nil {
		return "Canonical verification store unavailable.", nil
	}
	session, err := h.ws.store.GetVerificationSession(ctx, id)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("VERIFICATION %s\n  State: %s\n  Version: %d\n  Goal: %s rev %d\n  Plan: %s v%d\n  Run: %s v%d\n  Tree: %s\n  Criteria: %d | Claims: %d | Evidence: %d | Blockers: %d", session.ID, session.State, session.Version, session.Binding.GoalID, session.Binding.GoalRevision, session.Binding.PlanID, session.Binding.PlanVersion, session.Binding.RunID, session.Binding.RunVersion, session.Binding.TreeDigest, len(session.Criteria), len(session.Claims), len(session.Evidence), len(session.KnownBlockers)), nil
}

// handleInspect resolves an identifier against canonical store records. The kind
// may be given explicitly, otherwise every supported record type is probed so the
// operator can paste an identifier without first knowing what it refers to.
func (h *CommandHandler) handleInspect(ctx context.Context, kind, id string) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable. Open the TUI in an initialized MARSHAL project (marshal init).", nil
	}

	id = strings.TrimPrefix(id, "#")
	kind = strings.ToLower(strings.TrimSpace(kind))
	// Evidence is probed last because it searches the active claim set rather
	// than a standalone evidence table. A missing link is not a resolved record.
	order := []string{"claim", "checkpoint", "task", "handoff", "approval", "agent", "evidence"}
	if kind != "" {
		order = []string{kind}
	}

	var attempts []string
	for _, k := range order {
		out, err := h.inspectOne(ctx, k, id)
		if err == nil && out != "" && !(k == "evidence" && strings.Contains(out, ": NOT FOUND")) {
			return out, nil
		}
		if err != nil && !errors.Is(err, model.ErrNotFound) && !errors.Is(err, protocol.ErrHandoffNotFound) {
			return "", fmt.Errorf("inspect %s %s: %w; reopen the TUI and check /store", k, id, err)
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
		status := string(task.Status)
		if task.ControlState != "" {
			status = task.ControlState
		}
		return fmt.Sprintf("TASK %s\n  Title:    %s\n  Status:   %s\n  Risk:     %s\n  Revision: %d\n  Attempt:  %d\n", task.ID, RedactContent(task.Title, nil), status, task.Risk, task.Revision, task.Attempt), nil

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

	case "agent":
		id = strings.TrimPrefix(id, "@")
		var p *model.Participant
		h.ws.mu.RLock()
		for _, part := range h.ws.state.Participants {
			if strings.EqualFold(part.AgentID, id) {
				cp := part
				p = &cp
				break
			}
		}
		activeTurn := h.ws.state.ActiveTurn
		h.ws.mu.RUnlock()

		if p == nil {
			return "", fmt.Errorf("%w: agent %q not found in active team session", model.ErrNotFound, id)
		}

		stateStr := "AVAILABLE (execution not verified)"
		if !p.IsActive {
			stateStr = "UNAVAILABLE"
		} else if p.AgentID == activeTurn {
			stateStr = "ACTIVE TURN (execution not verified)"
		}

		modelStr := p.Model
		if modelStr == "" {
			modelStr = "UNKNOWN"
		}

		var b strings.Builder
		b.WriteString(fmt.Sprintf("AGENT %s\n", p.AgentID))
		b.WriteString(fmt.Sprintf("  Fixed Role:      %s\n", p.Role))
		b.WriteString(fmt.Sprintf("  State:           %s\n", stateStr))
		b.WriteString(fmt.Sprintf("  Harness:         %s\n", p.Harness))
		b.WriteString(fmt.Sprintf("  Model:           %s\n", modelStr))
		b.WriteString(fmt.Sprintf("  Provider:        %s\n", "UNKNOWN"))
		b.WriteString(fmt.Sprintf("  Native Mode:     %s\n", "UNKNOWN"))
		b.WriteString(fmt.Sprintf("  Active Task:     %s\n", "UNKNOWN"))
		b.WriteString(fmt.Sprintf("  Waiting On:      %s\n", "UNKNOWN"))
		b.WriteString(fmt.Sprintf("  Recent Handoffs: %s\n", "UNKNOWN"))
		b.WriteString(fmt.Sprintf("  Tokens / Cost:   %s\n", "UNKNOWN"))
		b.WriteString(fmt.Sprintf("  Routing Reason:  %s\n", "UNKNOWN"))
		return b.String(), nil
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

// handleRoute calculates an advisory plan only. Runtime does not consume this
// state, so the TUI must never present the result as an applied configuration.
func (h *CommandHandler) handleRoute(ctx context.Context, args []string) (string, error) {
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
	if errors.Is(err, model.ErrInvalid) {
		return fmt.Sprintf("Route not computed: %v.", err), nil
	}
	if err != nil {
		return "", fmt.Errorf("advisory route: %w", err)
	}
	modelLine, installedLine := h.routeResolution(ctx, plan.Harness)

	var b strings.Builder
	b.WriteString("ADVISORY ONLY — NOT APPLIED TO RUNTIME\n")
	if len(overrides) > 0 {
		b.WriteString(fmt.Sprintf("ADVISORY ROUTE RECOMPUTED (%s):\n", strings.Join(overrides, ", ")))
	} else {
		b.WriteString("ADVISORY ROUTE (current state):\n")
	}
	b.WriteString(fmt.Sprintf("  Role:         %s\n", plan.Role))
	b.WriteString(fmt.Sprintf("  Harness:      %s (%s)\n", plan.Harness, installedLine))
	if plan.PreferenceNote != "" {
		b.WriteString(fmt.Sprintf("  Preference:   %s\n", plan.PreferenceNote))
	}
	b.WriteString(fmt.Sprintf("  Model:        %s\n", modelLine))
	b.WriteString(fmt.Sprintf("  Native mode:  %s\n", orNone(plan.NativeMode)))
	b.WriteString(fmt.Sprintf("  Effort:       suggested %s (advisory; /effort sets what Codex runs request)\n", orNone(plan.ReasoningEffort)))
	b.WriteString(fmt.Sprintf("  Subagents:    %t\n", plan.UseSubagents))
	b.WriteString(fmt.Sprintf("  Tool policy:  %s\n", orNone(plan.ToolPolicy)))
	b.WriteString(fmt.Sprintf("  Context:      %s\n", orNone(plan.ContextStrategy)))
	b.WriteString(fmt.Sprintf("  Verification: %s\n", orNone(plan.VerificationPolicy)))
	b.WriteString(fmt.Sprintf("  Risk input:   %s (critical claims: %t)\n", req.Risk, req.HasCriticalClaims))
	if plan.Explanation != "" {
		b.WriteString(fmt.Sprintf("  Explanation:  %s\n", plan.Explanation))
	}
	request := "current state"
	if len(overrides) > 0 {
		request = strings.Join(overrides, ", ")
	}
	h.ws.mu.Lock()
	h.ws.lastRoute = &routeRecord{goalID: goal.ID, goalRevision: goal.Revision, request: request,
		explanation: fmt.Sprintf("Request: %s (risk %s, critical claims %t)\n%s\nHarness status: %s. Model: %s.",
			request, req.Risk, req.HasCriticalClaims, plan.Explanation, installedLine, modelLine)}
	h.ws.mu.Unlock()
	return b.String(), nil
}

// routeRecord is the last advisory route and the goal revision it describes.
type routeRecord struct {
	goalID       string
	goalRevision int64
	request      string
	explanation  string
}

// routeResolution says which model the routed harness would actually run and
// whether it is installed. The router never names a model; the model comes
// from the execution preference future runs read, or the provider default.
func (h *CommandHandler) routeResolution(ctx context.Context, harnessName string) (string, string) {
	probe := map[string]string{"claude-code": "claude"}[harnessName]
	if probe == "" {
		probe = harnessName
	}
	installed := "NOT INSTALLED: dispatch to it would fail"
	for _, p := range ProbeHarnesses() {
		if p.HarnessName == probe && p.Installed {
			installed = "installed"
		}
	}
	modelLine := "provider default, resolved at dispatch"
	if probe == "codex" || probe == "claude" {
		if a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority); ok && a != nil && a.runtime != nil {
			if _, current, err := a.runtime.ModelPreferenceRevision(ctx, probe); err == nil && current != "" {
				modelLine = current + " (execution preference)"
			} else if err == nil {
				modelLine = "adapter default, resolved at dispatch (no /model select preference)"
			}
		}
	}
	return modelLine, installed
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
