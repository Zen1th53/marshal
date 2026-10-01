package tui

import (
	"context"
	"encoding/json"
	"errors"
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

	if report.Verdict != doctor.Pass {
		b.WriteString("Run marshal doctor from the project root for detailed checks; use marshal init if the project is not initialized.\n")
	}
	return b.String(), nil
}

// handleTasks handles /tasks and /task subcommands.
func (h *CommandHandler) handleTasks(ctx context.Context, args []string, line string) (string, error) {
	if len(args) == 0 || strings.EqualFold(args[0], "list") {
		if h.ws.store == nil {
			return "Store unavailable to list tasks. Open the TUI in an initialized MARSHAL project (marshal init).", nil
		}
		tasks, err := h.ws.store.ListTasks(ctx)
		if err != nil {
			return "", fmt.Errorf("list tasks: %w", err)
		}
		if len(tasks) == 0 {
			return "No tasks in store. Task creation is unavailable in TUI; authenticated runtime authorization is required.", nil
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
		return "Store unavailable. Open the TUI in an initialized MARSHAL project (marshal init).", nil
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
			"  Shared memory stores verified records and agent-authority candidates; candidates are not verified facts.\n" +
			"  Use /memory list, /memory search <query>, or /memory provenance <id> to inspect records.\n" +
			"  Use /memory peers to show or change cross-agent visibility.\n" +
			"  Use /memory inject to govern what a starting native session is told\n" +
			"  about the work other coding agents already did in this project.", nil
	}

	switch strings.ToLower(args[0]) {
	case "inject":
		return h.handleMemoryInject(ctx, args[1:])
	case "peers":
		return h.handleMemoryPeers(args[1:])
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "list":
		if len(args) != 1 {
			return "Usage: /memory list", nil
		}
	case "search":
		if len(args) < 2 {
			return "Usage: /memory search <query>", nil
		}
	case "provenance":
		if len(args) != 2 {
			return "Usage: /memory provenance <memory_id>", nil
		}
	default:
		return "Usage: /memory [list|search <query>|provenance <id>|inject [channel]|peers [agent authors…]]", nil
	}
	if h.ws.store == nil {
		return "Store unavailable; reopen the TUI with a project store to read durable memory.", nil
	}

	h.ws.mu.RLock()
	projectID := h.ws.state.ProjectID
	h.ws.mu.RUnlock()

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
			query = strings.ToLower(strings.Join(args[1:], " "))
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
		r, err := h.ws.store.GetMemoryV2(ctx, projectID, args[1])
		if errors.Is(err, model.ErrNotFound) {
			return fmt.Sprintf("No memory record %s in this project.", args[1]), nil
		}
		if err != nil {
			return "", fmt.Errorf("read memory provenance: %w", err)
		}
		return fmt.Sprintf("MEMORY %s\n  Kind:       %s\n  Lifecycle:  %s\n  Authority:  %s\n  Confidence: %s\n  Digest:     %s\n  Title:      %s",
			r.ID, r.Kind, r.Lifecycle, r.Authority, r.Confidence,
			r.ContentDigest, RedactContent(r.Title, nil)), nil

	default:
		return "Usage: /memory [list|search <query>|provenance <id>|inject [channel]|peers [agent authors…]]", nil
	}
}

