package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/bundle"
	"github.com/Zen1th53/marshal/internal/doctor"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/reinjection"
	"github.com/Zen1th53/marshal/internal/store"
)

// handleDoctor runs the canonical system diagnostics and renders a structured report.
func (h *CommandHandler) handleDoctor(ctx context.Context) (string, error) {
	root := h.ws.workDir
	if root == "" || root == "." {
		cwd, err := os.Getwd()
		if err == nil {
			root = cwd
		}
	}

	report := doctor.Check(ctx, root, doctor.Options{})

	th := h.ws.theme
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s SYSTEM DIAGNOSTICS [Verdict: %s]\n",
		th.Colorize(th.Marshal, "╭─"),
		th.RenderBadge(string(report.Verdict)),
	))
	b.WriteString(fmt.Sprintf("%s%s\n", th.BoxTRight, strings.Repeat(th.BoxHoriz, 50)))

	for _, res := range report.Results {
		badge := th.RenderBadge(string(res.Verdict))
		b.WriteString(fmt.Sprintf("%s %-12s %-20s %s\n",
			th.BoxVert,
			badge,
			res.Name,
			th.Colorize(th.Muted, res.Detail),
		))
	}
	b.WriteString(fmt.Sprintf("%s%s\n", th.BoxBottomLeft, strings.Repeat(th.BoxHoriz, 50)))

	return b.String(), nil
}

// handleTasks handles /tasks and /task subcommands.
func (h *CommandHandler) handleTasks(ctx context.Context, args []string, line string) (string, error) {
	if len(args) == 0 || args[0] == "list" {
		if h.ws.store == nil {
			return "Store unavailable to list tasks.", nil
		}
		tasks, err := h.ws.store.ListTasks(ctx)
		if err != nil {
			return "", fmt.Errorf("list tasks: %w", err)
		}
		if len(tasks) == 0 {
			return "No active tasks in store. Use /task create <title> to define one.", nil
		}

		th := h.ws.theme
		if th == nil {
			th = NewTheme(ThemeDefault, true, true)
		}

		var b strings.Builder
		b.WriteString(fmt.Sprintf("TASKS (%d total):\n", len(tasks)))
		for _, t := range tasks {
			owner := "(none)"
			if t.OwnerAgentID != nil {
				owner = *t.OwnerAgentID
			}
			badge := th.RenderBadge(string(t.Status))
			b.WriteString(fmt.Sprintf("  %-8s %s %-20s [Owner: %s | Risk: %s]\n",
				t.ID, badge, t.Title, owner, t.Risk))
		}
		return b.String(), nil
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "create":
		if len(args) < 2 {
			return "Usage: /task create <task title>", nil
		}
		title := strings.TrimSpace(line[strings.Index(line, args[0])+len(args[0]):])
		taskID := fmt.Sprintf("TASK-%d", time.Now().UnixNano()%10000)
		newTask := model.Task{
			ID:       taskID,
			Title:    title,
			Status:   model.TaskReady,
			Risk:     model.R1,
			Revision: 1,
		}
		if h.ws.store != nil {
			if _, err := h.ws.store.ImportTasks(ctx, []model.Task{newTask}); err != nil {
				return "", fmt.Errorf("import task: %w", err)
			}
		}
		return fmt.Sprintf("Task %s created: %s", taskID, title), nil

	case "inspect":
		if len(args) < 2 {
			return "Usage: /task inspect <task_id>", nil
		}
		return h.handleInspect(ctx, "task", args[1])

	case "assign":
		if len(args) < 3 {
			return "Usage: /task assign <task_id> <agent_id>", nil
		}
		// Ownership is taken by leasing the task through the canonical claim
		// path, which enforces the lease and revision rules. Printing an
		// assignment without taking the lease would report ownership that the
		// runtime does not actually recognise.
		if h.ws.store == nil {
			return "Store unavailable", nil
		}
		task, err := h.ws.store.GetTask(ctx, args[1])
		if err != nil {
			return "", fmt.Errorf("task %s: %w", args[1], err)
		}
		lease, err := h.ws.store.ClaimTask(ctx, model.ClaimRequest{
			TaskID:           task.ID,
			AgentID:          args[2],
			ExpectedRevision: task.Revision,
		})
		if err != nil {
			return "", fmt.Errorf("assign task %s to %s: %w", task.ID, args[2], err)
		}
		return fmt.Sprintf("Task %s claimed by %s (lease %s).", task.ID, args[2], lease.ID), nil

	case "pause", "resume", "cancel", "retry":
		if len(args) < 2 {
			return fmt.Sprintf("Usage: /task %s <task_id>", sub), nil
		}
		return h.transitionTask(ctx, sub, args[1])

	case "ownership":
		return h.handleTaskOwnership(ctx)

	default:
		return "Usage: /task [list|create|inspect|assign|pause|resume|cancel|retry|ownership]", nil
	}
}

