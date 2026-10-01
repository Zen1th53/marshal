package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/google/uuid"
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
// taskScopeFlag strips a trailing "--scope project|active" and reports the
// requested scope. Project scope is the default; it is always labelled.
func taskScopeFlag(args []string) ([]string, string, bool) {
	for i, arg := range args {
		if !strings.EqualFold(arg, "--scope") {
			continue
		}
		if i != len(args)-2 {
			return nil, "", false
		}
		scope := strings.ToLower(args[i+1])
		if scope != "project" && scope != "active" {
			return nil, "", false
		}
		return args[:i], scope, true
	}
	return args, "project", true
}

// scopedTasks lists the project's tasks, or only those of the active plan.
// The returned label says which, so a session-oriented screen never shows
// project-wide work without saying so.
func (h *CommandHandler) scopedTasks(ctx context.Context, scope string) ([]model.Task, string, error) {
	tasks, err := h.ws.store.ListTasks(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("list tasks: %w", err)
	}
	if scope == "project" {
		return tasks, "scope: project, all tasks in this project", nil
	}
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil {
		return nil, "", errNoActiveScope
	}
	rt := a.runtime
	active, err := rt.ActivePlanScope(ctx)
	if err != nil {
		return nil, "", errNoActiveScope
	}
	filtered := tasks[:0:0]
	for _, t := range tasks {
		if active.TaskIDs[t.ID] {
			filtered = append(filtered, t)
		}
	}
	return filtered, fmt.Sprintf("scope: active plan %s v%d", active.PlanID, active.PlanVersion), nil
}

var errNoActiveScope = errors.New("no active plan: the active scope is unavailable; use --scope project")

func (h *CommandHandler) handleTasks(ctx context.Context, args []string, line string) (string, error) {
	args, scope, ok := taskScopeFlag(args)
	if !ok {
		return "Usage: /tasks [list|ownership] [--scope project|active]", nil
	}
	if len(args) == 0 || strings.EqualFold(args[0], "list") {
		if h.ws.store == nil {
			return "Store unavailable to list tasks. Open the TUI in an initialized MARSHAL project (marshal init).", nil
		}
		tasks, label, err := h.scopedTasks(ctx, scope)
		if errors.Is(err, errNoActiveScope) {
			return "Tasks: " + err.Error(), nil
		}
		if err != nil {
			return "", err
		}
		if len(tasks) == 0 {
			if scope == "active" {
				return "No tasks in the active plan (" + label + ").", nil
			}
			return "No tasks in store. Use /task create <title> in an authenticated workspace.", nil
		}

		th := h.ws.theme
		if th == nil {
			th = NewTheme(ThemeDefault, true, true)
		}

		var b strings.Builder
		b.WriteString(fmt.Sprintf("TASKS (%d total; %s):\n", len(tasks), label))
		for _, t := range tasks {
			owner := "(none)"
			if t.OwnerAgentID != nil {
				owner = *t.OwnerAgentID
			}
			status := string(t.Status)
			if t.ControlState != "" {
				status = t.ControlState
			}
			badge := th.RenderBadge(status)
			b.WriteString(fmt.Sprintf("  %-8s %s %-20s [Owner: %s | Risk: %s]\n",
				t.ID, badge, t.Title, owner, t.Risk))
		}
		return b.String(), nil
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "create", "assign", "pause", "resume", "cancel", "retry":
		return h.handleTaskMutation(ctx, args)
	case "inspect":
		return h.handleInspect(ctx, "task", args[1])

	case "ownership":
		if len(args) != 1 {
			return "Usage: /tasks ownership [--scope project|active]", nil
		}
		return h.handleTaskOwnership(ctx, scope)

	default:
		return "Usage: /task [list|create|inspect|assign|pause|resume|cancel|retry|ownership]", nil
	}
}

