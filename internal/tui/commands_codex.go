package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

// runGovernedCodexCmd executes a native Codex CLI command within MARSHAL's isolated,
// governed CODEX_HOME environment so no host config or credentials are ever corrupted.
func runGovernedCodexCmd(ctx context.Context, args []string) (string, error) {
	binary, err := project.FindBinary("codex")
	if err != nil {
		return "", fmt.Errorf("codex binary not found on PATH: %w; Install Codex and make codex available on PATH, then retry", err)
	}
	govHome, err := codex.EnsureGovernedCodexHome()
	if err != nil {
		return "", fmt.Errorf("ensure governed CODEX_HOME: %w", err)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, binary, args...)
	cmd.Env = append(os.Environ(), "CODEX_HOME="+govHome)
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		return outStr, fmt.Errorf("codex %s failed: %w\n%s\nUse /codex cli %s --help to check native usage", strings.Join(args, " "), err, outStr, args[0])
	}
	return outStr, nil
}

// handleCodex exposes native interactive sessions and governed task operations.
func (h *CommandHandler) handleCodex(ctx context.Context, args []string, line string) (string, error) {
	interactive := h.ws.terminal != nil && h.ws.terminal.IsTerminal()
	if usage := governedAgentUsage("codex", args, interactive); usage != "" {
		return usage, nil
	}
	if len(args) > 0 {
		args[0] = strings.ToLower(args[0])
		if args[0] == "plugins" {
			args[0] = "plugin"
		}
	}
	if len(args) > 1 && oneOf(args[0], "mcp", "plugin", "plugins", "features", "skill") {
		args[1] = strings.ToLower(args[1])
	}
	if len(args) == 0 && h.ws.terminal != nil && h.ws.terminal.IsTerminal() {
		return h.ws.runNativeCodex(ctx, nil)
	}
	if len(args) > 0 {
		sub := strings.ToLower(args[0])
		if h.ws.terminal != nil && h.ws.terminal.IsTerminal() {
			switch sub {
			case "mcp", "plugin", "features", "agents", "login", "logout", "review", "doctor":
				argv, err := nativeArgs(line)
				if err != nil {
					return "", err
				}
				if strings.EqualFold(argv[0], "/codex") {
					argv = argv[2:]
				} else if strings.EqualFold(argv[0], "/doctor") {
					// codex/provider selects the diagnostic target, not a native argument.
					argv = nil
				} else {
					argv = argv[1:]
				}
				if sub == "features" {
					if len(argv) == 0 {
						argv = []string{"list"}
					} else if oneOf(strings.ToLower(argv[0]), "list", "enable", "disable") {
						argv[0] = strings.ToLower(argv[0])
					}
				}
				return h.ws.runNativeCodex(ctx, append([]string{sub}, argv...))
			case "sandbox":
				if len(args) == 2 && oneOf(strings.ToLower(args[1]), "read-only", "workspace-write") {
					return h.ws.runNativeCodex(ctx, []string{"--sandbox", strings.ToLower(args[1])})
				}
			case "approval":
				if len(args) == 2 && (args[1] == "on-request" || args[1] == "never") {
					return h.ws.runNativeCodex(ctx, []string{"--ask-for-approval", args[1]})
				}
			case "search":
				if len(args) == 2 && oneOf(strings.ToLower(args[1]), "on", "enable", "true") {
					return h.ws.runNativeCodex(ctx, []string{"--search"})
				}
				if len(args) == 2 && oneOf(strings.ToLower(args[1]), "off", "disable", "false") {
					return h.ws.runNativeCodex(ctx, []string{"-c", `web_search="disabled"`})
				}
			}
		}
		switch sub {
		case "cli", "interactive", "chat", "open", "tui", "new", "continue":
			if sub == "continue" {
				return h.ws.runNativeCodex(ctx, []string{"resume", "--last"})
			}
			if sub == "new" {
				return h.ws.runNativeCodex(ctx, nil)
			}
			tail := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), strings.Fields(line)[0]))
			if strings.EqualFold(strings.Fields(line)[0], "/codex") && tail != "" {
				tail = strings.TrimSpace(strings.TrimPrefix(tail, strings.Fields(tail)[0]))
			}
			argv, err := nativeArgs(tail)
			if err != nil {
				return "", err
			}
			return h.ws.runNativeCodex(ctx, argv)
		case "resume", "fork":
			if h.ws.terminal != nil && h.ws.terminal.IsTerminal() {
				return h.ws.runNativeCodex(ctx, append([]string{sub}, args[1:]...))
			}
		}
	}
	source := h.ws.controlSource()
	if source == nil || source.Authority == nil {
		return "Codex control authority unavailable: no runtime attached to workspace. Open the TUI in an initialized MARSHAL project (marshal init); native Codex commands also require an interactive terminal and the Codex CLI on PATH.", nil
	}
	auth := source.Authority

	if len(args) == 0 || args[0] == "status" || args[0] == "info" || args[0] == "health" || args[0] == "help" {
		health, err := auth.CodexHealth(ctx)
		if err != nil {
			return "", fmt.Errorf("codex health probe: %w", err)
		}
		selectedModel, _ := auth.SelectedCodexModel(ctx)
		if selectedModel == "" {
			selectedModel = "(default)"
		}
		var b strings.Builder
		b.WriteString("CODEX GOVERNED CONTROL PLANE:\n")
		b.WriteString(fmt.Sprintf("  Status:        %s\n", health.Verdict))
		b.WriteString(fmt.Sprintf("  Available:     %t\n", health.Available))
		if health.BinaryPath != "" {
			b.WriteString("  Executable:    available on PATH\n")
		}
		if health.Version != "" {
			b.WriteString(fmt.Sprintf("  Version:       %s\n", health.Version))
		}
		b.WriteString(fmt.Sprintf("  App-Server:    %s (%s)\n", health.AppServerStatus, health.AppServerReason))
		b.WriteString(fmt.Sprintf("  Active Model:  %s\n", selectedModel))
		if len(health.Checks) > 0 {
			b.WriteString("  Checks:\n")
			keys := make([]string, 0, len(health.Checks))
			for k := range health.Checks {
				if k != "binary" { // Never expose host paths
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			for _, k := range keys {
				b.WriteString(fmt.Sprintf("    %-14s %s\n", k+":", health.Checks[k]))
			}
		}
		b.WriteString("\nAvailable subcommands:\n")
		b.WriteString("  /codex doctor              Run native Codex doctor diagnostics\n")
		b.WriteString("  /codex models              List available models and selection status\n")
		b.WriteString("  /codex model <slug>        Select active Codex model for execution\n")
		b.WriteString("  /codex review              Native review; headless: governed commit review\n")
		b.WriteString("  /codex sessions            List recorded governed sessions and run history\n")
		b.WriteString("  /codex mcp <list|add|rm>   Manage external MCP servers for Codex\n")
		b.WriteString("  /codex plugin <list|add|rm> Manage plugins and marketplaces\n")
		b.WriteString("  /codex apply [task_id]     Apply latest diff produced by Codex to working tree\n")
		b.WriteString("  /codex diff                Inspect pending diff from Codex tasks\n")
		b.WriteString("  /codex resume [id|--last]  Resume a previous Codex session\n")
		b.WriteString("  /codex fork [id|--last]    Fork a previous Codex session\n")
		b.WriteString("  /codex agents              Native agents; headless: recorded worker runs\n")
		b.WriteString("  /codex features            Inspect and toggle feature flags\n")
		b.WriteString("  /codex sandbox [mode]      Inspect or set sandbox policy (read-only/workspace-write)\n")
		b.WriteString("  /codex approval [policy]   Inspect or set approval policy (on-request/never)\n")
		b.WriteString("  /codex search [on|off]     Toggle web search tool for Codex\n")
		b.WriteString("  /codex login / /codex logout Native authentication (interactive terminal required)\n")
		b.WriteString("  /codex skill install <name> Install a local skill with digest verification\n")
		b.WriteString("  /codex run <task_id>       Dispatch an approved plan task to Codex\n")
		b.WriteString("  /codex exec <prompt...>    Directly create and launch a task with Codex\n")
		b.WriteString("  /codex cli [prompt...]     Launch native interactive Codex session\n")
		b.WriteString("  /codex new / continue    Start fresh or resume the latest project session\n")
		b.WriteString("  /codex cli <args...>      Pass native CLI arguments, including quoted paths\n")
		b.WriteString("  /codex <prompt...>         Any instruction runs directly with Codex!\n")
		return b.String(), nil
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "doctor":
		report, err := auth.CodexDoctor(ctx)
		if err != nil {
			return fmt.Sprintf("Codex doctor failed: %v", err), nil
		}
		return fmt.Sprintf("CODEX DOCTOR DIAGNOSTICS:\n  Overall Status: %s\n  Total Checks:   %d checks reported\n  Inspection:     Bounded and sanitized (no host credentials leaked)",
			strings.ToUpper(report.OverallStatus), report.CheckCount), nil

	case "models":
		models, def, err := auth.CodexModels(ctx)
		if err != nil {
			return fmt.Sprintf("Discover models failed: %v. Check Codex installation and authentication, then retry /models.", err), nil
		}
		selected, _ := auth.SelectedCodexModel(ctx)
		if selected == "" {
			selected = def
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("CODEX MODELS (%d eligible models discovered, default: %s):\n", len(models), def))
		for _, m := range models {
			marker := "  "
			if m.Slug == selected {
				marker = "▶ "
			}
			tag := ""
			if m.IsDefault {
				tag += " (default)"
			}
			if m.Slug == selected {
				tag += " [SELECTED]"
			}
			b.WriteString(fmt.Sprintf(" %s%-20s %-25s %s%s\n", marker, m.Slug, m.DisplayName, m.Visibility, tag))
		}
		b.WriteString(fmt.Sprintf("\nActive model: %s. Use `/codex model <slug>` to switch.\n", selected))
		return b.String(), nil

	case "model", "select":
		if len(args) < 2 {
			selected, _ := auth.SelectedCodexModel(ctx)
			return fmt.Sprintf("Current selected Codex model: %s\nUsage: /codex model <slug> to change", selected), nil
		}
		targetModel := args[1]
		if strings.HasPrefix(targetModel, "-") {
			return fmt.Sprintf("Failed to select Codex model %q: expected a model slug, not a CLI flag. Use /models to see eligible models.", targetModel), nil
		}
		current, err := auth.CodexModelPreference(ctx)
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return fmt.Sprintf("Failed to read Codex model preference: %v; reopen the TUI and retry /model <slug>.", err), nil
		}
		pref, err := auth.CodexSelectModel(ctx, targetModel, current.Revision)
		if err != nil {
			return fmt.Sprintf("Failed to select Codex model %q: %v. Use /models to check eligible models, then retry /model <slug>.", targetModel, err), nil
		}
		return fmt.Sprintf("Selected Codex model successfully switched to %q (revision: %d).", pref.Model, pref.Revision), nil

	case "review":
		res, err := auth.RunCodexReview(ctx)
		if err != nil {
			return fmt.Sprintf("Codex review failed: %v", err), nil
		}
		verified := res.ExitStatus == 0
		detail := strings.TrimSpace(res.Stdout)
		if detail == "" {
			detail = strings.TrimSpace(res.Stderr)
		}
		if detail == "" {
			detail = "ok"
		}
		return fmt.Sprintf("CODEX COMMIT REVIEW:\n  Commit:        %s\n  Verified:      %t\n  Output Digest: %s\n  Detail:        %s",
			res.Commit, verified, res.OutputDigest, detail), nil

	case "sessions", "runs", "history":
		sessions, err := auth.CodexSessions(ctx)
		if err != nil {
			return fmt.Sprintf("Failed to list Codex sessions: %v", err), nil
		}
		if len(sessions) == 0 {
			return "No governed Codex sessions recorded yet.", nil
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("RECORDED CODEX SESSIONS (%d total):\n", len(sessions)))
		for _, s := range sessions {
			b.WriteString(fmt.Sprintf("  %-16s Task: %-14s Run: %-16s Model: %-16s Status: %-10s Started: %s\n",
				s.SessionID, s.TaskID, s.RunID, s.Model, s.Status, s.StartedAt.Format("2006-01-02 15:04:05")))
		}
		return b.String(), nil

	case "mcp":
		if len(args) < 2 || args[1] == "list" {
			out, err := runGovernedCodexCmd(ctx, []string{"mcp", "list"})
			if err != nil {
				return fmt.Sprintf("Codex MCP list failed: %v", err), nil
			}
			if strings.TrimSpace(out) == "" {
				return "No MCP servers configured yet. Use `/codex mcp add <name> -- <cmd> [args...]` to add one.", nil
			}
			return fmt.Sprintf("CODEX MCP SERVERS:\n%s", out), nil
		}
		mcpSub := strings.ToLower(args[1])
		switch mcpSub {
		case "add":
			if len(args) < 4 {
				return "Usage: /codex mcp add <name> -- <command> [args...]", nil
			}
			mcpArgs := append([]string{"mcp"}, args[1:]...)
			out, err := runGovernedCodexCmd(ctx, mcpArgs)
			if err != nil {
				return fmt.Sprintf("Codex MCP add failed: %v", err), nil
			}
			return fmt.Sprintf("Codex MCP server added:\n%s", out), nil
		case "remove", "rm", "delete":
			if len(args) < 3 {
				return "Usage: /codex mcp remove <name>", nil
			}
			name := args[2]
			out, err := runGovernedCodexCmd(ctx, []string{"mcp", "remove", name})
			if err != nil {
				return fmt.Sprintf("Codex MCP remove failed: %v", err), nil
			}
			return fmt.Sprintf("Codex MCP server removed:\n%s", out), nil
		case "get":
			if len(args) < 3 {
				return "Usage: /codex mcp get <name>", nil
			}
			name := args[2]
			out, err := runGovernedCodexCmd(ctx, []string{"mcp", "get", name})
			if err != nil {
				return fmt.Sprintf("Codex MCP get failed: %v", err), nil
			}
			return fmt.Sprintf("Codex MCP server details:\n%s", out), nil
		default:
			out, err := runGovernedCodexCmd(ctx, append([]string{"mcp"}, args[1:]...))
			if err != nil {
				return fmt.Sprintf("Codex MCP %s failed: %v", mcpSub, err), nil
			}
			return out, nil
		}

	case "plugin", "plugins":
		if len(args) < 2 || args[1] == "list" {
			plugins, skills, err := auth.CodexPlugins(ctx)
			var b strings.Builder
			if err != nil {
				b.WriteString(fmt.Sprintf("Discovery incomplete: %v\n", err))
			}
			b.WriteString(fmt.Sprintf("CODEX PLUGINS (%d) & LOCAL SKILLS (%d):\n", len(plugins), len(skills)))
			if len(plugins) > 0 {
				b.WriteString("  Plugins:\n")
				for _, p := range plugins {
					status := "disabled"
					if p.Enabled {
						status = "enabled"
					}
					b.WriteString(fmt.Sprintf("    %-20s v%-8s market=%-16s (%s)\n", p.Name, p.Version, p.MarketplaceName, status))
				}
			}
			if len(skills) > 0 {
				b.WriteString("  Local Skills:\n")
				for _, s := range skills {
					b.WriteString(fmt.Sprintf("    %-24s %s\n", s.Name, s.Description))
				}
				b.WriteString("\nInstall a local skill with: /codex skill install <name>\n")
			}
			return b.String(), nil
		}
		pSub := strings.ToLower(args[1])
		switch pSub {
		case "add", "install":
			if len(args) < 3 {
				return "Usage: /codex plugin add <plugin_name>", nil
			}
			out, err := runGovernedCodexCmd(ctx, []string{"plugin", "add", args[2]})
			if err != nil {
				return fmt.Sprintf("Codex plugin add failed: %v", err), nil
			}
			return fmt.Sprintf("Plugin added:\n%s", out), nil
		case "remove", "rm", "uninstall":
			if len(args) < 3 {
				return "Usage: /codex plugin remove <plugin_name>", nil
			}
			out, err := runGovernedCodexCmd(ctx, []string{"plugin", "remove", args[2]})
			if err != nil {
				return fmt.Sprintf("Codex plugin remove failed: %v", err), nil
			}
			return fmt.Sprintf("Plugin removed:\n%s", out), nil
		case "marketplace":
			mArgs := append([]string{"plugin", "marketplace"}, args[2:]...)
			out, err := runGovernedCodexCmd(ctx, mArgs)
			if err != nil {
				return fmt.Sprintf("Codex plugin marketplace failed: %v", err), nil
			}
			return fmt.Sprintf("MARKETPLACES:\n%s", out), nil
		default:
			out, err := runGovernedCodexCmd(ctx, append([]string{"plugin"}, args[1:]...))
			if err != nil {
				return fmt.Sprintf("Codex plugin %s failed: %v", pSub, err), nil
			}
			return out, nil
		}

	case "skills":
		_, skills, err := auth.CodexPlugins(ctx)
		var b strings.Builder
		if err != nil {
			b.WriteString(fmt.Sprintf("Discovery incomplete: %v\n", err))
		}
		b.WriteString(fmt.Sprintf("LOCAL CODEX SKILLS (%d total):\n", len(skills)))
		for _, s := range skills {
			b.WriteString(fmt.Sprintf("  • %-24s %s\n", s.Name, s.Description))
		}
		b.WriteString("\nInstall a local skill with: /codex skill install <name>\n")
		return b.String(), nil

	case "skill":
		if len(args) != 3 || !strings.EqualFold(args[1], "install") {
			return "Usage: /codex skill install <skill_name>", nil
		}
		name := args[2]
		digest, err := auth.PreviewCodexSkill(name)
		if err != nil {
			return fmt.Sprintf("Preview skill %q failed: %v", name, err), nil
		}
		installedDigest, err := auth.InstallCodexSkill(ctx, name, digest)
		if err != nil {
			return fmt.Sprintf("Install skill %q failed: %v", name, err), nil
		}
		return fmt.Sprintf("Successfully installed project-local Codex skill %q\n  Immutable Digest: %s", name, installedDigest), nil

	case "apply":
		return h.applyCodexCloudTask(ctx, args[1:])

	case "diff":
		return h.handleDiff(ctx)

	case "resume":
		target := "--last"
		if len(args) >= 2 {
			target = args[1]
		}
		var execArgs []string
		if target == "--last" {
			execArgs = []string{"exec", "resume", "--last"}
		} else {
			execArgs = []string{"exec", "resume", target}
		}
		if len(args) >= 3 {
			prompt := strings.Join(args[2:], " ")
			execArgs = append(execArgs, prompt)
		}
		out, err := runGovernedCodexCmd(ctx, execArgs)
		if err != nil {
			return fmt.Sprintf("Codex resume failed: %v", err), nil
		}
		return fmt.Sprintf("CODEX SESSION RESUMED (%s):\n%s", target, out), nil

	case "fork":
		target := "--last"
		if len(args) >= 2 {
			target = args[1]
		}
		var execArgs []string
		if target == "--last" {
			execArgs = []string{"exec", "fork", "--last"}
		} else {
			execArgs = []string{"exec", "fork", target}
		}
		if len(args) >= 3 {
			prompt := strings.Join(args[2:], " ")
			execArgs = append(execArgs, prompt)
		}
		out, err := runGovernedCodexCmd(ctx, execArgs)
		if err != nil {
			return fmt.Sprintf("Codex fork failed: %v", err), nil
		}
		return fmt.Sprintf("CODEX SESSION FORKED (%s):\n%s", target, out), nil

	case "agents":
		sessions, err := auth.CodexSessions(ctx)
		if err != nil {
			return fmt.Sprintf("Failed to list Codex sessions: %v", err), nil
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("CODEX APP-SERVER AGENTS & SESSIONS (%d recorded runs):\n", len(sessions)))
		b.WriteString("  Source: governed worker-run history, not a live daemon inventory.\n")
		if len(sessions) == 0 {
			b.WriteString("  No governed agent runs recorded yet.\n")
		} else {
			for _, s := range sessions {
				b.WriteString(fmt.Sprintf("  • Session: %-16s | Task: %-14s | Run: %-16s | Status: %-10s\n",
					s.SessionID, s.TaskID, s.RunID, s.Status))
			}
		}
		return b.String(), nil

	case "features":
		if len(args) < 2 || strings.EqualFold(args[1], "list") {
			out, err := runGovernedCodexCmd(ctx, []string{"features", "list"})
			if err != nil {
				return fmt.Sprintf("Codex features failed: %v", err), nil
			}
			if strings.TrimSpace(out) == "" {
				return "Codex reported no feature flags. Use /codex cli features --help to inspect native support.", nil
			}
			lines := strings.Split(out, "\n")
			var b strings.Builder
			b.WriteString(fmt.Sprintf("CODEX FEATURE FLAGS (%d total, active sample):\n", len(lines)))
			count := 0
			for _, l := range lines {
				if strings.Contains(l, "true") || count < 15 {
					b.WriteString("  " + l + "\n")
					count++
				}
			}
			b.WriteString("\nUse `/codex features enable <feature>` or `/codex features disable <feature>` to toggle.\n")
			return b.String(), nil
		}
		subCmd := strings.ToLower(args[1])
		if (subCmd == "enable" || subCmd == "disable") && len(args) >= 3 {
			feat := args[2]
			out, err := runGovernedCodexCmd(ctx, []string{"features", subCmd, feat})
			if err != nil {
				return fmt.Sprintf("Codex feature %s %s failed: %v", subCmd, feat, err), nil
			}
			return fmt.Sprintf("Codex feature %s %s successfully.\n%s", feat, subCmd+"d", out), nil
		}
		return "Usage: /codex features [list | enable <feature> | disable <feature>]", nil

	case "sandbox":
		if len(args) < 2 {
			return "CODEX SANDBOX POLICY:\n  Native sessions use Codex settings. /codex sandbox <read-only|workspace-write> opens a session with that policy.", nil
		}
		mode := strings.ToLower(args[1])
		switch mode {
		case "read-only", "workspace-write":
			return fmt.Sprintf("Policy unchanged: %q requires an interactive native session.", mode), nil
		case "danger-full-access":
			return "Policy unchanged. Use /codex cli with explicit native arguments to configure a native session.", nil
		default:
			return "Invalid sandbox mode. Supported: read-only, workspace-write", nil
		}

	case "approval":
		if len(args) < 2 {
			return "CODEX APPROVAL POLICY:\n  Native sessions use Codex settings. /codex approval <on-request|never> opens a session with that policy.", nil
		}
		policy := strings.ToLower(args[1])
		switch policy {
		case "on-request", "never":
			return fmt.Sprintf("Policy unchanged: %q requires an interactive native session.", policy), nil
		default:
			return "Invalid approval policy. Supported: on-request, never", nil
		}

	case "search":
		if len(args) < 2 {
			return "CODEX WEB SEARCH:\n  Native sessions use Codex settings. /codex search <on|off> opens a session with that setting.", nil
		}
		opt := strings.ToLower(args[1])
		switch opt {
		case "on", "enable", "true":
			return "Search unchanged: enabling search requires an interactive native session.", nil
		case "off", "disable", "false":
			return "Search unchanged: disabling search requires an interactive native session.", nil
		default:
			return "Usage: /codex search [on|off]", nil
		}

	case "login", "logout":
		return "Authentication unchanged: login/logout requires an interactive native session.", nil

	case "run", "dispatch":
		if len(args) < 2 {
			return "Usage: /codex run <task_id> [model]", nil
		}
		taskID := args[1]
		modelName := ""
		if len(args) >= 3 {
			modelName = args[2]
		}
		req := CodexTaskDispatchRequest{TaskID: taskID, Model: modelName}
		res, err := auth.DispatchCodexTask(ctx, req)
		if err != nil {
			return fmt.Sprintf("Codex dispatch failed: %v", err), nil
		}
		return fmt.Sprintf("Dispatched task %s to Codex:\n  Run ID: %s\n  Status: %s", taskID, res.RunID, res.Status), nil

	case "exec":
		if len(args) < 2 {
			return "Usage: /codex exec <task prompt/instruction>", nil
		}
		prompt := agentPrompt(line)
		return h.handleCodexExec(ctx, auth, prompt)

	default:
		if interactive {
			prompt := strings.TrimSpace(line[len(strings.Fields(line)[0]):])
			return h.ws.runNativeCodex(ctx, []string{"--", prompt})
		}
		// If multiple words were supplied, treat the whole line after `/codex` as a direct prompt!
		// e.g. `/codex create a rest api endpoint` or `/codex test this package`
		if len(args) > 1 {
			cmdIdx := strings.Index(strings.ToLower(line), "/codex")
			prompt := strings.TrimSpace(line[cmdIdx+6:])
			return h.handleCodexExec(ctx, auth, prompt)
		}
		return fmt.Sprintf("Unknown Codex subcommand %q. Run `/codex` for help.", args[0]), nil
	}
}