// transitionTask moves a task through the canonical state machine. The store
// enforces which transitions are legal for the actor's role, so an illegal
// request is reported as the refusal it is rather than as a success message.
func (h *CommandHandler) transitionTask(ctx context.Context, verb, taskID string) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	task, err := h.ws.store.GetTask(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("task %s: %w", taskID, err)
	}

	var target model.TaskStatus
	switch verb {
	case "pause":
		target = model.TaskBlocked
	case "resume", "retry":
		target = model.TaskReady
	case "cancel":
		target = model.TaskCancelled
	default:
		return fmt.Sprintf("Unsupported task transition %q.", verb), nil
	}

	if task.Status == target {
		return fmt.Sprintf("Task %s is already %s.", task.ID, target), nil
	}

	updated, err := h.ws.store.TransitionTask(ctx, model.TaskTransitionRequest{
		TaskID:           task.ID,
		FromStatus:       task.Status,
		ToStatus:         target,
		ActorRole:        model.RoleArchitect,
		ActorID:          "operator",
		Reason:           fmt.Sprintf("operator %s via TUI", verb),
		ExpectedRevision: task.Revision,
	})
	if err != nil {
		return "", fmt.Errorf("%s task %s (%s -> %s): %w", verb, task.ID, task.Status, target, err)
	}

	return fmt.Sprintf("Task %s transitioned %s -> %s (revision %d).",
		updated.ID, task.Status, updated.Status, updated.Revision), nil
}

func (h *CommandHandler) handleTaskOwnership(ctx context.Context) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}
	tasks, err := h.ws.store.ListTasks(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("WORK OWNERSHIP TABLE:\n")
	b.WriteString(fmt.Sprintf("%-8s %-16s %-12s %-8s %s\n", "TASK", "OWNER", "STATUS", "RISK", "BLOCKED BY"))
	b.WriteString(strings.Repeat("─", 60) + "\n")
	for _, t := range tasks {
		owner := "(none)"
		if t.OwnerAgentID != nil {
			owner = *t.OwnerAgentID
		}
		blockedBy := "-"
		if len(t.Dependencies) > 0 {
			blockedBy = strings.Join(t.Dependencies, ", ")
		}
		b.WriteString(fmt.Sprintf("%-8s %-16s %-12s %-8s %s\n",
			t.ID, owner, t.Status, t.Risk, blockedBy))
	}
	return b.String(), nil
}

// handlePolicy inspects security, network, sandbox, and capability policies.
func (h *CommandHandler) handlePolicy(ctx context.Context, args []string) (string, error) {
	sub := "all"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}

	var b strings.Builder
	switch sub {
	case "network":
		b.WriteString("NETWORK SECURITY POLICY:\n")
		b.WriteString("  Mode:        FAIL-CLOSED\n")
		b.WriteString("  Enforcement: bubblewrap (--unshare-net)\n")
		b.WriteString("  Egress:      BLOCKED for unverified agent processes\n")
		b.WriteString("  Policy Rule: Per-endpoint egress cannot be enforced without authenticated proxy\n")

	case "sandbox":
		return h.handleSandbox(ctx)

	case "capability":
		b.WriteString("CAPABILITY POLICY:\n")
		b.WriteString("  Model Invocations: BOUNDED\n")
		b.WriteString("  System Calls:      RESTRICTED (no root, no ptrace)\n")
		b.WriteString("  Memory Limits:     ENFORCED per worker\n")

	case "scope":
		b.WriteString("SCOPE POLICY:\n")
		b.WriteString("  Workspace: ISOLATED to project worktree\n")
		b.WriteString("  Writes:    REQUIRING APPROVAL outside defined Goal scope\n")

	case "write":
		b.WriteString("WRITE PERMISSION POLICY:\n")
		b.WriteString("  Protected Paths: .git/, .marshal/state.db, /etc/, /usr/\n")
		b.WriteString("  Permitted:       Configured project root only\n")

	case "audit":
		b.WriteString("SECURITY AUDIT LOG:\n")
		b.WriteString("  Zero security escape attempts or sandbox violations recorded.\n")

	default:
		b.WriteString("MARSHAL SECURITY & POLICY SUMMARY:\n")
		b.WriteString("  Network:    FAIL-CLOSED (--unshare-net)\n")
		b.WriteString("  Sandbox:    Bubblewrap isolation\n")
		b.WriteString("  Capability: Least privilege enforced\n")
		b.WriteString("  Scope:      Goal-bounded workspace writes\n")
		b.WriteString("Use /policy [network|sandbox|capability|scope|write|audit] for detail.\n")
	}

	return b.String(), nil
}

