package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/collaboration"
	"github.com/Zen1th53/marshal/internal/model"
)

// CommandHandler dispatches interactive slash commands in the TUI workspace.
type CommandHandler struct {
	ws *Workspace
}

func NewCommandHandler(ws *Workspace) *CommandHandler {
	return &CommandHandler{ws: ws}
}

// Handle processes an interactive slash command line.
func (h *CommandHandler) Handle(ctx context.Context, line string) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", nil
	}

	parts := strings.Fields(line)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/help", "/?":
		return h.helpText(), nil

	case "/goal":
		if len(parts) < 2 {
			if h.ws.state.Goal.ID == "" {
				return "No active goal set. Use /goal <desired outcome> to define one.", nil
			}
			return fmt.Sprintf("Active Goal [v%d]: %s (ID: %s)",
				h.ws.state.Goal.Revision, h.ws.state.Goal.DesiredOutcome, h.ws.state.Goal.ID), nil
		}
		outcome := strings.TrimSpace(line[len(parts[0]):])
		return h.handleSetGoal(ctx, outcome)

	case "/mode":
		if len(parts) < 2 {
			return fmt.Sprintf("Current mode: %s (options: manual, auto, ultra)", h.ws.mode), nil
		}
		mode := strings.ToLower(parts[1])
		switch mode {
		case "manual", "auto", "ultra":
			h.ws.mode = mode
			h.ws.state.SessionMode = strings.ToUpper(mode)
			return fmt.Sprintf("Operating mode switched to %s.", strings.ToUpper(mode)), nil
		default:
			return "Invalid mode. Supported modes: manual, auto, ultra", nil
		}

	case "/status":
		return h.handleStatus(ctx)

	case "/inspect":
		if len(parts) < 2 {
			return "Usage: /inspect [claim|evidence|checkpoint|task|handoff|approval] <id>", nil
		}
		if len(parts) >= 3 {
			return h.handleInspect(ctx, parts[1], parts[2])
		}
		return h.handleInspect(ctx, "", parts[1])

	case "/approve":
		id := ""
		if len(parts) >= 2 {
			id = parts[1]
		}
		return h.handleApprove(ctx, id)

	case "/reject":
		id := ""
		if len(parts) >= 2 {
			id = parts[1]
		}
		return h.handleReject(ctx, id)

	case "/route":
		return h.handleRoute(ctx, parts[1:])

	case "/agents", "/roster":
		return h.handleAgents(ctx)

	case "/claims":
		return h.handleClaims(ctx)

	case "/evidence":
		if len(parts) < 2 {
			return "Usage: /evidence <evidence_id>", nil
		}
		return h.handleEvidence(ctx, parts[1])

	case "/why":
		return h.handleWhy(ctx)

	case "/msg", "/say":
		if len(parts) < 3 {
			return "Usage: /msg <agent_id|all> <message text>", nil
		}
		target := parts[1]
		msgText := strings.TrimSpace(line[len(parts[0])+len(parts[1])+1:])
		return h.handleSendMessage(ctx, target, msgText)

	case "/handoff":
		if len(parts) < 3 {
			return "Usage: /handoff <architect|developer|qa|appsec> <summary>", nil
		}
		targetRole := model.Role(strings.ToLower(parts[1]))
		summary := strings.TrimSpace(line[len(parts[0])+len(parts[1])+1:])
		return h.handleHandoff(ctx, targetRole, summary)

	case "/checkpoint":
		return h.handleCheckpoint(ctx)

	case "/rollback":
		if len(parts) < 2 {
			return "Usage: /rollback <checkpoint_id>", nil
		}
		return h.handleRollback(ctx, parts[1])

	case "/budget":
		return h.handleBudget(ctx)

	case "/pause":
		return h.handlePause(ctx)

	case "/resume":
		return h.handleResume(ctx)

	case "/cancel":
		return h.handleCancel(ctx)

	default:
		return fmt.Sprintf("Unknown command %q. Type /help for available commands.", cmd), nil
	}
}