func (h *CommandHandler) handleCodexExec(ctx context.Context, auth ControlAuthority, prompt string) (string, error) {
	taskID := fmt.Sprintf("TASK-CODEX-%d", time.Now().UnixNano()%1000000)

	h.ws.mu.RLock()
	rt := h.ws.runtime
	h.ws.mu.RUnlock()

	var codexAgentID string
	if rt != nil {
		// Ensure an eligible Codex agent is registered
		agents, err := rt.Agents(ctx)
		if err == nil {
			for _, a := range agents {
				if a.Status != model.AgentDisabled && a.ModelProvider == "codex" {
					codexAgentID = a.ID
					break
				}
			}
		}
		if codexAgentID == "" {
			newAgent, regErr := rt.RegisterAgent(ctx, app.RegisterAgentRequest{
				Name:          "codex-developer",
				Role:          model.RoleDeveloper,
				ModelProvider: "codex",
			})
			if regErr != nil {
				return "", fmt.Errorf("register codex agent: %w", regErr)
			}
			codexAgentID = newAgent.ID
		}

		// Create a unique task in store
		task := model.Task{
			ID:           taskID,
			Title:        prompt,
			Status:       model.TaskReady,
			Risk:         model.R1,
			OwnerAgentID: &codexAgentID,
		}
		if _, err := rt.Store().ImportTasks(ctx, []model.Task{task}); err != nil {
			return "", fmt.Errorf("import task: %w", err)
		}
	}

	// Dispatch the task through the control authority
	runRes, err := auth.DispatchCodexTask(ctx, CodexTaskDispatchRequest{
		TaskID:  taskID,
		AgentID: codexAgentID,
	})
	if err != nil {
		return fmt.Sprintf("Codex execution failed to start: %v", err), nil
	}

	modelName := runRes.Model
	if modelName == "" {
		modelName = runRes.RequestedModel
	}
	if modelName == "" {
		modelName = "(default)"
	}

	return fmt.Sprintf("CODEX TASK LAUNCHED:\n  Task ID:   %s\n  Run ID:    %s\n  Prompt:    %s\n  Model:     %s\n  Status:    %s\n\nExecution is running under MARSHAL governance. Track live in TUI or check /status.",
		taskID, runRes.RunID, prompt, modelName, runRes.Status), nil
}