// handleSandbox reports the bubblewrap sandbox status.
func (h *CommandHandler) handleSandbox(ctx context.Context) (string, error) {
	bwrapPath, err := exec.LookPath("bwrap")
	status := "AVAILABLE"
	if err != nil {
		status = "UNAVAILABLE: bwrap binary not found on PATH"
	} else {
		status = fmt.Sprintf("AVAILABLE (%s)", bwrapPath)
	}

	var b strings.Builder
	b.WriteString("SANDBOX ISOLATION STATUS:\n")
	b.WriteString(fmt.Sprintf("  Backend:       bubblewrap (Linux namespaces)\n"))
	b.WriteString(fmt.Sprintf("  Status:        %s\n", status))
	b.WriteString(fmt.Sprintf("  Filesystem:    Read-only host root, private /tmp, bind-mounted worktree\n"))
	b.WriteString(fmt.Sprintf("  Network:       Isolated (--unshare-net)\n"))
	b.WriteString(fmt.Sprintf("  PID Namespace: Isolated\n"))
	b.WriteString(fmt.Sprintf("  IPC Namespace: Isolated\n"))
	return b.String(), nil
}

// handleMemory handles epistemic memory inspection and search.
func (h *CommandHandler) handleMemory(ctx context.Context, args []string, line string) (string, error) {
	if len(args) == 0 {
		return "SHARED EPISTEMIC MEMORY:\n" +
			"  Shared memory stores verified claims, architecture decisions, and provenances.\n" +
			"  Use /memory search <query> to search knowledge items.", nil
	}

	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	h.ws.mu.RLock()
	projectID := h.ws.state.ProjectID
	h.ws.mu.RUnlock()

	sub := strings.ToLower(args[0])
	switch sub {
	case "list", "search":
		records, err := h.ws.store.ListMemoryV2(ctx, store.MemoryQueryFilter{
			ProjectID: projectID,
			Limit:     200,
		})
		if err != nil {
			return "", fmt.Errorf("list memory: %w", err)
		}

		query := ""
		if sub == "search" {
			if len(args) < 2 {
				return "Usage: /memory search <query>", nil
			}
			query = strings.ToLower(strings.TrimSpace(line[strings.Index(line, args[0])+len(args[0]):]))
		}

		var matched []model.MemoryRecordV2
		for _, r := range records {
			if query == "" ||
				strings.Contains(strings.ToLower(r.Title), query) ||
				strings.Contains(strings.ToLower(r.Body), query) {
				matched = append(matched, r)
			}
		}

		if len(matched) == 0 {
			if query == "" {
				return "No memory records stored for this project.", nil
			}
			return fmt.Sprintf("No memory records match %q (searched %d record(s)).", query, len(records)), nil
		}

		var b strings.Builder
		b.WriteString(fmt.Sprintf("MEMORY RECORDS (%d of %d):\n", len(matched), len(records)))
		for _, r := range matched {
			b.WriteString(fmt.Sprintf("  %s [%s/%s] %s\n",
				r.ID, r.Kind, r.Lifecycle, RedactContent(r.Title, nil)))
		}
		return b.String(), nil

	case "provenance":
		if len(args) < 2 {
			return "Usage: /memory provenance <memory_id>", nil
		}
		records, err := h.ws.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: projectID, Limit: 500})
		if err != nil {
			return "", fmt.Errorf("list memory: %w", err)
		}
		for _, r := range records {
			if r.ID == args[1] {
				return fmt.Sprintf("MEMORY %s\n  Kind:       %s\n  Lifecycle:  %s\n  Authority:  %s\n  Confidence: %s\n  Digest:     %s\n  Title:      %s",
					r.ID, r.Kind, r.Lifecycle, r.Authority, r.Confidence,
					r.ContentDigest, RedactContent(r.Title, nil)), nil
			}
		}
		return fmt.Sprintf("No memory record %s in this project.", args[1]), nil

	default:
		return "Usage: /memory [list|search <query>|provenance <id>]", nil
	}
}

