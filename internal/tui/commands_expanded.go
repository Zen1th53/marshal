package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/bundle"
	"github.com/Zen1th53/marshal/internal/cloud"
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
	return "Policy enforcement status: NOT VERIFIED. TUI is not connected to an authenticated runtime policy read-back service.", nil
}

// handleSandbox reports the bubblewrap sandbox status.
func (h *CommandHandler) handleSandbox(ctx context.Context) (string, error) {
	return "Sandbox status: NOT VERIFIED. Isolation is established and reported per runtime execution, not by the TUI.", nil
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
		return "Harness selection was NOT applied: Runtime has no authenticated canonical execution-profile service.", nil
	}

	return "Usage: /harness [probe|status|select <role> <harness>]", nil
}

// handleModel exposes saved preferences read-only. Mutations fail closed until
// Runtime owns a canonical authenticated execution-profile service.
func (h *CommandHandler) handleModel(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || args[0] == "show" {
		return h.renderModelSelections(ctx)
	}
	if args[0] != "select" || len(args) < 2 {
		return "Usage: /model select <harness> <model_name>  (or /model show)", nil
	}
	return "Model selection was NOT applied: Runtime has no authenticated canonical execution-profile service.", nil
}

// renderModelSelections reports the persisted model preference per harness.
func (h *CommandHandler) renderModelSelections(ctx context.Context) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable", nil
	}
	var b strings.Builder
	b.WriteString("SAVED MODEL PREFERENCES (NOT APPLIED TO RUNTIME):\n")
	for _, pr := range ProbeHarnesses() {
		profile, err := h.ws.store.GetHarnessProfile(ctx, pr.HarnessName)
		selected := UnknownModel
		if err == nil && profile != nil && profile.DefaultModel != "" {
			selected = profile.DefaultModel
		}
		b.WriteString(fmt.Sprintf("  %-12s %s\n", pr.HarnessName, selected))
	}
	b.WriteString("Runtime execution-profile integration is unavailable.")
	return b.String(), nil
}

// currentRoutePlan asks the advisory router what it would select right now.
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

// handleEffort exposes saved preferences read-only and refuses mutations that
// Runtime cannot apply.
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
		return fmt.Sprintf("Saved reasoning preference for %s: %s (NOT APPLIED TO RUNTIME).",
			plan.Harness, orNone(current)), nil
	}
	return "Reasoning effort was NOT applied: Runtime has no authenticated canonical execution-profile service.", nil
}

// handleUltra reports ULTRA entitlement status.
//
// It reports; it does not grant. There is no argument to this command that
// turns ULTRA on, because entitlement is not a thing the client decides — a
// toggle here would be exactly the local flag the design refuses to rely on.
func (h *CommandHandler) handleUltra(ctx context.Context, args []string) (string, error) {
	gate, executionEnabled := h.ws.ultraGate()
	if !gate.Entitled() {
		return "ULTRA is unavailable: no cryptographically verified entitlement is active.", nil
	}

	expiry, _ := gate.ExpiresAt()
	remaining := time.Until(expiry).Round(time.Second)
	if !gate.Capability(cloud.CapabilityDelegation) {
		return fmt.Sprintf(
			"ULTRA is active but does not grant delegation, so confirmations are still asked for.\n  Lease expires in %s.",
			remaining), nil
	}
	if !executionEnabled {
		return fmt.Sprintf(
			"ULTRA is entitled but Execution is off, so it behaves exactly like Standard.\n  Lease expires in %s.\n  Set %s=1 to enable it.",
			remaining, cloud.EnvExecution), nil
	}
	return fmt.Sprintf("ULTRA is active with delegation.\n  Lease expires in %s.", remaining), nil
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
	return fmt.Sprintf("RUNTIME STATUS:\n  Execution state: NOT VERIFIED\n  Session label:   %s\n  TUI has no authenticated runtime health channel.\n", h.ws.sessionID), nil
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
	return fmt.Sprintf("STORE STATUS:\n  Backend:  SQLite\n  Schema:   v%d (Latest: v%d)\n  Health:   NOT VERIFIED (schema read succeeded only)\n",
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
		return "Blind-interpretation resolution was NOT recorded: authenticated runtime support is unavailable.", nil
	}
	return "BLIND INTERPRETATION:\n  State: NOT VERIFIED (no canonical interpretation read-back service).", nil
}

// handleReinjection handles constraint reinjection digests.
func (h *CommandHandler) handleReinjection(ctx context.Context) (string, error) {
	return "CONSTRAINT RE-INJECTION:\n  State: NOT VERIFIED (no execution-bound digest was read back).", nil
}

// handleAlignment handles alignment guard state.
func (h *CommandHandler) handleAlignment(ctx context.Context, args []string) (string, error) {
	if len(args) > 0 && strings.ToLower(args[0]) == "resolve" {
		return "Alignment escalation was NOT resolved: authenticated runtime authorization is required.", nil
	}
	return "ALIGNMENT GUARD: NOT VERIFIED (no execution-bound alignment result was read back).", nil
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