func (h *CommandHandler) handleSetGoal(ctx context.Context, outcome string) (string, error) {
	h.ws.mu.Lock()
	defer h.ws.mu.Unlock()

	goalID := h.ws.state.Goal.ID
	var rev int64 = 1
	var expectedRev int64 = 0
	if goalID == "" {
		goalID = fmt.Sprintf("goal-%d", time.Now().UnixNano())
	} else {
		expectedRev = h.ws.state.Goal.Revision
		rev = expectedRev + 1
	}

	goal := model.GoalContract{
		ID:                 goalID,
		SessionID:          h.ws.sessionID,
		Revision:           rev,
		DesiredOutcome:     outcome,
		Risk:               model.R1,
		AuthoritySource:    "operator",
		UnderstandingState: model.GoalReady,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}

	if h.ws.store != nil {
		if err := h.ws.store.SaveGoalContract(ctx, goal, expectedRev); err != nil {
			return "", fmt.Errorf("save goal contract: %w", err)
		}
	}

	h.ws.state.Goal = goal
	h.ws.state.UnderstandingState = model.GoalReady

	// Trigger ULTRA route update
	if h.ws.router != nil {
		plan, err := h.ws.router.Route(ctx, model.ULTRARouteRequest{
			GoalID:            goalID,
			FixedRole:         model.RoleDeveloper,
			PreferredHarness:  "codex",
			Risk:              model.R1,
			HasCriticalClaims: false,
		})
		if err == nil {
			h.ws.state.RouteExplanation = plan.Explanation
		}
	}

	return fmt.Sprintf("Active Goal updated to revision %d: %s", rev, outcome), nil
}

func (h *CommandHandler) handleAgents(ctx context.Context) (string, error) {
	h.ws.mu.Lock()
	defer h.ws.mu.Unlock()

	if len(h.ws.state.Participants) == 0 {
		return "No active team participants. Default team: Claude (Architect), Codex (Developer), OpenCode (QA), Antigravity (AppSec/Integration).", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("TEAM ROSTER (%d agents):\n", len(h.ws.state.Participants)))
	for _, p := range h.ws.state.Participants {
		active := "inactive"
		if p.IsActive {
			active = "active"
		}
		sb.WriteString(fmt.Sprintf("  • %-14s | Role: %-10s | Harness: %-12s | Model: %-18s | %s\n",
			p.AgentID, p.Role, p.Harness, p.Model, active))
	}
	return sb.String(), nil
}

func (h *CommandHandler) handleClaims(ctx context.Context) (string, error) {
	h.ws.mu.Lock()
	defer h.ws.mu.Unlock()

	if len(h.ws.state.Claims) == 0 {
		return "No claims registered under the active goal.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CLAIMS (%d total):\n", len(h.ws.state.Claims)))
	for _, c := range h.ws.state.Claims {
		crit := ""
		if c.Criticality.IsCritical() {
			crit = " [CRITICAL]"
		}
		sb.WriteString(fmt.Sprintf("  [%-9s]%s %s: %s\n",
			c.State, crit, c.ID, RedactContent(c.NormalizedText, nil)))
	}
	return sb.String(), nil
}

func (h *CommandHandler) handleEvidence(ctx context.Context, evidenceID string) (string, error) {
	h.ws.mu.RLock()
	defer h.ws.mu.RUnlock()

	for _, cl := range h.ws.state.Claims {
		for _, ev := range cl.SupportingEvidence {
			if ev.EvidenceID == evidenceID {
				return fmt.Sprintf("Evidence %s supports Claim %s [%s]: %s (tool: %s)",
					evidenceID, cl.ID, cl.State, RedactContent(cl.NormalizedText, nil), ev.Tool), nil
			}
		}
		for _, ev := range cl.ContradictingEvidence {
			if ev.EvidenceID == evidenceID {
				return fmt.Sprintf("Evidence %s contradicts Claim %s [%s]: %s (tool: %s)",
					evidenceID, cl.ID, cl.State, RedactContent(cl.NormalizedText, nil), ev.Tool), nil
			}
		}
	}

	return fmt.Sprintf("Evidence %s: recorded in evidence ledger (no active contradictory claims).", evidenceID), nil
}

func (h *CommandHandler) handleWhy(ctx context.Context) (string, error) {
	h.ws.mu.Lock()
	defer h.ws.mu.Unlock()

	if h.ws.state.RouteExplanation != "" {
		return fmt.Sprintf("ULTRA ROUTING EXPLANATION:\n%s", h.ws.state.RouteExplanation), nil
	}

	if h.ws.router != nil {
		plan, err := h.ws.router.Route(ctx, model.ULTRARouteRequest{
			GoalID:            h.ws.state.Goal.ID,
			FixedRole:         model.RoleDeveloper,
			PreferredHarness:  "codex",
			Risk:              model.R1,
			HasCriticalClaims: false,
		})
		if err == nil {
			return fmt.Sprintf("ULTRA ROUTING EXPLANATION:\n%s", plan.Explanation), nil
		}
	}

	return "No route explanation available yet.", nil
}

