package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	rawLine := line
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
				return "No active goal set. Use /goal create <request>.", nil
			}
			return fmt.Sprintf("Active Goal [v%d]: %s (ID: %s)",
				h.ws.state.Goal.Revision, h.ws.state.Goal.DesiredOutcome, h.ws.state.Goal.ID), nil
		}
		// Subcommands are routed before the free-text path. Without this any
		// "/goal add-constraint no-net" was swallowed whole and written as the
		// desired outcome, silently destroying the goal statement.
		switch strings.ToLower(parts[1]) {
		case "constraints":
			if len(parts) != 2 {
				return goalUsage, nil
			}
			return h.handleGoalConstraints(ctx)
		case "diff", "version", "criteria", "donotdo", "progress":
			return h.handleGoalReport(ctx, strings.ToLower(parts[1]), parts[2:])
		case "create", "edit", "add-constraint", "rm-constraint":
			return h.handleGoalMutation(ctx, strings.ToLower(parts[1]), goalCommandText(rawLine))
		}
		return "Goal mutation is unavailable in TUI for unknown verbs.\n" + goalUsage, nil

	case "/mode":
		const modeMeaning = "The mode is a supervision preference: it grants no authority, hard approvals always stay with you, and ULTRA execution is switched separately with /ultra start|stop."
		if len(parts) < 2 {
			gate, execution := h.ws.ultraGate()
			h.ws.mu.RLock()
			mode := h.ws.mode
			h.ws.mu.RUnlock()
			return fmt.Sprintf("Current mode: %s (options: manual, auto, ultra; ULTRA entitled: %t; ULTRA execution: %s)\n%s",
				effectiveModeLabel(mode, gate.Entitled()), gate.Entitled(), onOff(execution && gate.Entitled()), modeMeaning), nil
		}
		mode := strings.ToLower(parts[1])
		switch mode {
		case "manual", "auto":
			h.ws.mu.Lock()
			h.ws.mode = mode
			h.ws.state.SessionMode = strings.ToUpper(mode)
			h.ws.mu.Unlock()
			return fmt.Sprintf("Operating mode switched to %s. %s", strings.ToUpper(mode), modeMeaning), nil
		case "ultra":
			// Switching to ULTRA asks the same gate every other entry path
			// asks. Without a verified lease the mode does not change, so a
			// user cannot talk their way into ULTRA through the TUI. The label
			// never turns ULTRA execution on.
			gate, execution := h.ws.ultraGate()
			if !gate.Entitled() {
				return "ULTRA is unavailable: no cryptographically verified entitlement is active.", nil
			}
			h.ws.mu.Lock()
			h.ws.mode = "ultra"
			h.ws.state.SessionMode = "ULTRA"
			h.ws.mu.Unlock()
			if !execution {
				return "Operating mode switched to ULTRA. ULTRA execution is still OFF; turn it on with /ultra start.", nil
			}
			return "Operating mode switched to ULTRA. ULTRA execution is ON.", nil
		default:
			return "Invalid mode. Supported modes: manual, auto, ultra", nil
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
		return h.handleApprovalDecision(ctx, parts[1:], true)

	case "/reject":
		return h.handleApprovalDecision(ctx, parts[1:], false)

	case "/route":
		return h.handleRoute(ctx, parts[1:])

	case "/agents", "/roster":
		return h.handleAgents(ctx)

	case "/claims":
		return h.handleClaims(ctx)

	case "/evidence":
		if len(parts) < 2 {
			return "Usage: /evidence <evidence_id> | /evidence list", nil
		}
		if strings.EqualFold(parts[1], "list") {
			return h.handleEvidenceList(ctx)
		}
		return h.handleEvidence(ctx, parts[1])

	case "/why":
		return h.handleWhy(ctx)

	case "/msg", "/say":
		return h.handleCollaboration(ctx, "message", parts[1:])

	case "/handoff":
		return h.handleCollaboration(ctx, "handoff", parts[1:])

	case "/checkpoint":
		if len(parts) == 1 {
			return h.handleCheckpointRead(ctx, []string{"list"})
		}
		if !strings.EqualFold(parts[1], "create") {
			return h.handleCheckpointRead(ctx, parts[1:])
		}
		return h.handleCheckpointCreate(ctx, strings.Join(parts[2:], " "))

	case "/rollback":
		return h.handleRollback(ctx, parts[1:])

	case "/budget":
		if len(parts) > 1 {
			return h.handleBudgetSet(ctx, parts[1:])
		}
		out, err := h.handleBudget(ctx)
		if err != nil {
			return out, err
		}
		return out + h.budgetLimitsAndRuns(ctx), nil

	case "/pause":
		return h.handleRunControl(ctx, "pause", parts[1:])

	case "/resume":
		// "run:<id>" or no argument controls a MARSHAL run; anything else is
		// native Codex resume syntax such as --last.
		if len(parts) > 1 && !strings.HasPrefix(parts[1], "run:") {
			return h.handleCodex(ctx, append([]string{"resume"}, parts[1:]...), line)
		}
		if len(parts) > 2 {
			return "Usage: /resume [run:<id>]  (native Codex: /resume --last)", nil
		}
		return h.handleRunControl(ctx, "resume", parts[1:])

	case "/cancel":
		return h.handleRunControl(ctx, "cancel", parts[1:])

	case "/doctor":
		if len(parts) > 1 && (strings.EqualFold(parts[1], "codex") || strings.EqualFold(parts[1], "provider")) {
			return h.handleCodex(ctx, []string{"doctor"}, line)
		}
		return h.handleDoctor(ctx)

	case "/tasks", "/task":
		if len(parts) > 1 && !strings.EqualFold(parts[1], "list") && !strings.EqualFold(parts[1], "inspect") && !strings.EqualFold(parts[1], "ownership") && !strings.EqualFold(parts[1], "--scope") {
			return h.handleTaskMutation(ctx, parts[1:])
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
		return h.handleStore(ctx, parts[1:])

	case "/export":
		return h.handleExport(ctx, parts[1:])

	case "/blind":
		return h.handleBlind(ctx, parts[1:])

	case "/reinjection":
		return h.handleReinjection(ctx)

	case "/alignment":
		return h.handleAlignment(ctx, parts[1:])

	case "/diff":
		if len(parts) > 2 || (len(parts) == 2 && parts[1] != "staged" && parts[1] != "unstaged" && parts[1] != "untracked") {
			return "Usage: /diff [staged|unstaged|untracked]", nil
		}
		scope := ""
		if len(parts) == 2 {
			scope = parts[1]
		}
		return h.handleDiffScope(ctx, scope)

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

	// Explain the exact route /route last computed, while it still describes
	// the current goal revision. A route for another revision is stale.
	if last := h.ws.lastRoute; last != nil {
		goal := h.ws.state.Goal
		if last.goalID == goal.ID && last.goalRevision == goal.Revision {
			return "ADVISORY ROUTING EXPLANATION (NOT APPLIED):\n" + last.explanation, nil
		}
		h.ws.lastRoute = nil
		return fmt.Sprintf("The last route (%s) was computed for goal %s revision %d, and the goal has changed since. Run /route again.",
			last.request, orNone(last.goalID), last.goalRevision), nil
	}

	if h.ws.state.RouteExplanation != "" {
		return fmt.Sprintf("ADVISORY ROUTING EXPLANATION (NOT APPLIED):\n%s", h.ws.state.RouteExplanation), nil
	}

	// A route explanation is meaningful only while the same canonical Cloud
	// gate that authorizes ULTRA execution still holds a verified entitlement.
	if h.ws.router != nil {
		plan, err := h.ws.router.Route(ctx, model.ULTRARouteRequest{
			GoalID:    h.ws.state.Goal.ID,
			FixedRole: model.RoleDeveloper,
			Risk:      model.R1,
		})
		if err == nil {
			return fmt.Sprintf("ADVISORY ROUTING EXPLANATION (NOT APPLIED):\nNo /route request yet; default developer route at R1.\n%s", plan.Explanation), nil
		}
	}

	return "No route explanation is available yet.", nil
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

func (h *CommandHandler) helpText() string {
	navigationHint := ""
	if h.ws.navigationRefusal() == "" {
		navigationHint = "     Ctrl+N: Navigation · Esc: Navigation (empty composer)"
	}
	return `MARSHAL Terminal Workspace Commands:
  /status                  Show canonical session, goal, team, claim, budget, and termination status
  /goal                    View the active goal
  /goal constraints        List bound constraints
  /goal create|edit|add-constraint|rm-constraint  Create or revise the goal as the local owner
  /goal version [revision] | diff [from to] | criteria | donotdo | progress  Read canonical reports
  /marshal <goal>          Plan with a Marshal model, then marshal the work to agents (/marshal help)
  /mode [manual|auto|ultra] Switch session supervision mode label; ULTRA requires entitlement
  /ultra [status|start|stop|request]  Show ULTRA status, switch execution on or off, or request entitlement
  /agents, /roster         List registered participants, fixed roles, and harnesses
  /claims                  List active claims and epistemic verification states
  /tasks, /task [list|inspect <id>|ownership] [--scope project|active]  Read tasks
  /task create|assign|pause|resume|cancel|retry  Authenticated task controls
  /budget [set calls=<n> duration=<d>|clear]  Show consumption and limits; set limits via a goal revision
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
  /evidence <id>           Show an artifact (bytes re-checked) or evidence reference, with every linked claim
  /evidence list           List stored artifacts and claim evidence references
  /approve [approval_id]   Unavailable: authenticated runtime authorization required
  /reject [approval_id]    Unavailable: authenticated runtime authorization required
  /route [key=value ...]   Compute an advisory route; it is not applied to Runtime
  /why                     Explain advisory routing; verified ULTRA entitlement required
  /msg, /say <agent|all> <text>  Message the team session as the local owner
  /handoff <role> <summary> Hand the session turn to the active participant of a role
  /checkpoint list | inspect <id> | diff <from> <to>  Read the project's snapshots; files re-checked
  /checkpoint create <reason>  Snapshot the project's files
  /rollback <id> [confirm <digest>]  Preview, then restore a snapshot (recovery point kept)
  /pause [run:<id>]        Pause a run of this session (stops new dispatch)
  /resume [run:<id>]       Resume a paused run; /resume --last: native Codex resume
  /cancel [run:<id>]       Cancel a run of this session and its provider turns
  /review [instructions]   Native Codex review; governed commit review takes no instructions
  /approvals               List pending approvals
  /approval                Show native Codex approval policy
  /termination             Inspect termination state
  /context                 Inspect context and drift
  /diff [staged|unstaged|untracked]  Bounded diff inventory (default: combined)
  /models                  List discovered models and active selection
  /model [show]            Show execution model preferences and saved harness defaults
  /model <slug>            Select a Codex execution model through its control authority
  /model select <codex|claude> <model>  Set the model future governed runs use
  /harness [probe|status|select <role> <harness>]  Probe availability; selection unavailable
  /effort [<level>|default]  Show or set the reasoning effort future Codex runs request
  /provider [status|config <name>]  Probe availability; credentials stay in the harness (/providers alias)
  /policy [network|sandbox|capability|scope|write|audit]  Enforcement NOT VERIFIED
  /sandbox [read-only|workspace-write]  No args: NOT VERIFIED; mode: open native Codex
  /backup [create|restore <backup_path> [confirm <digest>]]  Create a verified snapshot; preview, then restore one
  /fingerprint             Report per-run fingerprint history unavailable
  /runtime                 Report runtime execution health NOT VERIFIED
  /store [check quick|check full|counts]  Schema, quick check and inventory; full can take long (5s timeout)
  /export                  Write evidence bundle for the current canonical goal revision
  /reinjection             Report execution-bound constraint digest NOT VERIFIED
  /alignment [scope|violations|blast|deletions|status]  Advisory alignment results for this session's tasks
  /alignment resolve run:<run>/<task>#<n> <acknowledged|goal-amendment-needed> <reason>  Record a decision
  /features [list|enable <feature>|disable <feature>]  Native Codex feature flags
  /login                   Open native Codex login in an interactive terminal
  /logout                  Open native Codex logout in an interactive terminal
  /mcp [list|add|rm]       Manage Codex MCP server integrations
  /plugin /plugins [list|add|rm]   Manage Codex plugins and extensions
  /apply <codex_task_id>   Apply a Codex task diff (snapshot first; changed files reported)
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