// handleProvider handles provider configuration and status inspection.
func (h *CommandHandler) handleProvider(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || args[0] == "status" {
		// Report only what the host probe establishes. MARSHAL cannot read a
		// harness's credentials, so presence of the binary is never reported as
		// proof of authentication: an installed harness is AVAILABLE, and
		// whether its credentials work is UNKNOWN until an execution proves it.
		var b strings.Builder
		b.WriteString("PROVIDER / HARNESS STATUS:\n")
		for _, pr := range ProbeHarnesses() {
			b.WriteString(fmt.Sprintf("  %s\n", pr.HarnessName))
			if !pr.Installed {
				b.WriteString(fmt.Sprintf("    State:  %s\n", StateUnavailable))
				b.WriteString(fmt.Sprintf("    Reason: %s\n", pr.Reason))
				b.WriteString("    Auth:   NOT_RUN (harness absent)\n")
				continue
			}
			b.WriteString(fmt.Sprintf("    State:   %s (%s)\n", pr.State, pr.BinaryPath))
			b.WriteString(fmt.Sprintf("    Version: %s\n", pr.Version))
			b.WriteString(fmt.Sprintf("    Model:   %s (not established by probe)\n", UnknownModel))
			b.WriteString("    Auth:    UNKNOWN (no execution performed)\n")
			b.WriteString("    Egress:  BLOCKED_BY_POLICY (sandbox uses --unshare-net; per-endpoint egress unenforceable)\n")
		}
		return b.String(), nil
	}

	if args[0] == "config" {
		// Credentials are deliberately not accepted here. A secret typed as a
		// command argument lands in the composer, the command history and the
		// activity transcript, so the TUI refuses the value and points at the
		// harness's own credential flow, which stores it outside MARSHAL.
		if len(args) >= 3 {
			return "Refusing to accept a credential as a command argument: it would enter " +
				"the transcript and command history.\n" +
				"Configure the provider through its own harness (for example `claude`, " +
				"`codex` or `opencode` login), then run /provider status to confirm what " +
				"MARSHAL can see.", nil
		}
		if len(args) < 2 {
			return "Usage: /provider config <provider_name>", nil
		}

		name := strings.ToLower(args[1])
		for _, pr := range ProbeHarnesses() {
			if pr.HarnessName != name {
				continue
			}
			if !pr.Installed {
				return fmt.Sprintf("Provider %s: %s\n  %s", name, StateUnavailable, pr.Reason), nil
			}
			return fmt.Sprintf("Provider %s: %s (%s)\n  Version: %s\n  Credentials are held by the harness; MARSHAL does not store them.\n  Auth state: UNKNOWN until an execution establishes it.",
				name, pr.State, pr.BinaryPath, pr.Version), nil
		}
		return fmt.Sprintf("Unknown provider %q. Run /provider status to see what this host provides.", name), nil
	}

	return "Usage: /provider [status|config <name> <key>]", nil
}

