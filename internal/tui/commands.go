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
		// Subcommands are routed before the free-text path. Without this any
		// "/goal add-constraint no-net" was swallowed whole and written as the
		// desired outcome, silently destroying the goal statement.
		switch strings.ToLower(parts[1]) {
		case "constraints":
			return h.handleGoalConstraints(ctx)
		case "add-constraint":
			return "Goal mutation is unavailable in TUI: authenticated runtime authorization is required.", nil
		case "rm-constraint":
			return "Goal mutation is unavailable in TUI: authenticated runtime authorization is required.", nil
		}
		return "Goal mutation is unavailable in TUI: authenticated runtime authorization is required.", nil

	case "/mode":
		if len(parts) < 2 {
			return fmt.Sprintf("Current mode: %s (options: manual, auto; ultra requires entitlement and is unavailable)", h.ws.mode), nil
		}
		mode := strings.ToLower(parts[1])
		switch mode {
		case "manual", "auto":
			h.ws.mode = mode
			h.ws.state.SessionMode = strings.ToUpper(mode)
			return fmt.Sprintf("Operating mode switched to %s.", strings.ToUpper(mode)), nil
		case "ultra":
			return "ULTRA is unavailable: no cryptographically verified entitlement is active.", nil
		default:
			return "Invalid mode. Supported modes: manual, auto", nil
		}

	case "/status":
		return h.handleStatus(ctx)
	case "/review", "/verification":
		if len(parts) != 2 {
			return "Usage: /review <verification_id>", nil
		}
		return h.handleVerification(ctx, parts[1])

	case "/learning":
		if len(parts) != 2 {
			return "Usage: /learning <memory_commit_id>", nil
		}
		return h.handleMemoryCommit(ctx, parts[1])

	case "/memory-search":
		if len(parts) < 2 {
			return "Usage: /memory-search <project_id> [term ...]", nil
		}
		return h.handleMemorySearch(ctx, parts[1], parts[2:], false)

	case "/memory-stale":
		if len(parts) < 2 {
			return "Usage: /memory-stale <project_id> [term ...]", nil
		}
		return h.handleMemorySearch(ctx, parts[1], parts[2:], true)

	case "/provenance":
		if len(parts) != 2 {
			return "Usage: /provenance <item_id>", nil
		}
		return h.handleMemoryProvenance(ctx, parts[1])

	case "/trust":
		taskClass := ""
		if len(parts) == 2 {
			taskClass = parts[1]
		}
		return h.handleRoutingTrust(ctx, taskClass)

	case "/fingerprints":
		if len(parts) != 2 {
			return "Usage: /fingerprints <project_id>", nil
		}
		return h.handleFingerprints(ctx, parts[1])

	case "/playbooks":
		if len(parts) != 2 {
			return "Usage: /playbooks <project_id>", nil
		}
		return h.handlePlaybookCandidates(ctx, parts[1])

	case "/replay-index":
		runID := ""
		if len(parts) == 2 {
			runID = parts[1]
		}
		return h.handleReplayIndex(ctx, runID)

	case "/inspect":
		if len(parts) < 2 {
			return "Usage: /inspect [claim|evidence|checkpoint|task|handoff|approval] <id>", nil
		}
		if len(parts) >= 3 {
			return h.handleInspect(ctx, parts[1], parts[2])
		}
		return h.handleInspect(ctx, "", parts[1])

	case "/approve":
		return "Approval mutation is unavailable in TUI: authenticated runtime authorization is required.", nil

	case "/reject":
		return "Approval mutation is unavailable in TUI: authenticated runtime authorization is required.", nil

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
		return "Message mutation is unavailable in TUI: authenticated runtime authorization is required.", nil

	case "/handoff":
		return "Handoff mutation is unavailable in TUI: authenticated runtime authorization is required.", nil

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

	case "/doctor":
		return h.handleDoctor(ctx)

	case "/tasks", "/task":
		if len(parts) > 1 {
			return "Task mutation is unavailable in TUI: authenticated runtime authorization is required.", nil
		}
		return h.handleTasks(ctx, parts[1:], line)

	case "/policy":
		return h.handlePolicy(ctx, parts[1:])

	case "/sandbox":
		return h.handleSandbox(ctx)

	case "/memory":
		return h.handleMemory(ctx, parts[1:], line)

	case "/provider", "/providers":
		return h.handleProvider(ctx, parts[1:])

	case "/harness":
		return h.handleHarness(ctx, parts[1:])

	case "/model":
		return h.handleModel(ctx, parts[1:])

	case "/effort":
		return h.handleEffort(ctx, parts[1:])

	case "/ultra":
		return h.handleUltra(ctx, parts[1:])

	case "/backup":
		return h.handleBackup(ctx, parts[1:])

	case "/fingerprint":
		return h.handleFingerprint(ctx)

	case "/runtime":
		return h.handleRuntime(ctx)

	case "/store":
		return h.handleStore(ctx)

	case "/export":
		return h.handleExport(ctx, parts[1:])

	case "/blind":
		return h.handleBlind(ctx, parts[1:])

	case "/reinjection":
		return h.handleReinjection(ctx)

	case "/alignment":
		return h.handleAlignment(ctx, parts[1:])

	case "/diff":
		return h.handleDiff(ctx)

	case "/approvals":
		return h.handleApprovals(ctx, parts[1:])

	case "/approval":
		return h.handleApproval(ctx, parts[1:])

	case "/termination":
		return h.handleTermination(ctx)

	case "/context":
		return h.handleContext(ctx, parts[1:])

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

	return fmt.Sprintf("Evidence %s: NOT FOUND in the active canonical claim set.", evidenceID), nil
}

