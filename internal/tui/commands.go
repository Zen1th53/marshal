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
	// A question /ultra stop asked is answered by the next command or not at
	// all, so a confirmation typed later cannot switch execution off.
	if cmd != "/ultra" && h.ws != nil {
		h.ws.askUltraStop(false)
	}

	// Backup paths may contain spaces; parse argv rather than splitting them.
	if cmd == "/backup" {
		var err error
		parts, err = nativeArgs(line)
		if err != nil {
			return "Usage: /backup [create|restore <backup_path>] (quote paths with spaces): " + err.Error(), nil
		}
	}
	if integrationCommand(cmd) {
		var err error
		parts, err = nativeArgs(line)
		if err != nil {
			return "Usage: " + cmd + " (quote complete arguments): " + err.Error(), nil
		}
	}
	if usage := configCommandUsage(parts); usage != "" {
		return usage, nil
	}
	if usage := workCommandUsage(parts); usage != "" {
		return usage, nil
	}

	// Read commands must render the current canonical revision, rather than
	// the cache from before a runtime update.
	switch cmd {
	case "/goal", "/claims", "/agents", "/roster", "/evidence", "/why", "/budget", "/route", "/inspect", "/export":
		if err := h.ws.RefreshState(ctx); err != nil {
			return "", fmt.Errorf("refresh command state: %w", err)
		}
	}

	switch cmd {
	case "/help", "/?":
		if len(parts) != 1 {
			return "Usage: /help", nil
		}
		return h.helpText(), nil

	case "/quit", "/exit":
		if len(parts) != 1 {
			return "Usage: /quit or /exit", nil
		}
		h.ws.mu.Lock()
		h.ws.exitRequested = true
		h.ws.mu.Unlock()
		return "Exiting MARSHAL terminal workspace. Any durable session data is preserved.", nil

	case "/goal":
		if len(parts) < 2 {
			if h.ws.state.Goal.ID == "" {
				return "No active goal set. Goal creation is unavailable in TUI; authenticated runtime authorization is required.", nil
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
		case "diff", "version", "criteria", "donotdo", "progress":
			return "Goal " + strings.ToLower(parts[1]) + " reporting is unavailable in TUI: canonical goal reporting support is not implemented.", nil
		case "add-constraint":
			return "Goal mutation is unavailable in TUI: authenticated runtime authorization is required.", nil
		case "rm-constraint":
			return "Goal mutation is unavailable in TUI: authenticated runtime authorization is required.", nil
		}
		return "Goal mutation is unavailable in TUI: authenticated runtime authorization is required.", nil

	case "/mode":
		if len(parts) < 2 {
			gate, _ := h.ws.ultraGate()
			return fmt.Sprintf("Current mode: %s (options: manual, auto, ultra; ULTRA entitled: %t)", h.ws.mode, gate.Entitled()), nil
		}
		mode := strings.ToLower(parts[1])
		switch mode {
		case "manual", "auto":
			h.ws.mode = mode
			h.ws.state.SessionMode = strings.ToUpper(mode)
			return fmt.Sprintf("Operating mode switched to %s.", strings.ToUpper(mode)), nil
		case "ultra":
			// Switching to ULTRA asks the same gate every other entry path
			// asks. Without a verified lease the mode does not change, so a
			// user cannot talk their way into ULTRA through the TUI.
			gate, _ := h.ws.ultraGate()
			if !gate.Entitled() {
				return "ULTRA is unavailable: no cryptographically verified entitlement is active.", nil
			}
			h.ws.mode = "ultra"
			h.ws.state.SessionMode = "ULTRA"
			return "Operating mode switched to ULTRA.", nil
		default:
			return "Invalid mode. Supported modes: manual, auto", nil
		}

	case "/update":
		return h.handleUpdate(ctx, parts[1:])

	case "/status":
		return h.handleStatus(ctx)
	case "/verification":
		if len(parts) != 2 {
			return "Usage: /verification <verification_id>", nil
		}
		return h.handleVerification(ctx, parts[1])

	case "/review":
		if len(parts) == 2 && strings.HasPrefix(parts[1], "ver-") {
			return h.handleVerification(ctx, parts[1])
		}
		return h.handleCodex(ctx, append([]string{"review"}, parts[1:]...), line)

	case "/learning":
		if len(parts) != 2 {
			return "Usage: /learning <memory_commit_id>", nil
		}
		return h.handleMemoryCommit(ctx, parts[1])

	case "/optimization":
		if len(parts) != 2 {
			return "Usage: /optimization <cycle_id>", nil
		}
		return h.handleOptimizationCycle(ctx, parts[1])

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
		if len(parts) > 1 && !strings.EqualFold(parts[1], "create") {
			return "Checkpoint " + strings.ToLower(parts[1]) + " is unavailable in TUI: authenticated runtime snapshot support is not implemented.", nil
		}
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
		if len(parts) > 1 {
			return h.handleCodex(ctx, append([]string{"resume"}, parts[1:]...), line)
		}
		return h.handleResume(ctx)

	case "/cancel":
		return h.handleCancel(ctx)

	case "/doctor":
		if len(parts) > 1 && (strings.EqualFold(parts[1], "codex") || strings.EqualFold(parts[1], "provider")) {
			return h.handleCodex(ctx, []string{"doctor"}, line)
		}
		return h.handleDoctor(ctx)

	case "/tasks", "/task":
		if len(parts) > 1 && !strings.EqualFold(parts[1], "list") && !strings.EqualFold(parts[1], "inspect") && !strings.EqualFold(parts[1], "ownership") {
			return "Task mutation is unavailable in TUI: authenticated runtime authorization is required.", nil
		}
		return h.handleTasks(ctx, parts[1:], line)

	case "/policy":
		return h.handlePolicy(ctx, parts[1:])

	case "/sandbox":
		if len(parts) > 1 {
			return h.handleCodex(ctx, append([]string{"sandbox"}, parts[1:]...), line)
		}
		return h.handleSandbox(ctx)

	case "/memory":
		return h.handleMemory(ctx, parts[1:], line)

	case "/provider", "/providers":
		return h.handleProvider(ctx, parts[1:])

	case "/harness":
		return h.handleHarness(ctx, parts[1:])

	case "/models":
		return h.handleCodex(ctx, []string{"models"}, line)

	case "/model":
		if len(parts) == 2 && !strings.EqualFold(parts[1], "show") && !strings.EqualFold(parts[1], "select") {
			return h.handleCodex(ctx, []string{"model", parts[1]}, line)
		}
		return h.handleModel(ctx, parts[1:])

	case "/effort":
		return h.handleEffort(ctx, parts[1:])

	case "/ultra":
		return h.handleUltra(ctx, parts[1:])

	case "/marshal":
		return h.handleMarshal(ctx, parts[1:])

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
		if len(parts) != 1 {
			return "Usage: /diff", nil
		}
		return h.handleDiff(ctx)

	case "/approvals":
		return h.handleApprovals(ctx, parts[1:])

	case "/approval":
		return h.handleApproval(ctx, parts[1:])

	case "/termination":
		return h.handleTermination(ctx)

	case "/context":
		return h.handleContext(ctx, parts[1:])

	case "/codex":
		return h.handleCodex(ctx, parts[1:], line)

	case "/claude":
		return h.handleClaude(ctx, parts[1:], line)

	case "/opencode":
		return h.handleOpenCode(ctx, parts[1:], line)
	case "/agy", "/antigravity":
		return h.handleAntigravity(ctx, parts[1:], line)

	case "/mcp":
		return h.handleCodex(ctx, append([]string{"mcp"}, parts[1:]...), line)

	case "/plugin", "/plugins":
		return h.handleCodex(ctx, append([]string{"plugin"}, parts[1:]...), line)

	case "/apply":
		return h.handleCodex(ctx, append([]string{"apply"}, parts[1:]...), line)

	case "/sessions":
		return h.handleCodex(ctx, append([]string{"sessions"}, parts[1:]...), line)

	case "/fork":
		return h.handleCodex(ctx, append([]string{"fork"}, parts[1:]...), line)

	case "/search":
		return h.handleCodex(ctx, append([]string{"search"}, parts[1:]...), line)

	case "/features":
		return h.handleCodex(ctx, append([]string{"features"}, parts[1:]...), line)

	case "/skills":
		return h.handleCodex(ctx, append([]string{"skills"}, parts[1:]...), line)

	case "/skill":
		return h.handleCodex(ctx, append([]string{"skill"}, parts[1:]...), line)

	case "/login":
		return h.handleCodex(ctx, []string{"login"}, line)

	case "/logout":
		return h.handleCodex(ctx, []string{"logout"}, line)

	default:
		if !strings.HasPrefix(line, "/") {
			// Plain text runs nothing. Launching an agent spends the operator's
			// tokens and can touch the worktree, so it happens only when they
			// name one: a typo, a stray paste or a command typed without its
			// slash must never be read as consent to start a session.
			return plainTextRunsNothing(line, h.ws.knownCommand), nil
		}
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

	// A goal change must not implicitly run the ULTRA router unless the
	// canonical Community Cloud gate still holds a verified entitlement. Clear
	// any explanation from a prior lease first so expiry cannot leave an ULTRA
	// label looking current in Standard mode. Explicit /route remains an
	// advisory simulation and is labelled NOT APPLIED.
	h.ws.state.RouteExplanation = ""
	if h.ws.router != nil && h.ws.ultra != nil && h.ws.ultra.Entitled() {
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
		return "No registered team participants. Harness availability is discovered when the workspace refreshes.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("TEAM ROSTER (%d agents):\n", len(h.ws.state.Participants)))
	for _, p := range h.ws.state.Participants {
		active := "unavailable"
		if p.IsActive {
			active = "available"
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
	evidenceID = strings.TrimPrefix(evidenceID, "#")
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

	// Check the live gate before reading a cached explanation as leases can
	// expire between the action that computed it and this command.  Clearing
	// the cache prevents a later entitled session from mistaking an old route
	// for a fresh one.
	if h.ws.ultra == nil || !h.ws.ultra.Entitled() {
		h.ws.state.RouteExplanation = ""
		return "No ULTRA route explanation is available: the canonical entitlement is not active.", nil
	}

	if h.ws.state.RouteExplanation != "" {
		return fmt.Sprintf("ADVISORY ROUTING EXPLANATION (NOT APPLIED):\n%s", h.ws.state.RouteExplanation), nil
	}

	// A route explanation is meaningful only while the same canonical Cloud
	// gate that authorizes ULTRA execution still holds a verified entitlement.
	// Calling the ULTRA router after expiry would not execute anything, but it
	// would still expose an ULTRA-labelled recommendation in a Standard
	// session and create a misleading authority boundary.
	if h.ws.router != nil && h.ws.ultra != nil && h.ws.ultra.Entitled() {
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

	return "No route explanation is available yet.", nil
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
	cpID = strings.TrimPrefix(cpID, "#")
	return fmt.Sprintf("Rollback to %s was NOT performed: authenticated runtime restoration is not implemented.", cpID), nil
}

func (h *CommandHandler) handleBudget(ctx context.Context) (string, error) {
	h.ws.mu.Lock()
	defer h.ws.mu.Unlock()

	tokStr := "UNKNOWN"
	if h.ws.state.BudgetConsumed.TotalTokens != nil {
		tokStr = fmt.Sprintf("%d", *h.ws.state.BudgetConsumed.TotalTokens)
	}
	costStr := "UNKNOWN"
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
	navigationHint := ""
	if h.ws.navigationRefusal() == "" {
		navigationHint = "     Ctrl+N: Navigation · Esc: Navigation (empty composer)"
	}
	return `MARSHAL Terminal Workspace Commands:
  /status                  Show canonical session, goal, team, claim, budget, and termination status
  /goal [outcome]          View active goal; edits unavailable (runtime authorization required)
  /goal constraints        List bound constraints
  /goal create|edit|add-constraint|rm-constraint  Edits unavailable (runtime authorization required)
  /goal diff|version|criteria|donotdo|progress  Reporting unavailable (not implemented)
  /marshal <goal>          Plan with a Marshal model, then marshal the work to agents (/marshal help)
  /mode [manual|auto|ultra] Switch session supervision mode label; ULTRA requires entitlement
  /ultra [status|start|stop|request]  Show ULTRA status, switch execution on or off, or request entitlement
  /agents, /roster         List registered participants, fixed roles, and harnesses
  /claims                  List active claims and epistemic verification states
  /tasks, /task [list|inspect <id>|ownership]  Read tasks; mutations unavailable
  /task create|assign|pause|resume|cancel|retry  Mutations unavailable (runtime authorization required)
  /budget                  Show consumed budget (unknown tokens/cost stay UNKNOWN)
  /learning <id>           Inspect a Process 07 memory commit, promotions and refusals
  /optimization <id>       Inspect a Process 08 governed optimization cycle and refusals
  /memory [list|search <query>|provenance <id>]  Read project memory records
  /memory peers [agent authors…]  Show or set cross-agent visibility
  /memory inject [chan]    Govern how a native session receives the other agents' work
  /memory-search <proj>    Search durable memory with state, freshness and contradictions
  /memory-stale <proj>     Include stale memory, always marked unusable
  /provenance <item>       Show one memory item's full version history
  /trust [task-class]      Measured routing outcomes, failures and selection bias
  /fingerprints <proj>     Bounded failure fingerprints
  /playbooks <proj>        Playbook candidates awaiting review
  /replay-index [run]      Replay and reproducibility index
  /inspect [kind] <id>     Inspect a claim, evidence, checkpoint, task, handoff, approval, or agent
  /evidence <id>           Show supporting/contradicting evidence links in active claims
  /approve [approval_id]   Unavailable: authenticated runtime authorization required
  /reject [approval_id]    Unavailable: authenticated runtime authorization required
  /route [key=value ...]   Compute an advisory route; it is not applied to Runtime
  /why                     Explain advisory routing; verified ULTRA entitlement required
  /msg, /say <agent|all> <text>  Unavailable: authenticated runtime authorization required
  /handoff <role> <summary> Unavailable: authenticated runtime authorization required
  /checkpoint [list|create|inspect|diff]  Unavailable: runtime snapshot support required
  /rollback <id>           Unavailable: runtime restoration support required
  /pause                   Unavailable: authenticated runtime process control required
  /resume [id|--last]      No args: runtime control unavailable; args: native Codex resume
  /cancel                  Unavailable: authenticated runtime process control required
  /review [instructions]   Native Codex review; governed commit review takes no instructions
  /approvals               List pending approvals
  /approval                Show native Codex approval policy
  /termination             Inspect termination state
  /context                 Inspect context and drift
  /diff                    Interactive diff inspector for pending changes
  /models                  List discovered models and active selection
  /model [show]            Read saved harness model preferences (not applied to Runtime)
  /model <slug>            Select a Codex execution model through its control authority
  /model select <harness> <model>  Unavailable: runtime execution-profile integration required
  /harness [probe|status|select <role> <harness>]  Probe availability; selection unavailable
  /effort [low|medium|high] Read probed knobs/advisory default; selection UNKNOWN, changes unavailable
  /provider [status|config <name>]  Probe availability; credentials stay in the harness (/providers alias)
  /policy [network|sandbox|capability|scope|write|audit]  Enforcement NOT VERIFIED
  /sandbox [read-only|workspace-write]  No args: NOT VERIFIED; mode: open native Codex
  /backup [create|restore <backup_path>]  Create verified snapshot; restore only verifies
  /fingerprint             Report per-run fingerprint history unavailable
  /runtime                 Report runtime execution health NOT VERIFIED
  /store                   Read SQLite schema version (integrity NOT VERIFIED)
  /export                  Write evidence bundle for the current canonical goal revision
  /blind [resolve [reason ...]]  Interpretation NOT VERIFIED; resolution unavailable
  /reinjection             Report execution-bound constraint digest NOT VERIFIED
  /alignment [scope|violations|blast|deletions|status|resolve [reason ...]]  NOT VERIFIED; resolution unavailable
  /features [list|enable <feature>|disable <feature>]  Native Codex feature flags
  /login                   Open native Codex login in an interactive terminal
  /logout                  Open native Codex logout in an interactive terminal
  /mcp [list|add|rm]       Manage Codex MCP server integrations
  /plugin /plugins [list|add|rm]   Manage Codex plugins and extensions
  /apply [task_id]         Apply a Codex task diff to working tree
  /skills                  List local Codex skills
  /skill install <name>    Install a project-local Codex skill
  /sessions               List governed Codex sessions
  /fork [id|--last]        Fork a native Codex session
  /doctor [codex|provider] Run system diagnostics, or native Codex doctor
  /search [on|off]         Open a native Codex session with that search setting
  /codex [subcommand]      Full Codex control plane (status, models, review, exec, run, cli)
  /claude [subcommand]     Full Claude control plane (status, models, doctor, exec, run)
  /opencode [subcommand]   Native OpenCode sessions (new, continue, resume, fork, cli)
  /agy /antigravity [subcommand] Native Antigravity sessions (new, continue, resume, cli)
  <prompt...>              Plain text runs nothing; choose /codex, /claude, /opencode, or /agy
  /update [install]        Check for a newer MARSHAL release, or install it
  /verification <id>       Inspect a canonical verification run
  /help, /?                Show this help reference
  /quit, /exit             Exit TUI workspace (session remains durable in SQLite)

Function Keys & Shortcuts:
  F1: Help       F2: Review     F3: Diff viewer
  F4: Status     F5: Models     F6: MCP servers
  F7: Codex      F8: Claude     F9: OpenCode     F12: Antigravity
  F10: Update    F11: Unassigned` + navigationHint + `

Composer:
  /  or  @                 Opens the command menu as you type
  Up / Down                Move the highlight; the draft is left alone
  Tab / Shift+Tab          Cycle candidates into the draft; never submit
  Enter                    Accept a candidate; run a finished command with no explicit selection
  Esc                      Dismiss the menu, keeping what you typed
  Paste                    Long or multi-line pastes collapse to a placeholder
                           and are restored in full when you submit
  Select & copy            Works with the terminal's own selection`
}

// plainTextRunsNothing explains why a bare line did nothing, and names the ways
// to actually run something.
//
// The commonest reason for landing here is a command typed without its leading
// slash, so that case is answered with the command rather than with a lecture.
func plainTextRunsNothing(line string, known func(string) bool) string {
	trimmed := strings.TrimSpace(line)
	if first := strings.Fields(trimmed); len(first) > 0 && known != nil {
		if candidate := "/" + first[0]; known(candidate) {
			return fmt.Sprintf("Nothing was run. Did you mean %s?\n"+
				"  Commands need their leading slash.", candidate)
		}
	}
	return "Nothing was run: plain text does not start an agent.\n" +
		"  /codex <prompt>   Send this to Codex\n" +
		"  /claude <prompt>  Send this to Claude\n" +
		"  /opencode <prompt> Open a native OpenCode session\n" +
		"  /agy <prompt>     Open a native Antigravity session\n" +
		"  F7 / F8 / F9 / F12  Open Codex, Claude, OpenCode, or Antigravity\n" +
		"  /help             List every command"
}

// knownCommand reports whether a token names a command this workspace has.
func (w *Workspace) knownCommand(candidate string) bool {
	if w == nil || w.completer == nil {
		return false
	}
	_, matches := w.completer.Suggest(candidate, len([]rune(candidate)))
	for _, m := range matches {
		if m == candidate {
			return true
		}
	}
	return false
}