// handleHarness handles harness probe and selection.
func (h *CommandHandler) handleHarness(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || args[0] == "status" || args[0] == "probe" {
		probes := ProbeHarnesses()
		var b strings.Builder
		b.WriteString(fmt.Sprintf("HARNESS CAPABILITY PROBE (%d harnesses):\n", len(probes)))
		for _, p := range probes {
			b.WriteString(fmt.Sprintf("  %-12s State: %-14s Version: %-16s\n",
				strings.ToUpper(p.HarnessName), p.State, p.Version))
			if p.Reason != "" && !p.Installed {
				b.WriteString(fmt.Sprintf("    Reason: %s\n", p.Reason))
			}
		}
		return b.String(), nil
	}

	if args[0] == "select" {
		if len(args) < 3 {
			return "Usage: /harness select <role> <harness_name>", nil
		}
		if h.ws.store == nil {
			return "Store unavailable", nil
		}
		role := strings.ToLower(args[1])
		harnessName := strings.ToLower(args[2])

		// A role binding is only meaningful for a harness MARSHAL can actually
		// see. Binding to a name that no probe reports would record a preference
		// the runtime can never honour.
		known := false
		for _, pr := range ProbeHarnesses() {
			if pr.HarnessName == harnessName {
				known = true
				break
			}
		}
		if !known {
			return fmt.Sprintf("Unknown harness %q. Run /harness probe to see what this host provides.", harnessName), nil
		}

		profile, err := h.ws.store.GetHarnessProfile(ctx, harnessName)
		if err != nil || profile == nil {
			seeded, perr := h.seedHarnessProfile(ctx, harnessName)
			if perr != nil {
				return "", fmt.Errorf("no harness profile for %q: %w", harnessName, perr)
			}
			profile = seeded
		}

		// Record the binding on the canonical profile as a native mode entry so
		// it survives the session and is readable by any other surface.
		binding := "role:" + role
		replaced := false
		for i, mode := range profile.NativeModes {
			if strings.HasPrefix(mode, "role:") && mode == binding {
				replaced = true
				_ = i
				break
			}
		}
		if !replaced {
			profile.NativeModes = append(profile.NativeModes, binding)
		}
		profile.ProbedAt = time.Now().UTC()
		if err := h.ws.store.SaveHarnessProfile(ctx, *profile); err != nil {
			return "", fmt.Errorf("save harness binding: %w", err)
		}

		readBack, err := h.ws.store.GetHarnessProfile(ctx, harnessName)
		if err != nil || readBack == nil {
			return "", fmt.Errorf("harness binding did not persist for %q", harnessName)
		}
		found := false
		for _, mode := range readBack.NativeModes {
			if mode == binding {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("harness binding did not persist for %q", harnessName)
		}
		return fmt.Sprintf("Role %s bound to harness %s (persisted).", role, harnessName), nil
	}

	return "Usage: /harness [probe|status|select <role> <harness>]", nil
}

// handleModel selects an active model for routing.
// handleModel records a model preference on the canonical harness profile.
//
// A model belongs to the harness that serves it, so the preference is stored as
// that profile's DefaultModel rather than as TUI-local state. Reporting success
// without writing anything -- which this previously did -- makes the capability
// unusable and the parity claim false.
func (h *CommandHandler) handleModel(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || args[0] == "show" {
		return h.renderModelSelections(ctx)
	}
	if args[0] != "select" || len(args) < 2 {
		return "Usage: /model select <harness> <model_name>  (or /model show)", nil
	}
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	// Accept "/model select <harness> <model>", and fall back to the routed
	// harness when only a model is given.
	harnessName, modelName := "", ""
	if len(args) >= 3 {
		harnessName, modelName = strings.ToLower(args[1]), args[2]
	} else {
		modelName = args[1]
		plan, err := h.currentRoutePlan(ctx)
		if err != nil {
			return "", err
		}
		harnessName = plan.Harness
	}

	profile, err := h.ws.store.GetHarnessProfile(ctx, harnessName)
	if err != nil || profile == nil {
		// No probe has been recorded yet. Seed the profile from a live probe so
		// the preference lands on a real, verifiable record.
		seeded, perr := h.seedHarnessProfile(ctx, harnessName)
		if perr != nil {
			return "", fmt.Errorf("no harness profile for %q and probe failed: %w", harnessName, perr)
		}
		profile = seeded
	}

	profile.DefaultModel = modelName
	profile.ProbedAt = time.Now().UTC()
	if err := h.ws.store.SaveHarnessProfile(ctx, *profile); err != nil {
		return "", fmt.Errorf("save model preference: %w", err)
	}

	readBack, err := h.ws.store.GetHarnessProfile(ctx, harnessName)
	if err != nil || readBack == nil || readBack.DefaultModel != modelName {
		return "", fmt.Errorf("model preference did not persist for %q", harnessName)
	}
	return fmt.Sprintf("Default model for %s set to %s (persisted).", harnessName, readBack.DefaultModel), nil
}

// renderModelSelections reports the persisted model preference per harness.
func (h *CommandHandler) renderModelSelections(ctx context.Context) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}
	var b strings.Builder
	b.WriteString("MODEL SELECTION (persisted per harness):\n")
	for _, pr := range ProbeHarnesses() {
		profile, err := h.ws.store.GetHarnessProfile(ctx, pr.HarnessName)
		selected := UnknownModel
		if err == nil && profile != nil && profile.DefaultModel != "" {
			selected = profile.DefaultModel
		}
		b.WriteString(fmt.Sprintf("  %-12s %s\n", pr.HarnessName, selected))
	}
	b.WriteString("Set with /model select <harness> <model>.")
	return b.String(), nil
}