func (h *CommandHandler) handleTaskOwnership(ctx context.Context, scope string) (string, error) {
	if h.ws.store == nil {
		return "Store unavailable. Open the TUI in an initialized MARSHAL project (marshal init).", nil
	}
	tasks, label, err := h.scopedTasks(ctx, scope)
	if errors.Is(err, errNoActiveScope) {
		return "Ownership: " + err.Error(), nil
	}
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("WORK OWNERSHIP TABLE (" + label + "):\n")
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
// handlePolicy separates configured policy from observed gate decisions, and
// never reports an aspect as enforced without execution-bound evidence of it.
func (h *CommandHandler) handlePolicy(ctx context.Context, args []string) (string, error) {
	aspect := "network, sandbox, capability, scope, write and audit"
	if len(args) > 0 {
		aspect = strings.ToLower(args[0])
	}
	verdict := fmt.Sprintf("Policy enforcement status: NOT VERIFIED for %s; no execution-bound observation of it is recorded.", aspect)
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil {
		return verdict + " TUI is not connected to a runtime.", nil
	}
	readback, err := a.runtime.PolicyReadback(ctx)
	if err != nil {
		return "", fmt.Errorf("read runtime policy: %w; reopen the TUI and check /store", err)
	}
	var b strings.Builder
	b.WriteString("RUNTIME POLICY (configured; not proof of enforcement):\n")
	if readback.RuntimePolicy == "" {
		b.WriteString("  Runtime policy: NONE (no active policy governs runs)\n")
	} else {
		fmt.Fprintf(&b, "  Runtime policy: %s (active)\n", readback.RuntimePolicy)
	}
	switch readback.GateEngine {
	case "default placeholder":
		b.WriteString("  Gate engine:    DEFAULT PLACEHOLDER; its only check always passes, so it is a hook, not enforcement\n")
	case "configured":
		b.WriteString("  Gate engine:    CONFIGURED\n")
	default:
		b.WriteString("  Gate engine:    NONE\n")
	}
	b.WriteString("GATE DECISIONS (observed):\n")
	g := readback.Gates
	if g.Allowed+g.Denied == 0 {
		b.WriteString("  None recorded.\n")
	} else {
		digest := g.LastDigest
		if len(digest) > 19 {
			digest = digest[:19]
		}
		fmt.Fprintf(&b, "  %d allowed, %d denied; latest %s at %s (policy %s)\n", g.Allowed, g.Denied, g.LastPoint, g.LastAt, digest)
	}
	b.WriteString(verdict)
	return b.String(), nil
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
			b.WriteString(fmt.Sprintf("    Model:   %s\n", h.providerModelLine(ctx, pr.HarnessName)))
			b.WriteString("    Auth:    UNKNOWN (no execution performed)\n")
			b.WriteString("    Egress:  governed cells BLOCKED_BY_POLICY (sandbox uses --unshare-net; per-endpoint egress unenforceable); native sessions UNKNOWN (they run in the provider's own environment; not observed)\n")
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
			return "Usage: /provider config <harness|provider>", nil
		}

		// MARSHAL reaches providers through harnesses, so an API provider name
		// resolves to the harness that serves it. This command inspects; it
		// changes no configuration.
		requested := strings.ToLower(args[1])
		name := map[string]string{"anthropic": "claude", "claude-code": "claude", "openai": "codex", "agy": "antigravity", "google": "antigravity", "gemini": "antigravity"}[requested]
		if name == "" {
			name = requested
		}
		via := ""
		if name != requested {
			via = fmt.Sprintf(" (provider %s is reached through the %s harness)", requested, name)
		}
		const configure = "\n  This inspects the harness; it changes no configuration.\n  Configure: model with /model select <codex|claude> <model>, Codex reasoning effort with /effort, credentials with the harness's own login."
		for _, pr := range ProbeHarnesses() {
			if pr.HarnessName != name {
				continue
			}
			if !pr.Installed {
				return fmt.Sprintf("Harness %s%s: %s\n  %s%s", name, via, StateUnavailable, pr.Reason, configure), nil
			}
			return fmt.Sprintf("Harness %s%s: %s (%s)\n  Version: %s\n  Model:   %s\n  Credentials are held by the harness; MARSHAL does not store them.\n  Auth state: UNKNOWN until an execution establishes it.%s",
				name, via, pr.State, pr.BinaryPath, pr.Version, h.providerModelLine(ctx, name), configure), nil
		}
		return fmt.Sprintf("Unknown harness or provider %q. Known harnesses: claude, codex, opencode, antigravity (providers anthropic, openai, google map to them). Run /provider status to see what this host provides.", requested), nil
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

// handleModel shows model preferences and selects the model future governed
// runs of an adapter use, through the authenticated operator boundary.
func (h *CommandHandler) handleModel(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || strings.EqualFold(args[0], "show") {
		return h.renderModelSelections(ctx)
	}
	if !strings.EqualFold(args[0], "select") || len(args) != 3 {
		return "Usage: /model select <codex|claude> <model_name>  (or /model show)", nil
	}
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil || a.localControl == nil {
		return "Model selection was NOT applied: authenticated runtime authorization is required.", nil
	}
	adapter := strings.ToLower(args[1])
	if adapter != "codex" && adapter != "claude" {
		return fmt.Sprintf("Model selection was NOT applied: %s runs do not read a model preference. Supported: codex, claude.", args[1]), nil
	}
	revision, _, err := a.runtime.ModelPreferenceRevision(ctx, adapter)
	if err != nil {
		return "", fmt.Errorf("read model preference: %w", err)
	}
	e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, TargetID: "model:" + adapter,
		ExpectedVersion: revision, IdempotencyKey: uuid.NewString()}
	preference, err := a.runtime.CommandSetModel(a.localControl.Context(ctx), e, adapter, args[2])
	if err != nil {
		return fmt.Sprintf("Model selection was NOT applied: %v", err), nil
	}
	return fmt.Sprintf("Future governed %s runs will use %s (preference revision %d). Runs already started keep their model.", adapter, preference.Model, preference.Revision), nil
}