// handleMemoryInject governs how a starting native session is told what the
// other providers already did in this project.
func (h *CommandHandler) handleMemoryInject(ctx context.Context, args []string) (string, error) {
	h.ws.mu.RLock()
	root := h.ws.workDir
	runtime := h.ws.runtime
	h.ws.mu.RUnlock()
	if runtime != nil {
		root = runtime.ProjectRoot()
	}

	if len(args) == 0 {
		configured := loadInjectChannel(root)
		var b strings.Builder
		fmt.Fprintf(&b, "CROSS-AGENT MEMORY INJECTION:\n")
		fmt.Fprintf(&b, "  Configured:  %s\n", configured)
		for _, provider := range []string{"claude", "codex", "opencode", "antigravity"} {
			channel, note := resolveInjectChannel(provider, configured)
			fmt.Fprintf(&b, "  %-17s %s\n", providerDisplayName(provider)+" uses:", channel)
			if note != "" {
				fmt.Fprintf(&b, "                    %s\n", note)
			}
		}
		b.WriteString("\nChannels:\n")
		b.WriteString("  auto           Claude: system prompt, Codex: developer instructions, OpenCode/Agy: MARSHAL's briefing directory (default)\n")
		b.WriteString("  system-prompt  Append to the agent's system prompt; costs no turn (Claude only)\n")
		b.WriteString("  project-doc    Write a marked block into AGENTS.md / CLAUDE.md; costs no turn\n")
		b.WriteString("  prompt         Pass as the opening prompt; always works, consumes one turn\n")
		b.WriteString("  off            Start native sessions with no briefing\n")
		b.WriteString("\n  /memory inject <channel>     Change the channel\n")
		b.WriteString("  /memory inject preview [claude|codex|opencode|agy|antigravity]   Show the briefing a provider would receive\n")
		b.WriteString("  /memory inject clear         Remove MARSHAL blocks from the project documents\n")
		return b.String(), nil
	}

	if (strings.EqualFold(args[0], "preview") && len(args) > 2) ||
		(!strings.EqualFold(args[0], "preview") && len(args) != 1) {
		return "Usage: /memory inject <channel>|preview [claude|codex|opencode|agy|antigravity]|clear", nil
	}

	switch strings.ToLower(args[0]) {
	case "clear":
		cleared, err := clearProjectDocBlock(root)
		if err != nil {
			return "", fmt.Errorf("clear project document blocks: %w", err)
		}
		if cleared == 0 {
			return "No MARSHAL memory block found in AGENTS.md or CLAUDE.md.", nil
		}
		return fmt.Sprintf("Removed the MARSHAL memory block from %d project document(s).", cleared), nil

	case "preview":
		provider := "claude"
		if len(args) > 1 {
			provider = strings.ToLower(args[1])
		}
		if provider == "agy" {
			provider = "antigravity"
		}
		if provider != "claude" && provider != "codex" && provider != "opencode" && provider != "antigravity" {
			return "Usage: /memory inject preview [claude|codex|opencode|agy|antigravity]", nil
		}
		briefing, err := h.ws.crossAgentBriefing(ctx, provider)
		if err != nil {
			return "", fmt.Errorf("compile cross-agent briefing: %w", err)
		}
		if strings.TrimSpace(briefing) == "" {
			return fmt.Sprintf("No other provider has recorded work in this project, so %s would receive no briefing.", provider), nil
		}
		channel, note := resolveInjectChannel(provider, loadInjectChannel(root))
		header := fmt.Sprintf("BRIEFING FOR %s via %s (%d bytes):\n", strings.ToUpper(provider), channel, len(briefing))
		if note != "" {
			header += note + "\n"
		}
		return header + "\n" + briefing, nil
	}

	channel, err := parseInjectChannel(args[0])
	if err != nil {
		return err.Error(), nil
	}
	if err := saveInjectChannel(root, channel); err != nil {
		return "", fmt.Errorf("save injection channel: %w", err)
	}
	var providers, notes []string
	for _, provider := range []string{"claude", "codex", "opencode", "antigravity"} {
		resolved, note := resolveInjectChannel(provider, channel)
		providers = append(providers, fmt.Sprintf("%s: %s", providerDisplayName(provider), resolved))
		if note != "" {
			notes = append(notes, note)
		}
	}
	confirmation := fmt.Sprintf("Cross-agent injection channel set to %s (%s).", channel, strings.Join(providers, ", "))
	if len(notes) > 0 {
		confirmation += "\n" + strings.Join(notes, "\n")
	}
	return confirmation, nil
}