func (h *CommandHandler) handleSendMessage(ctx context.Context, target, msgText string) (string, error) {
	if h.ws.coord == nil {
		return "Collaboration coordinator unavailable", nil
	}

	now := time.Now().UTC()
	msg := model.AgentMessage{
		ID:        fmt.Sprintf("msg-user-%d", now.UnixNano()),
		SessionID: h.ws.sessionID,
		From: model.AuthorProvenance{
			AgentID: "operator",
			Harness: "tui",
		},
		To:        target,
		Kind:      model.MessageQuestion,
		Content:   RedactContent(msgText, nil),
		CreatedAt: now,
	}

	_, err := h.ws.coord.SendMessage(ctx, msg, false, false)
	if err != nil {
		if errors.Is(err, collaboration.ErrSessionNotFound) {
			participants := h.ws.state.Participants
			if len(participants) == 0 {
				participants = []model.Participant{
					{AgentID: "claude", Role: model.RoleArchitect, Harness: "claude-code", Model: "claude-3-7-sonnet", IsActive: true},
					{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex", Model: "gpt-4o", IsActive: true},
					{AgentID: "opencode", Role: model.RoleQA, Harness: "opencode", Model: "deepseek-coder", IsActive: true},
					{AgentID: "antigravity", Role: model.RoleAppSec, Harness: "antigravity", Model: "gemini-2.5-pro", IsActive: true},
				}
			}
			goalID := h.ws.state.Goal.ID
			if goalID == "" {
				goalID = "goal-interactive"
			}
			_, _ = h.ws.coord.CreateSession(ctx, h.ws.sessionID, goalID, h.ws.state.Goal.Revision, participants)
			_, err = h.ws.coord.SendMessage(ctx, msg, false, false)
		}
		if err != nil {
			return "", fmt.Errorf("send message: %w", err)
		}
	}

	h.ws.mu.Lock()
	h.ws.state.RecentMessages = append(h.ws.state.RecentMessages, msg)
	h.ws.mu.Unlock()

	return fmt.Sprintf("Message sent to %s.", target), nil
}

func (h *CommandHandler) handleHandoff(ctx context.Context, targetRole model.Role, summary string) (string, error) {
	if h.ws.coord == nil {
		return "Collaboration coordinator unavailable", nil
	}

	prov := model.AuthorProvenance{
		AgentID: "operator",
		Harness: "tui",
	}

	sess, err := h.ws.coord.HandOffOwnership(ctx, h.ws.sessionID, prov, targetRole, RedactContent(summary, nil), nil, nil)
	if err != nil {
		return "", fmt.Errorf("handoff failed: %w", err)
	}

	h.ws.mu.Lock()
	h.ws.state.ActiveTurn = sess.ActiveTurn
	h.ws.mu.Unlock()

	return fmt.Sprintf("Turn successfully handed off to %s (Agent: %s).", targetRole, sess.ActiveTurn), nil
}

func (h *CommandHandler) handleCheckpoint(ctx context.Context) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	cpID := fmt.Sprintf("cp-tui-%d", time.Now().UnixNano())
	cp := model.HandoffCheckpoint{
		ID:           cpID,
		Version:      1,
		SessionID:    h.ws.sessionID,
		TaskID:       "task-interactive",
		GoalID:       h.ws.state.Goal.ID,
		GoalRevision: h.ws.state.Goal.Revision,
		Role:         "operator",
		Author: model.AuthorProvenance{
			AgentID: "operator",
			Harness: "tui",
		},
		Reason:    "Manual operator checkpoint via TUI",
		CreatedAt: time.Now().UTC(),
	}

	if err := h.ws.store.SaveHandoffCheckpoint(ctx, cp); err != nil {
		return "", fmt.Errorf("save checkpoint: %w", err)
	}

	return fmt.Sprintf("Durable checkpoint created: %s", cpID), nil
}

func (h *CommandHandler) handleRollback(ctx context.Context, cpID string) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	cp, err := h.ws.store.GetHandoffCheckpoint(ctx, cpID)
	if err != nil {
		return "", fmt.Errorf("checkpoint %s not found: %w", cpID, err)
	}

	now := time.Now().UTC()
	rb := model.CheckpointRollback{
		RollbackID:   fmt.Sprintf("rb-%d", now.UnixNano()),
		CheckpointID: cp.ID,
		Actor: model.AuthorProvenance{
			AgentID: "operator",
			Harness: "tui",
		},
		Reason:    "Operator requested rollback in TUI",
		CreatedAt: now,
	}

	if err := h.ws.store.RecordCheckpointRollback(ctx, rb); err != nil {
		return "", fmt.Errorf("record rollback: %w", err)
	}

	return fmt.Sprintf("Successfully rolled back to checkpoint %s (authored by %s at %s).",
		cpID, cp.Author.AgentID, cp.CreatedAt.Format(time.RFC3339)), nil
}