// renderModelSelections reports the persisted model preference per harness.
func (h *CommandHandler) renderModelSelections(ctx context.Context) (string, error) {
	if h.ws.store == nil {
		return configStoreUnavailable, nil
	}
	var b strings.Builder
	if a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority); ok && a != nil && a.runtime != nil {
		b.WriteString("EXECUTION MODEL PREFERENCES (read by future governed runs):\n")
		for _, adapter := range app.ModelPreferenceAdapters {
			revision, current, err := a.runtime.ModelPreferenceRevision(ctx, adapter)
			if err != nil {
				return "", fmt.Errorf("read model preference: %w", err)
			}
			if current == "" {
				current = "(none; the adapter's own default applies)"
			} else {
				current = fmt.Sprintf("%s (revision %d)", current, revision)
			}
			b.WriteString(fmt.Sprintf("  %-12s %s\n", adapter, current))
		}
	}
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
		return h.setCodexEffort(ctx, args[0])
	}
	var applied string
	if a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority); ok && a != nil && a.runtime != nil {
		preference, err := a.runtime.CodexModelPreference(ctx)
		switch {
		case errors.Is(err, model.ErrNotFound):
			applied = "CODEX EXECUTION EFFORT (read by future governed Codex runs):\n  No Codex model is selected; runs use the model's own default effort.\n"
		case err != nil:
			return "", fmt.Errorf("read reasoning preference: %w; reopen the TUI and check /store", err)
		default:
			effort := preference.Effort
			if effort == "" {
				effort = "model default"
			}
			applied = fmt.Sprintf("CODEX EXECUTION EFFORT (read by future governed Codex runs):\n  Model %s, effort %s (preference revision %d)\n  Claude runs read no reasoning effort.\n", preference.Model, effort, preference.Revision)
		}
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
	return applied + fmt.Sprintf("REASONING EFFORT (NOT APPLIED TO RUNTIME):\n  Harness (advisory route): %s\n  Selected effort: UNKNOWN (no canonical preference read-back)\n  Advisory route default: %s\n  Probed reasoning knobs: %s", plan.Harness, orNone(plan.ReasoningEffort), knobs), nil
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
		offline := "Offline: exit the TUI, stop the daemon, then run marshal state restore <backup_path>."
		if len(args) == 2 {
			if !store.DatabaseInUseCheckSupported() {
				return fmt.Sprintf("Backup %s verified (schema v%d, SHA-256 %s).\nRestore from a live session needs open-file inspection (Linux). %s",
					args[1], meta.SchemaVersion, meta.DatabaseSHA256, offline), nil
			}
			digest := strings.TrimPrefix(meta.DatabaseSHA256, "sha256:")
			return fmt.Sprintf("Backup %s verified (schema v%d, SHA-256 %s).\n"+
				"Nothing has changed yet. Restoring replaces the whole project state. The current state is backed up first, and the restore is refused while any MARSHAL window or the daemon has the database open.\n"+
				"To restore now: /backup restore %s confirm %s\n%s", args[1], meta.SchemaVersion, meta.DatabaseSHA256, args[1], digest[:12], offline), nil
		}
		return h.restoreBackup(ctx, args[1], meta.DatabaseSHA256, args[3])

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

// handleRuntime shows runtime status.
// sessionRuns reads this session's execution runs from the canonical engine.
func (h *CommandHandler) sessionRuns(ctx context.Context) (*runtimeControlAuthority, []execution.ExecutionRun, error) {
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil {
		return nil, nil, nil
	}
	service, err := a.execution()
	if err != nil {
		return a, nil, nil
	}
	runs, err := service.Engine().ListRuns(ctx)
	if err != nil {
		return a, nil, err
	}
	var mine []execution.ExecutionRun
	for _, run := range runs {
		if run.SessionID == a.sessionID {
			mine = append(mine, run)
		}
	}
	return a, mine, nil
}

func (h *CommandHandler) handleRuntime(ctx context.Context) (string, error) {
	a, runs, err := h.sessionRuns(ctx)
	if err != nil {
		return "", fmt.Errorf("read runs: %w; reopen the TUI and check /store", err)
	}
	if a == nil {
		return fmt.Sprintf("RUNTIME STATUS:\n  Execution state: NOT VERIFIED\n  Session label:   %s\n  TUI is not connected to a runtime.\n", h.ws.sessionID), nil
	}
	status, err := a.RuntimeStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("read runtime status: %w; reopen the TUI and check /store", err)
	}
	var b strings.Builder
	b.WriteString("RUNTIME STATUS (canonical store read-back; process liveness is not probed):\n")
	fmt.Fprintf(&b, "  Instance:  %s\n", orNone(a.RuntimeInstanceID()))
	fmt.Fprintf(&b, "  Schema:    v%d\n", status.SchemaVersion)
	fmt.Fprintf(&b, "  Records:   %d agents, %d sessions, %d tasks, %d leases\n", status.AgentCount, status.SessionCount, status.TaskCount, status.LeaseCount)
	fmt.Fprintf(&b, "  Session:   %s\n", h.ws.sessionID)
	if len(runs) == 0 {
		b.WriteString("  Runs:      none in this session\n")
	}
	for _, run := range runs {
		policy := run.PolicySnapshot
		if policy == "" {
			policy = "none recorded"
		}
		fmt.Fprintf(&b, "  Run %s: %s (goal %s rev %d, %d tasks, policy snapshot %s)\n", run.RunID, run.State, orNone(run.GoalID), run.GoalRevision, len(run.Tasks), policy)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// handleStore renders bounded, read-only canonical store diagnostics.
func (h *CommandHandler) handleStore(ctx context.Context, args []string) (string, error) {
	if h.ws.store == nil {
		return configStoreUnavailable, nil
	}
	authority := &runtimeControlAuthority{store: h.ws.store}
	ctx, cancel := context.WithTimeout(ctx, store.DiagnosticTimeout)
	defer cancel()
	fail := func(err error) (string, error) {
		return "", fmt.Errorf("store diagnostics: %w; reopen the TUI and check the project database", err)
	}
	var out strings.Builder
	out.WriteString("STORE STATUS:\n  Backend: SQLite\n")
	if len(args) == 0 {
		ver, err := authority.StoreSchemaVersion(ctx)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(&out, "  Schema: v%d (Latest: v%d)\n", ver, store.LatestSchemaVersion)
	}
	if len(args) == 0 || strings.EqualFold(args[0], "check") {
		check := "quick_check"
		var err error
		if len(args) == 2 && strings.EqualFold(args[1], "full") {
			check = "integrity_check"
			out.WriteString("  Full integrity_check can take long; timeout: 5s.\n")
			err = authority.StoreIntegrity(ctx)
		} else {
			err = authority.StoreQuickCheck(ctx)
		}
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(&out, "  SQLite %s passed.\n", check)
	}
	if len(args) == 0 || strings.EqualFold(args[0], "counts") {
		out.WriteString("  Inventory counts (not proof of health):\n")
		for _, table := range store.CountTables() {
			count, err := authority.ObjectCount(ctx, table)
			if err != nil {
				return fail(err)
			}
			fmt.Fprintf(&out, "    %s: %d\n", table, count)
		}
	}
	return out.String(), nil
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
// handleBlind answers plainly: no blind interpretations are collected in
// this build, so there is nothing to read back or resolve.
func (h *CommandHandler) handleBlind(ctx context.Context, args []string) (string, error) {
	if len(args) > 0 && strings.EqualFold(args[0], "resolve") {
		return "Blind-interpretation resolution was NOT recorded: blind interpretation is not available in this build.", nil
	}
	return "BLIND INTERPRETATION: not available in this build. No interpretations are collected, so state is NOT VERIFIED and there is nothing to resolve.", nil
}

// handleReinjection handles constraint reinjection digests.
// handleReinjection reads back, per run of this session, the goal revision
// and hard constraints the run is bound to and the constraint package digest
// each native turn actually received.
func (h *CommandHandler) handleReinjection(ctx context.Context) (string, error) {
	a, runs, err := h.sessionRuns(ctx)
	if err != nil {
		return "", fmt.Errorf("read runs: %w; reopen the TUI and check /store", err)
	}
	if a == nil {
		return "CONSTRAINT RE-INJECTION:\n  State: NOT VERIFIED (TUI is not connected to a runtime).", nil
	}
	if len(runs) == 0 {
		return "CONSTRAINT RE-INJECTION:\n  No runs in this session, so no constraint package has been injected.", nil
	}
	h.ws.mu.RLock()
	goal := h.ws.state.Goal
	h.ws.mu.RUnlock()
	var b strings.Builder
	b.WriteString("CONSTRAINT RE-INJECTION (execution-bound read-back):\n")
	for _, run := range runs {
		binding := "CURRENT"
		if run.GoalID != goal.ID || run.GoalRevision != goal.Revision {
			binding = fmt.Sprintf("STALE (current goal %s rev %d)", orNone(goal.ID), goal.Revision)
		}
		fmt.Fprintf(&b, "  Run %s: goal %s rev %d, %s; %d hard constraints bound\n", run.RunID, orNone(run.GoalID), run.GoalRevision, binding, len(run.HardConstraints))
		injected := 0
		for id, task := range run.Tasks {
			if task.NativeTurn == nil {
				continue
			}
			injected++
			digest := task.NativeTurn.ConstraintDigest
			if len(digest) > 19 {
				digest = digest[:19]
			}
			fmt.Fprintf(&b, "    task %s: %s turn %s received constraint package %s\n", id, task.NativeTurn.Provider, orNone(task.NativeTurn.TurnID), orNone(digest))
		}
		if injected == 0 {
			b.WriteString("    No native turn has started, so nothing has been injected yet.\n")
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// handleAlignment handles alignment guard state.

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

// setCodexEffort records the reasoning effort future governed Codex runs
// request for the selected model, through the authenticated operator
// boundary. "default" returns to the model's own default.
func (h *CommandHandler) setCodexEffort(ctx context.Context, level string) (string, error) {
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil || a.localControl == nil {
		return "Reasoning effort was NOT applied: authenticated runtime authorization is required.", nil
	}
	level = strings.ToLower(level)
	if level == "default" {
		level = ""
	}
	revision, current, err := a.runtime.ModelPreferenceRevision(ctx, "codex")
	if err != nil {
		return "", fmt.Errorf("read reasoning preference: %w", err)
	}
	if current == "" {
		return "Reasoning effort was NOT applied: select a Codex model first with /model select codex <model>; the effort is validated against that model.", nil
	}
	e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, TargetID: "effort:codex",
		ExpectedVersion: revision, IdempotencyKey: uuid.NewString()}
	preference, err := a.runtime.CommandSetEffort(a.localControl.Context(ctx), e, "codex", level)
	if err != nil {
		return fmt.Sprintf("Reasoning effort was NOT applied: %v", err), nil
	}
	effort := preference.Effort
	if effort == "" {
		effort = "the model's default effort"
	}
	return fmt.Sprintf("Future governed Codex runs of %s will request %s (preference revision %d). Runs already started keep theirs.", preference.Model, effort, preference.Revision), nil
}

// providerModelLine names the model a harness's governed runs would use: the
// execution preference for Codex and Claude, otherwise the harness default,
// which the probe does not establish.
func (h *CommandHandler) providerModelLine(ctx context.Context, harnessName string) string {
	if harnessName == "codex" || harnessName == "claude" {
		if a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority); ok && a != nil && a.runtime != nil {
			if revision, current, err := a.runtime.ModelPreferenceRevision(ctx, harnessName); err == nil && current != "" {
				return fmt.Sprintf("%s (execution preference, revision %d)", current, revision)
			}
		}
	}
	return UnknownModel + " (harness default; not established by probe)"
}

// restoreBackup performs a coordinated restore once the operator confirmed
// the backup's digest, and swaps the workspace onto the reopened runtime.
func (h *CommandHandler) restoreBackup(ctx context.Context, path, digest, confirmed string) (string, error) {
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil || a.localControl == nil {
		return "Restore was NOT performed: authenticated runtime authorization is required.", nil
	}
	if len(confirmed) < 12 || !strings.HasPrefix(strings.TrimPrefix(digest, "sha256:"), confirmed) {
		return fmt.Sprintf("Restore was NOT performed: %q does not match the backup's digest; run /backup restore %s to see it again.", confirmed, path), nil
	}
	e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, TargetID: "state:" + path, IdempotencyKey: uuid.NewString()}
	result, err := a.runtime.CommandRestoreState(a.localControl.Context(ctx), e, path, digest)
	if result.Runtime != nil && result.Runtime != a.runtime && a.replaceRuntime != nil {
		a.replaceRuntime(result.Runtime)
	}
	if err != nil {
		recovery := ""
		if result.RecoveryPath != "" {
			recovery = " The state before the attempt is backed up at " + result.RecoveryPath + "."
		}
		return fmt.Sprintf("Restore was NOT performed: %v.%s", err, recovery), nil
	}
	return fmt.Sprintf("Project state restored from %s; the restored database matches the confirmed digest and has been reopened.\nThe previous state is backed up at %s (restore it the same way to undo).",
		path, result.RecoveryPath), nil
}