func (h *CommandHandler) handleWhy(ctx context.Context) (string, error) {
	h.ws.mu.Lock()
	defer h.ws.mu.Unlock()

	if h.ws.state.RouteExplanation != "" {
		return fmt.Sprintf("ADVISORY ROUTING EXPLANATION (NOT APPLIED):\n%s", h.ws.state.RouteExplanation), nil
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
			return fmt.Sprintf("ADVISORY ROUTING EXPLANATION (NOT APPLIED):\n%s", plan.Explanation), nil
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
			// Seed the session from live host discovery rather than a fixed
			// roster. DiscoverTeamParticipants reports a harness as active only
			// when its binary is actually present, and leaves the model as
			// UNKNOWN, so an implicitly created session never persists an
			// invented model name into canonical state.
			participants := h.ws.state.Participants
			if len(participants) == 0 {
				participants = DiscoverTeamParticipants(nil)
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
	return "Checkpoint creation is unavailable in TUI: authenticated runtime snapshot support is not implemented.", nil
}

func (h *CommandHandler) handleRollback(ctx context.Context, cpID string) (string, error) {
	return fmt.Sprintf("Rollback to %s was NOT performed: authenticated runtime restoration is not implemented.", cpID), nil
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
	return "Pause was NOT performed: TUI has no authenticated runtime process-control handle.", nil
}

func (h *CommandHandler) handleResume(ctx context.Context) (string, error) {
	return "Resume was NOT performed: TUI has no authenticated runtime process-control handle.", nil
}

func (h *CommandHandler) handleCancel(ctx context.Context) (string, error) {
	return "Cancel was NOT performed: TUI has no authenticated runtime process-control handle.", nil
}

func (h *CommandHandler) helpText() string {
	return `MARSHAL Terminal Workspace Commands:
  /status                  Show canonical session, goal, team, claim, budget, and termination status
  /goal [outcome]          View or update the active GoalContract
  /mode [manual|auto]       Switch operating supervision mode; ULTRA requires entitlement
  /agents                  List registered participants, fixed roles, and harnesses
  /claims                  List active claims and epistemic verification states
  /learning <id>           Inspect a Process 07 memory commit, promotions and refusals
  /memory-search <proj>    Search durable memory with state, freshness and contradictions
  /memory-stale <proj>     Include stale memory, always marked unusable
  /provenance <item>       Show one memory item's full version history
  /trust [task-class]      Measured routing outcomes, failures and selection bias
  /fingerprints <proj>     Bounded failure fingerprints
  /playbooks <proj>        Playbook candidates awaiting review
  /replay-index [run]      Replay and reproducibility index
  /inspect [kind] <id>     Inspect a claim, evidence, checkpoint, task, handoff, or approval
  /evidence <id>           Inspect evidence item details and linked claims
  /approve [approval_id]   Grant a pending approval through the policy approval store
  /reject [approval_id]    Deny a pending approval and record the decision durably
  /route [key=value ...]   Compute an advisory route; it is not applied to Runtime
  /why                     Explain the advisory routing calculation
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