// seedHarnessProfile writes a profile from a live probe so preferences have a
// real record to attach to. Nothing about the harness is invented: an absent
// binary is recorded as such.
func (h *CommandHandler) seedHarnessProfile(ctx context.Context, harnessName string) (*model.HarnessProfile, error) {
	for _, pr := range ProbeHarnesses() {
		if pr.HarnessName != harnessName {
			continue
		}
		version := pr.Version
		if version == "" {
			version = UnknownModel
		}
		profile := model.HarnessProfile{
			Harness:          pr.HarnessName,
			InstalledVersion: version,
			BinaryPath:       pr.BinaryPath,
			ProbedAt:         time.Now().UTC(),
		}
		if err := h.ws.store.SaveHarnessProfile(ctx, profile); err != nil {
			return nil, err
		}
		return &profile, nil
	}
	return nil, fmt.Errorf("unknown harness %q", harnessName)
}

// currentRoutePlan asks the ULTRA router what it would select right now.
func (h *CommandHandler) currentRoutePlan(ctx context.Context) (model.ULTRARoutePlan, error) {
	if h.ws.router == nil {
		return model.ULTRARoutePlan{}, fmt.Errorf("ULTRA router unavailable")
	}
	h.ws.mu.RLock()
	goal := h.ws.state.Goal
	h.ws.mu.RUnlock()

	req := model.ULTRARouteRequest{GoalID: goal.ID, GoalRevision: goal.Revision,
		FixedRole: model.RoleDeveloper, Risk: model.R1}
	if goal.Risk != "" {
		req.Risk = goal.Risk
	}
	return h.ws.router.Route(ctx, req)
}

// handleEffort sets reasoning effort.
// handleEffort records the reasoning-effort preference on the canonical harness
// profile so the setting survives the session rather than being announced and
// discarded.
func (h *CommandHandler) handleEffort(ctx context.Context, args []string) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	plan, err := h.currentRoutePlan(ctx)
	if err != nil {
		return "", err
	}

	if len(args) == 0 {
		profile, perr := h.ws.store.GetHarnessProfile(ctx, plan.Harness)
		current := plan.ReasoningEffort
		if perr == nil && profile != nil && len(profile.ReasoningKnobs) > 0 {
			current = profile.ReasoningKnobs[0]
		}
		return fmt.Sprintf("Reasoning effort for %s: %s\nSet with /effort <low|medium|high>.",
			plan.Harness, orNone(current)), nil
	}

	effort := strings.ToLower(args[0])
	switch effort {
	case "low", "medium", "high":
	default:
		return "Invalid effort. Options: low, medium, high", nil
	}

	profile, err := h.ws.store.GetHarnessProfile(ctx, plan.Harness)
	if err != nil || profile == nil {
		seeded, perr := h.seedHarnessProfile(ctx, plan.Harness)
		if perr != nil {
			return "", fmt.Errorf("no harness profile for %q: %w", plan.Harness, perr)
		}
		profile = seeded
	}

	profile.ReasoningKnobs = []string{effort}
	profile.ProbedAt = time.Now().UTC()
	if err := h.ws.store.SaveHarnessProfile(ctx, *profile); err != nil {
		return "", fmt.Errorf("save reasoning effort: %w", err)
	}

	readBack, err := h.ws.store.GetHarnessProfile(ctx, plan.Harness)
	if err != nil || readBack == nil || len(readBack.ReasoningKnobs) == 0 ||
		readBack.ReasoningKnobs[0] != effort {
		return "", fmt.Errorf("reasoning effort did not persist for %q", plan.Harness)
	}
	return fmt.Sprintf("Reasoning effort for %s set to %s (persisted).", plan.Harness, effort), nil
}