// handleMemoryPeers shows or sets the shared channel: who joins it, and who
// each agent sees in it.
//
// Visibility is per reader because that is the question an operator actually
// has. Models differ in what they can use — a strong one does better seeing
// everything the others did, a weaker one does worse — so the two directions
// between any pair are set separately and are allowed to disagree.
func (h *CommandHandler) handleMemoryPeers(args []string) (string, error) {
	h.ws.mu.RLock()
	root := h.ws.workDir
	runtime := h.ws.runtime
	h.ws.mu.RUnlock()
	if runtime != nil {
		root = runtime.ProjectRoot()
	}

	cfg, problems := loadChannelConfig(root)
	if len(args) == 0 {
		return h.renderChannel(root, cfg, problems, ""), nil
	}

	name := canonicalProvider(args[0])
	if len(args) == 1 {
		return "Usage: /memory peers <agent> <agents|all|none>", nil
	}
	list := strings.Join(args[1:], " ")
	fields := splitList(list)
	if len(fields) == 0 || (mentionsNone(list) && len(fields) != 1) {
		return "Usage: /memory peers <agent|participants> <agents|all|none> (none must be used alone)", nil
	}

	if name == "participants" {
		if mentionsNone(list) {
			return "Usage: /memory peers participants <agents|all> (an empty participant list means all agents)", nil
		}
		joined, bad := parseProviderList("", list)
		if len(bad) > 0 {
			return fmt.Sprintf("%s: not an agent MARSHAL runs. Agents: %s.",
				strings.Join(bad, ", "), strings.Join(knownProviders, ", ")), nil
		}
		cfg.participants = joined
		if err := saveChannelConfig(root, cfg); err != nil {
			return "", fmt.Errorf("save channel configuration: %w", err)
		}
		return h.renderChannel(root, cfg, nil, ""), nil
	}

	if !isKnownProvider(name) {
		return fmt.Sprintf("%q is not an agent MARSHAL runs. Agents: %s.",
			args[0], strings.Join(knownProviders, ", ")), nil
	}
	authors, bad := parseProviderList(name, list)
	if len(bad) > 0 {
		return fmt.Sprintf("%s: not an agent MARSHAL runs. Agents: %s.",
			strings.Join(bad, ", "), strings.Join(knownProviders, ", ")), nil
	}
	cfg.sees[name] = authors
	if err := saveChannelConfig(root, cfg); err != nil {
		return "", fmt.Errorf("save channel configuration: %w", err)
	}
	return h.renderChannel(root, cfg, nil, name), nil
}

// renderChannel draws the arrangement.
//
// Colour carries state and nothing else. Every agent name is the same colour,
// because the name is not the information — whether it contributes, and who it
// is given, is. Colouring each agent differently would make the report look
// like it means more than it says.
func (h *CommandHandler) renderChannel(root string, cfg channelConfig, problems []string, changed string) string {
	th := h.ws.theme
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}
	const nameWidth = 13
	// Padding is computed on the visible text: %-14s counts escape bytes and
	// would leave every coloured column ragged.
	pad := func(text string) string {
		if gap := nameWidth - len(StripANSI(text)); gap > 0 {
			return text + strings.Repeat(" ", gap)
		}
		return text
	}
	name := func(n string) string { return pad(th.Colorize(th.Active, n)) }
	// The row that just changed is marked, because a command that alters state
	// and answers with one line leaves the operator to trust that it worked.
	// The whole arrangement is reprinted so the change is read in context, and
	// the marker says which part of it moved.
	edge := func(agent string) string {
		if agent == changed {
			return th.Colorize(th.Warning, th.GlyphArrowR)
		}
		return th.BoxVert
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s SHARED CHANNEL\n", th.Colorize(th.Marshal, "╭─"))
	fmt.Fprintf(&b, "%s%s\n", th.BoxTRight, strings.Repeat(th.BoxHoriz, 58))

	for _, agent := range knownProviders {
		mark, note := th.Colorize(th.Success, th.GlyphDotFull), "contributes as it works"
		switch {
		case !cfg.joins(agent):
			mark, note = th.Colorize(th.Muted, th.GlyphDotEmpty), "not in the channel"
		case !capturesLive(agent):
			mark, note = th.Colorize(th.Warning, th.GlyphDotHalf), "contributes when it exits"
		}
		fmt.Fprintf(&b, "%s %s %s %s\n", edge(agent), mark, name(agent), th.Colorize(th.Muted, note))
	}

	fmt.Fprintf(&b, "%s%s\n", th.BoxTRight, strings.Repeat(th.BoxHoriz, 58))
	for _, reader := range knownProviders {
		// Only authors actually in the channel are listed. A reader configured
		// for an agent that never joined would otherwise be shown a name that
		// can never appear in its view.
		var authors []string
		for _, author := range cfg.visibleTo(reader) {
			if cfg.joins(author) {
				authors = append(authors, author)
			}
		}
		list := th.Colorize(th.Muted, "nothing")
		if len(authors) > 0 {
			list = th.Colorize(th.Active, strings.Join(authors, ", "))
		}
		fmt.Fprintf(&b, "%s %s %s %s\n", edge(reader), name(reader),
			th.Colorize(th.Muted, "sees"), list)
	}
	fmt.Fprintf(&b, "%s%s\n", th.BoxBottomLeft, strings.Repeat(th.BoxHoriz, 58))

	b.WriteString(th.Colorize(th.Muted,
		"An agent is never shown its own work: it already knows what it did.\n"))
	for _, problem := range problems {
		fmt.Fprintf(&b, "%s %s: %s\n",
			th.Colorize(th.Warning, th.GlyphCross), livePeerPath(root), problem)
	}
	b.WriteString(th.Colorize(th.Muted,
		"\n/memory peers <agent> <agents|all|none>\n/memory peers participants <agents|all>"))
	return b.String()
}