func (h *CommandHandler) handleBudget(ctx context.Context) (string, error) {
	h.ws.mu.Lock()
	defer h.ws.mu.Unlock()

	tokStr := "0"
	if h.ws.state.BudgetConsumed.TotalTokens != nil {
		tokStr = fmt.Sprintf("%d", *h.ws.state.BudgetConsumed.TotalTokens)
	}
	costStr := "$0.00"
	if h.ws.state.BudgetConsumed.CostUSD != nil {
		costStr = fmt.Sprintf("$%.4f", *h.ws.state.BudgetConsumed.CostUSD)
	}

	return fmt.Sprintf("BUDGET CONSUMED:\n  Tokens: %s\n  Cost: %s\n  Model Calls: %d\n  Handoffs: %d\n  Wall-clock: %s",
		tokStr, costStr, h.ws.state.BudgetConsumed.ModelCalls, h.ws.state.BudgetConsumed.Handoffs,
		h.ws.state.BudgetConsumed.Duration.Round(time.Millisecond)), nil
}

func (h *CommandHandler) handlePause(ctx context.Context) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	sess, err := h.ws.store.GetTeamSession(ctx, h.ws.sessionID)
	if err != nil {
		return "", fmt.Errorf("get session: %w", err)
	}

	sess.Status = "PAUSED"
	sess.UpdatedAt = time.Now().UTC()
	if err := h.ws.store.SaveTeamSession(ctx, *sess); err != nil {
		return "", fmt.Errorf("save session: %w", err)
	}

	return "Collaborative session paused. Use /resume to continue.", nil
}

func (h *CommandHandler) handleResume(ctx context.Context) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	sess, err := h.ws.store.GetTeamSession(ctx, h.ws.sessionID)
	if err != nil {
		return "", fmt.Errorf("get session: %w", err)
	}

	sess.Status = "ACTIVE"
	sess.UpdatedAt = time.Now().UTC()
	if err := h.ws.store.SaveTeamSession(ctx, *sess); err != nil {
		return "", fmt.Errorf("save session: %w", err)
	}

	return "Collaborative session resumed.", nil
}

func (h *CommandHandler) handleCancel(ctx context.Context) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	now := time.Now().UTC()
	term := model.GoalTermination{
		SessionID:    h.ws.sessionID,
		GoalID:       h.ws.state.Goal.ID,
		GoalRevision: h.ws.state.Goal.Revision,
		State:        model.StateCancelled,
		ReasonCode:   model.ReasonUserCancelled,
		ReasonDetail: "Operator initiated cancellation via TUI",
		CompletedAt:  now,
	}

	if err := h.ws.store.SaveGoalTermination(ctx, term); err != nil {
		return "", fmt.Errorf("record termination: %w", err)
	}

	h.ws.mu.Lock()
	h.ws.state.TerminationState = model.StateCancelled
	h.ws.mu.Unlock()

	return "Active goal execution cancelled by operator.", nil
}

func (h *CommandHandler) helpText() string {
	return `MARSHAL Terminal Workspace Commands:
  /status                  Show canonical session, goal, team, claim, budget, and termination status
  /goal [outcome]          View or update the active GoalContract
  /mode [manual|auto|ultra] Switch operating supervision mode
  /agents                  List registered participants, fixed roles, and harnesses
  /claims                  List active claims and epistemic verification states
  /inspect [kind] <id>     Inspect a claim, evidence, checkpoint, task, handoff, or approval
  /evidence <id>           Inspect evidence item details and linked claims
  /approve [approval_id]   Grant a pending approval through the policy approval store
  /reject [approval_id]    Deny a pending approval and record the decision durably
  /route [key=value ...]   Show or recompute the ULTRA route (role, harness, risk)
  /why                     Explain ULTRA routing decisions (harness, model, effort)
  /msg <agent|all> <text>  Send operator guidance to the team or a specific agent
  /handoff <role> <summary> Transfer active turn to the target role
  /checkpoint              Create a durable handoff checkpoint
  /rollback <id>           Roll back state to an eligible checkpoint
  /budget                  Inspect token, cost, call, and time budgets
  /pause                   Pause the active collaborative session
  /resume                  Resume a paused collaborative session
  /cancel                  Cancel active goal execution
  /help                    Show this help reference
  /quit, /exit             Exit TUI workspace (session remains durable in SQLite)`
}