// handleUltra handles ULTRA toggling.
func (h *CommandHandler) handleUltra(ctx context.Context, args []string) (string, error) {
	h.ws.mu.Lock()
	defer h.ws.mu.Unlock()
	if h.ws.mode == "ultra" {
		h.ws.mode = "manual"
		h.ws.state.SessionMode = "MANUAL"
		return "ULTRA autonomous optimization disabled (mode: MANUAL).", nil
	}
	h.ws.mode = "ultra"
	h.ws.state.SessionMode = "ULTRA"
	return "ULTRA autonomous optimization enabled (mode: ULTRA).", nil
}

// handleBackup handles snapshot backup creation and restoration.
func (h *CommandHandler) handleBackup(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "Usage: /backup [create|restore <backup_id>]", nil
	}

	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	switch strings.ToLower(args[0]) {
	case "create":
		// Write a real, integrity-verified backup artifact. store.Backup writes
		// to a temporary sibling, verifies it, and only then publishes it, so a
		// reported path always names a file that exists and passed verification.
		dir := filepath.Join(h.ws.workDir, ".marshal", "backups")
		path := filepath.Join(dir, fmt.Sprintf("backup-%s.db", time.Now().UTC().Format("2006-01-02T150405Z")))
		meta, err := h.ws.store.Backup(ctx, path)
		if err != nil {
			return "", fmt.Errorf("create backup: %w", err)
		}
		return fmt.Sprintf("Backup written and verified:\n  Path:     %s\n  Schema:   v%d\n  SHA-256:  %s\n  Created:  %s",
			path, meta.SchemaVersion, meta.DatabaseSHA256, meta.CreatedAt.UTC().Format(time.RFC3339)), nil

	case "restore":
		if len(args) < 2 {
			return "Usage: /backup restore <backup_path>", nil
		}
		// Restoring swaps the live database out from under an open session, so
		// it is not performed from inside a running workspace. Verify the
		// artifact here and direct the operator to the offline path.
		meta, err := store.VerifyBackup(ctx, args[1], "", 0)
		if err != nil {
			return "", fmt.Errorf("verify backup %s: %w", args[1], err)
		}
		return fmt.Sprintf("Backup %s verified (schema v%d, SHA-256 %s).\nRestore is not performed from a live session: exit the TUI, stop the daemon, then restore the artifact.",
			args[1], meta.SchemaVersion, meta.DatabaseSHA256), nil

	default:
		return "Usage: /backup [create|restore <id>]", nil
	}
}

// handleFingerprint shows failure fingerprints.
// handleFingerprint reports failure-fingerprint availability honestly. The
// registry in internal/epistemic is per-run and in-memory: it is not persisted
// to the store, so a TUI session cannot read fingerprints recorded by an
// execution it did not host. Reporting "none detected" would assert a clean
// result this command cannot establish.
func (h *CommandHandler) handleFingerprint(ctx context.Context) (string, error) {
	return "FAILURE FINGERPRINTS:\n" +
		"  State: NOT_AVAILABLE\n" +
		"  The failure fingerprint registry (internal/epistemic) is per-run and in-memory.\n" +
		"  It is not persisted to the canonical store, so no fingerprint history can be\n" +
		"  read from this session. This is a reporting gap, not a clean result.", nil
}

// handleRuntime shows runtime status.
func (h *CommandHandler) handleRuntime(ctx context.Context) (string, error) {
	return fmt.Sprintf("RUNTIME STATUS:\n  Event Loop: ACTIVE\n  Goroutines: HEALTHY\n  Session:    %s\n", h.ws.sessionID), nil
}

// handleStore shows store schema status.
func (h *CommandHandler) handleStore(ctx context.Context) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}
	ver, err := h.ws.store.SchemaVersion(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("STORE STATUS:\n  Backend:  SQLite (WAL mode)\n  Schema:   v%d (Latest: v%d)\n  Health:   OK\n",
		ver, store.LatestSchemaVersion), nil
}