// handleProvider handles provider configuration and status inspection.
func (h *CommandHandler) handleProvider(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || strings.EqualFold(args[0], "status") {
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

	if strings.EqualFold(args[0], "config") {
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

	return "Usage: /provider [status|config <name>]", nil
}

// handleHarness handles harness probe and selection.
func (h *CommandHandler) handleHarness(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || strings.EqualFold(args[0], "status") || strings.EqualFold(args[0], "probe") {
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

	if strings.EqualFold(args[0], "select") {
		return "Harness selection was NOT applied: Runtime has no authenticated canonical execution-profile service.", nil
	}

	return "Usage: /harness [probe|status|select <role> <harness>]", nil
}

// handleModel exposes saved preferences read-only. Mutations fail closed until
// Runtime owns a canonical authenticated execution-profile service.
func (h *CommandHandler) handleModel(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || strings.EqualFold(args[0], "show") {
		return h.renderModelSelections(ctx)
	}
	if !strings.EqualFold(args[0], "select") || len(args) < 2 {
		return "Usage: /model select <harness> <model_name>  (or /model show)", nil
	}
	return "Model selection was NOT applied: Runtime has no authenticated canonical execution-profile service.", nil
}

// renderModelSelections reports the persisted model preference per harness.
func (h *CommandHandler) renderModelSelections(ctx context.Context) (string, error) {
	if h.ws.store == nil {
		return configStoreUnavailable, nil
	}
	var b strings.Builder
	b.WriteString("SAVED MODEL PREFERENCES (NOT APPLIED TO RUNTIME):\n")
	for _, pr := range ProbeHarnesses() {
		profile, err := h.ws.store.GetHarnessProfile(ctx, pr.HarnessName)
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return "", fmt.Errorf("read model preferences: %w; reopen the TUI and check /store", err)
		}
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

// handleEffort distinguishes probed capabilities and advisory defaults from
// an execution-bound selection, and refuses mutations Runtime cannot apply.
func (h *CommandHandler) handleEffort(ctx context.Context, args []string) (string, error) {
	if h.ws.store == nil {
		return configStoreUnavailable, nil
	}
	if len(args) > 0 {
		return "Reasoning effort was NOT applied: Runtime has no authenticated canonical execution-profile service.", nil
	}
	plan, err := h.currentRoutePlan(ctx)
	if err != nil {
		return "", fmt.Errorf("read reasoning preference: %w; the advisory router is unavailable, reopen the TUI", err)
	}
	profile, err := h.ws.store.GetHarnessProfile(ctx, plan.Harness)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return "", fmt.Errorf("read reasoning preference: %w; reopen the TUI and check /store", err)
	}
	knobs := "UNKNOWN (no probed capability metadata)"
	if profile != nil && len(profile.ReasoningKnobs) > 0 {
		knobs = strings.Join(profile.ReasoningKnobs, ", ")
	}
	return fmt.Sprintf("REASONING EFFORT (NOT APPLIED TO RUNTIME):\n  Harness (advisory route): %s\n  Selected effort: UNKNOWN (no canonical preference read-back)\n  Advisory route default: %s\n  Probed reasoning knobs: %s", plan.Harness, orNone(plan.ReasoningEffort), knobs), nil
}

// handleUltra reports ULTRA status and switches ULTRA Execution.
//
// No argument grants anything: entitlement is not a thing the client decides.
// /ultra start and /ultra stop change only the person's preference for this
// session, which does nothing without a verified entitlement. Every session
// starts with execution off.
func (h *CommandHandler) handleUltra(ctx context.Context, args []string) (string, error) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	confirming := len(args) == 2 && sub == "stop" && strings.EqualFold(args[1], "confirm")
	asked := h.ws.takeUltraStop()
	switch {
	case len(args) == 1 && sub == "request":
		// `/ultra request` asks an operator to grant this installation ULTRA.
		// It queues a question for a person, and the answer arrives from the
		// server or not at all.
		return h.requestUltra(ctx)
	case len(args) == 1 && sub == "start":
		return h.startUltra(), nil
	case len(args) == 1 && sub == "stop":
		return h.askStopUltra(), nil
	case confirming:
		if !asked {
			return h.askStopUltra(), nil
		}
		h.ws.setUltraExecution(false)
		return "ULTRA Execution is off. The session behaves like Standard; /ultra start turns it on again.", nil
	case len(args) == 0 || (len(args) == 1 && sub == "status"):
	default:
		return "Usage: /ultra [status|start|stop|request]", nil
	}
	gate, executionEnabled := h.ws.ultraGate()

	if !gate.Entitled() {
		// Distinguish "you have not been granted this" from "we could not ask".
		// Both leave the session in Standard, but only the first is answered by
		// requesting one, and telling a user to request when the server is
		// rate-limiting them sends them round a loop.
		if err := h.ws.ultraError(); err != nil {
			return "ULTRA status: INACTIVE — activation failed: " + err.Error() + "\n" +
				"  The session is running as Standard. Try again shortly.", nil
		}
		return "ULTRA status: INACTIVE — no cryptographically verified entitlement is active.\n" +
			"  Use /ultra request to ask an operator for one.", nil
	}

	expiry, _ := gate.ExpiresAt()
	remaining := time.Until(expiry).Round(time.Second)
	grantExpiryLine := "Grant expiry: unavailable from Community Cloud."
	if grantExpiry, ok := gate.EntitlementExpiresAt(); ok {
		grantExpiryLine = "Grant expires: " + grantExpiry.Local().Format("2006-01-02 15:04 -07:00") + "."
	}
	if !executionEnabled {
		return fmt.Sprintf(
			"ULTRA status: ENTITLED, EXECUTION OFF — the session behaves like Standard.\n  %s\n  Lease expires in %s.\n  Use /ultra start to enable execution.",
			grantExpiryLine, remaining), nil
	}
	if !gate.Capability(cloud.CapabilityDelegation) {
		return fmt.Sprintf("ULTRA status: ACTIVE — delegation is unavailable; confirmations are still required.\n  %s\n  Lease expires in %s.", grantExpiryLine, remaining), nil
	}
	return fmt.Sprintf("ULTRA status: ACTIVE — delegation is enabled.\n  %s\n  Lease expires in %s.", grantExpiryLine, remaining), nil
}

// startUltra turns ULTRA Execution on for this session. Without a verified
// entitlement it changes nothing and says how to get one.
func (h *CommandHandler) startUltra() string {
	gate, _ := h.ws.ultraGate()
	if !gate.Entitled() {
		if err := h.ws.ultraError(); err != nil {
			return "ULTRA was not started: activation failed: " + err.Error() + "\n" +
				"  The session is running as Standard. Try again shortly."
		}
		return "ULTRA was not started: no cryptographically verified entitlement is active.\n" +
			"  Use /ultra request to ask an operator for one."
	}
	h.ws.setUltraExecution(true)
	if !gate.Capability(cloud.CapabilityDelegation) {
		return "ULTRA Execution is on. Delegation is unavailable, so confirmations are still required."
	}
	return "ULTRA Execution is on — delegation is enabled. /ultra stop turns it off."
}

// askStopUltra asks the person to confirm before execution is switched off:
// stopping it mid-session changes how every later task is confirmed.
func (h *CommandHandler) askStopUltra() string {
	if _, executionEnabled := h.ws.ultraGate(); !executionEnabled {
		return "ULTRA Execution is already off."
	}
	h.ws.askUltraStop(true)
	return "Do you really want to turn ULTRA Execution off?\n" +
		"  Type /ultra stop confirm to turn it off; any other command keeps it on."
}

// requestUltra asks an operator to grant this installation ULTRA.
//
// The reply is a status rather than a capability. "pending" means somebody has
// been asked; nothing changes here until they answer, and the next session will
// pick up the entitlement if they approved it.
func (h *CommandHandler) requestUltra(ctx context.Context) (string, error) {
	client, state, sessionID := h.ws.ultraRequester()
	if client == nil {
		return "The Community Cloud is not configured, so there is nobody to ask.\n" +
			"  Set " + cloud.EnvEndpoint + " and start MARSHAL again.", nil
	}

	status, err := client.RequestEntitlement(ctx, state, sessionID)
	if err != nil {
		// A refusal and an outage read differently because they call for
		// different responses: one is an answer, the other is "try later".
		if errors.Is(err, cloud.ErrUnreachable) {
			return "Could not reach the Community Cloud. Your request was not sent.", nil
		}
		return "The request was refused: " + err.Error(), nil
	}

	switch status {
	case "pending":
		return "Requested. An operator has been asked to approve ULTRA for this installation.\n" +
			"  Nothing changes until they do; check back with /ultra.", nil
	case "active":
		return "This installation is already entitled to ULTRA.\n" +
			"  Restart MARSHAL to pick it up if /ultra still says otherwise.", nil
	default:
		return "The server answered: " + status, nil
	}
}

// handleBackup handles snapshot backup creation and restoration.
func (h *CommandHandler) handleBackup(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "Usage: /backup [create|restore <backup_path>]", nil
	}

	if h.ws.store == nil {
		return configStoreUnavailable, nil
	}

	switch strings.ToLower(args[0]) {
	case "create":
		// Write a real, integrity-verified backup artifact. store.Backup writes
		// to a temporary sibling, verifies it, and only then publishes it, so a
		// reported path always names a file that exists and passed verification.
		dir := filepath.Join(h.ws.workDir, ".marshal", "backups")
		path := filepath.Join(dir, fmt.Sprintf("backup-%s.db", time.Now().UTC().Format("2006-01-02T150405.000000000Z")))
		meta, err := h.ws.store.Backup(ctx, path)
		if err != nil {
			return "", fmt.Errorf("create backup: %w; check that the project backup directory is writable, reopen the TUI and check /store", err)
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
			return "", fmt.Errorf("verify backup %s: %w; use /backup restore <backup_path> with an existing verified backup file", args[1], err)
		}
		return fmt.Sprintf("Backup %s verified (schema v%d, SHA-256 %s).\nRestore is not performed from a live session: exit the TUI, stop the daemon, then run marshal state restore <backup_path>.",
			args[1], meta.SchemaVersion, meta.DatabaseSHA256), nil

	default:
		return "Usage: /backup [create|restore <backup_path>]", nil
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
		return configStoreUnavailable, nil
	}
	ver, err := h.ws.store.SchemaVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("read schema: %w; reopen the TUI and check the project database", err)
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
		return configStoreUnavailable, nil
	}

	h.ws.mu.RLock()
	goal := h.ws.state.Goal
	claims := h.ws.state.Claims
	participants := h.ws.state.Participants
	commit := h.ws.state.GitStatus.Commit
	h.ws.mu.RUnlock()

	if goal.ID == "" {
		return "No active goal: open a session with a canonical goal before exporting. Goal creation in TUI is unavailable; authenticated runtime authorization is required.", nil
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
		return "", fmt.Errorf("create evidence directory: %w; check that .marshal/evidence is a writable directory", err)
	}
	path := filepath.Join(dir, b.BundleID+".json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return "", fmt.Errorf("write evidence bundle: %w; check that .marshal/evidence is a writable directory", err)
	}

	return fmt.Sprintf("Evidence bundle written:\n  Path:       %s\n  Goal:       %s [rev %d]\n  Critical:   %d claim(s)\n  Evidence:   %d ref(s)\n  Unresolved: %d\n  Digest:     %s",
		path, b.GoalID, b.GoalRevision, len(b.CriticalClaims), len(b.EvidenceRefs),
		len(b.UnresolvedItems), b.BundleDigest), nil
}

// handleBlind handles blind interpretation.
func (h *CommandHandler) handleBlind(ctx context.Context, args []string) (string, error) {
	if len(args) > 0 && strings.EqualFold(args[0], "resolve") {
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
	return h.handleDiffScope(ctx, "")
}

func (h *CommandHandler) handleDiffScope(ctx context.Context, scope string) (string, error) {
	if h.ws.diffViewer != nil {
		h.ws.diffViewer.scope = scope
		if err := h.ws.diffViewer.Toggle(); err != nil {
			return fmt.Sprintf("Diff error: %v. Check that the project is a Git worktree, then retry /diff.", err), nil
		}
		if h.ws.diffViewer.IsOpen() {
			return "Diff viewer opened (press Esc or q to close, n/p for hunks).", nil
		}
		return "Diff viewer closed.", nil
	}
	return "Diff viewer unavailable. Open marshal tui in an initialized Git project (marshal init), then retry /diff.", nil
}