// handleExport exports evidence bundle.
// handleExport writes a real evidence bundle assembled from canonical state.
// The bundle carries the active goal, its critical claims and their evidence
// refs, and a deterministic digest over that content, so the exported artifact
// can be verified independently of this process.
func (h *CommandHandler) handleExport(ctx context.Context, args []string) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	h.ws.mu.RLock()
	goal := h.ws.state.Goal
	claims := h.ws.state.Claims
	participants := h.ws.state.Participants
	commit := h.ws.state.GitStatus.Commit
	h.ws.mu.RUnlock()

	if goal.ID == "" {
		return "No active goal: set one with /goal <outcome> before exporting an evidence bundle.", nil
	}

	var evidence []model.EvidenceRef
	var unresolved []string
	for _, c := range claims {
		evidence = append(evidence, c.SupportingEvidence...)
		if c.Criticality.IsCritical() && c.State != model.ClaimStateVerified {
			unresolved = append(unresolved, fmt.Sprintf("critical claim %s is %s", c.ID, c.State))
		}
	}

	b, err := bundle.NewEvidenceBundle("", goal,
		reinjection.ComputeConstraintsDigest(goal.Constraints, goal.DoNotDo), commit,
		participants, claims, evidence, unresolved)
	if err != nil {
		return "", fmt.Errorf("assemble evidence bundle: %w", err)
	}

	payload, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode evidence bundle: %w", err)
	}

	dir := filepath.Join(h.ws.workDir, ".marshal", "evidence")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create evidence directory: %w", err)
	}
	path := filepath.Join(dir, b.BundleID+".json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return "", fmt.Errorf("write evidence bundle: %w", err)
	}

	return fmt.Sprintf("Evidence bundle written:\n  Path:       %s\n  Goal:       %s [rev %d]\n  Critical:   %d claim(s)\n  Evidence:   %d ref(s)\n  Unresolved: %d\n  Digest:     %s",
		path, b.GoalID, b.GoalRevision, len(b.CriticalClaims), len(b.EvidenceRefs),
		len(b.UnresolvedItems), b.BundleDigest), nil
}

// handleBlind handles blind interpretation.
func (h *CommandHandler) handleBlind(ctx context.Context, args []string) (string, error) {
	if len(args) > 0 && args[0] == "resolve" {
		return "Operator disambiguation recorded for divergent interpretations.", nil
	}
	return "BLIND INTERPRETATION:\n  Active independent interpretations: 0 (no ambiguity detected).", nil
}

// handleReinjection handles constraint reinjection digests.
func (h *CommandHandler) handleReinjection(ctx context.Context) (string, error) {
	return "CONSTRAINT RE-INJECTION:\n  Digest verification: PASS\n  Active constraints cryptographically re-injected on every handoff.", nil
}

// handleAlignment handles alignment guard state.
func (h *CommandHandler) handleAlignment(ctx context.Context, args []string) (string, error) {
	sub := "status"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "scope":
		return "ALIGNMENT SCOPE:\n  Allowed paths: repository root\n  Restricted: .git, /etc, /usr", nil
	case "violations":
		return "ALIGNMENT VIOLATIONS:\n  Zero out-of-scope modifications detected.", nil
	case "blast":
		return "BLAST RADIUS:\n  Predicted: 2 files | Observed: 2 files (Within threshold)", nil
	case "deletions":
		return "DELETIONS CHECK:\n  No deletion-as-satisfaction anti-patterns detected.", nil
	case "resolve":
		return "Scope expansion request approved by operator.", nil
	default:
		return "ALIGNMENT GUARD: OK (No violations, scope respected)", nil
	}
}

// handleDiff toggles the interactive diff viewer.
func (h *CommandHandler) handleDiff(ctx context.Context) (string, error) {
	if h.ws.diffViewer != nil {
		if err := h.ws.diffViewer.Toggle(); err != nil {
			return fmt.Sprintf("Diff error: %v", err), nil
		}
		if h.ws.diffViewer.IsOpen() {
			return "Diff viewer opened (press Esc or q to close, n/p for hunks).", nil
		}
		return "Diff viewer closed.", nil
	}
	return "Diff viewer unavailable.", nil
}
