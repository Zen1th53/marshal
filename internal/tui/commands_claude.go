package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

// runGovernedClaudeCmd executes a native Claude CLI command inside MARSHAL's
// isolated CLAUDE_CONFIG_DIR so the operator's own Claude Code state and
// credentials are never modified by a governed inspection.
func runGovernedClaudeCmd(ctx context.Context, args []string) (string, error) {
	binary, err := project.FindBinary("claude")
	if err != nil {
		return "", fmt.Errorf("claude binary not found on PATH: %w", err)
	}
	if err := claude.ValidateDangerousFlags(args); err != nil {
		return "", err
	}
	govHome, err := claude.EnsureGovernedClaudeHome()
	if err != nil {
		return "", fmt.Errorf("ensure governed CLAUDE_CONFIG_DIR: %w", err)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, binary, args...)
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+govHome)
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		return outStr, fmt.Errorf("claude %s failed: %w", strings.Join(args, " "), err)
	}
	return outStr, nil
}

// handleClaude exposes the governed Claude control plane. It mirrors the Codex
// surface, minus the operations Claude Code has no equivalent for.
func (h *CommandHandler) handleClaude(ctx context.Context, args []string, line string) (string, error) {
	if len(args) == 0 && h.ws.terminal != nil && h.ws.terminal.IsTerminal() {
		return h.ws.runNativeAgent(ctx, "claude", nil)
	}
	if len(args) > 0 {
		sub := strings.ToLower(args[0])
		if h.ws.terminal != nil && h.ws.terminal.IsTerminal() {
			switch sub {
			case "mcp", "plugin", "auth", "agents", "doctor", "login", "logout":
				parsed, err := nativeArgs(line)
				if err != nil {
					return "", err
				}
				argv := append([]string{sub}, parsed[2:]...)
				if sub == "login" || sub == "logout" {
					argv = append([]string{"auth"}, argv...)
				}
				return h.ws.runNativeAgent(ctx, "claude", argv)
			}
		}
		switch sub {
		case "new", "cli", "interactive", "chat", "open", "tui":
			tail := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), strings.Fields(line)[0]))
			if tail != "" {
				tail = strings.TrimSpace(strings.TrimPrefix(tail, strings.Fields(tail)[0]))
			}
			argv, err := nativeArgs(tail)
			if err != nil {
				return "", err
			}
			return h.ws.runNativeAgent(ctx, "claude", argv)
		case "continue", "resume", "fork":
			parsed, err := nativeArgs(line)
			if err != nil {
				return "", err
			}
			tail := parsed[2:]
			argv := []string{"--continue"}
			if sub == "resume" || (sub == "fork" && len(tail) > 0) {
				argv = []string{"--resume"}
			}
			argv = append(argv, tail...)
			if sub == "fork" {
				argv = append(argv, "--fork-session")
			}
			return h.ws.runNativeAgent(ctx, "claude", argv)
		}
	}
	source := h.ws.controlSource()
	if source == nil || source.Authority == nil {
		return "Claude control authority unavailable: no runtime attached to workspace.", nil
	}
	auth := source.Authority

	if len(args) == 0 || args[0] == "status" || args[0] == "info" || args[0] == "health" || args[0] == "help" {
		health, err := auth.ClaudeHealth(ctx)
		if err != nil {
			return "", fmt.Errorf("claude health probe: %w", err)
		}
		selectedModel, _ := auth.SelectedClaudeModel(ctx)
		if selectedModel == "" {
			selectedModel = "(default)"
		}
		var b strings.Builder
		b.WriteString("CLAUDE GOVERNED CONTROL PLANE:\n")
		b.WriteString(fmt.Sprintf("  Status:        %s\n", health.Verdict))
		b.WriteString(fmt.Sprintf("  Available:     %t\n", health.Available))
		if health.BinaryPath != "" {
			b.WriteString("  Executable:    available on PATH\n")
		}
		if health.Version != "" {
			b.WriteString(fmt.Sprintf("  Version:       %s\n", health.Version))
		}
		b.WriteString(fmt.Sprintf("  Stream:        %s (%s)\n", health.StreamStatus, health.StreamReason))
		b.WriteString(fmt.Sprintf("  Active Model:  %s\n", selectedModel))
		b.WriteString("\nAvailable subcommands:\n")
		b.WriteString("  /claude new / continue    Start or continue native Claude with automatic memory\n")
		b.WriteString("  /claude resume / fork     Pick a conversation or fork the latest conversation\n")
		b.WriteString("  /claude cli <args...>     Native Claude arguments, MCP, plugins and authentication\n")
		b.WriteString("  /claude mcp / plugin / auth / agents  Native management commands\n")
		b.WriteString("  /claude doctor             Run native Claude doctor diagnostics\n")
		b.WriteString("  /claude models             List eligible models and selection status\n")
		b.WriteString("  /claude model <slug>       Select active Claude model for execution\n")
		b.WriteString("  /claude sessions           List recorded governed sessions and run history\n")
		b.WriteString("  /claude run <task_id>      Dispatch an approved plan task to Claude\n")
		b.WriteString("  /claude exec <prompt...>   Directly create and launch a task with Claude\n")
		b.WriteString("  /claude <prompt...>        Any instruction runs directly with Claude\n")
		return b.String(), nil
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "doctor":
		report, err := auth.ClaudeDoctor(ctx)
		if err != nil {
			return fmt.Sprintf("Claude doctor failed: %v", err), nil
		}
		return fmt.Sprintf("CLAUDE DOCTOR DIAGNOSTICS:\n  Overall Status: %s\n  Total Checks:   %d checks reported\n  Inspection:     Bounded and sanitized (no host credentials leaked)",
			strings.ToUpper(report.OverallStatus), report.CheckCount), nil

	case "models":
		models, def, err := auth.ClaudeModels(ctx)
		if err != nil {
			return fmt.Sprintf("Discover models failed: %v", err), nil
		}
		selected, _ := auth.SelectedClaudeModel(ctx)
		if selected == "" {
			selected = def
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("CLAUDE MODELS (%d eligible, resolved default: %s):\n", len(models), def))
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
			b.WriteString(fmt.Sprintf(" %s%-28s %-12s%s\n", marker, m.Slug, m.Visibility, tag))
		}
		b.WriteString(fmt.Sprintf("\nActive model: %s. Use `/claude model <slug>` to switch.\n", selected))
		return b.String(), nil

	case "model", "select":
		if len(args) < 2 {
			selected, _ := auth.SelectedClaudeModel(ctx)
			return fmt.Sprintf("Current selected Claude model: %s\nUsage: /claude model <slug> to change", selected), nil
		}
		targetModel := args[1]
		pref, err := auth.ClaudeSelectModel(ctx, targetModel, 0)
		if err != nil {
			return fmt.Sprintf("Failed to select Claude model %q: %v", targetModel, err), nil
		}
		return fmt.Sprintf("Selected Claude model successfully switched to %q (revision: %d).", pref.Model, pref.Revision), nil

	case "sessions", "runs", "history":
		sessions, err := auth.ClaudeSessions(ctx)
		if err != nil {
			return fmt.Sprintf("Failed to list Claude sessions: %v", err), nil
		}
		if len(sessions) == 0 {
			return "No governed Claude sessions recorded yet.", nil
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("RECORDED CLAUDE SESSIONS (%d total):\n", len(sessions)))
		for _, s := range sessions {
			b.WriteString(fmt.Sprintf("  %-16s Task: %-14s Run: %-16s Model: %-16s Status: %-10s Started: %s\n",
				s.SessionID, s.TaskID, s.RunID, s.Model, s.Status, s.StartedAt.Format("2006-01-02 15:04:05")))
		}
		return b.String(), nil

	case "run", "dispatch":
		if len(args) < 2 {
			return "Usage: /claude run <task_id> [model]", nil
		}
		taskID := args[1]
		modelName := ""
		if len(args) >= 3 {
			modelName = args[2]
		}
		res, err := auth.DispatchClaudeTask(ctx, ClaudeTaskDispatchRequest{TaskID: taskID, Model: modelName})
		if err != nil {
			return fmt.Sprintf("Claude dispatch failed: %v", err), nil
		}
		return fmt.Sprintf("Dispatched task %s to Claude:\n  Run ID: %s\n  Status: %s", taskID, res.RunID, res.Status), nil

	case "exec":
		if len(args) < 2 {
			return "Usage: /claude exec <task prompt/instruction>", nil
		}
		cmdIdx := strings.Index(line, args[0])
		prompt := strings.TrimSpace(line[cmdIdx+len(args[0]):])
		return h.handleClaudeExec(ctx, auth, prompt)

	default:
		if h.ws.terminal != nil && h.ws.terminal.IsTerminal() {
			prompt := strings.TrimSpace(line[len(strings.Fields(line)[0]):])
			return h.ws.runNativeAgent(ctx, "claude", []string{"--", prompt})
		}
		// Multiple words after /claude are treated as a direct instruction,
		// matching how /codex behaves.
		if len(args) > 1 {
			cmdIdx := strings.Index(strings.ToLower(line), "/claude")
			prompt := strings.TrimSpace(line[cmdIdx+7:])
			return h.handleClaudeExec(ctx, auth, prompt)
		}
		return fmt.Sprintf("Unknown Claude subcommand %q. Run `/claude` for help.", args[0]), nil
	}
}

func (h *CommandHandler) handleClaudeExec(ctx context.Context, auth ControlAuthority, prompt string) (string, error) {
	taskID := fmt.Sprintf("TASK-CLAUDE-%d", time.Now().UnixNano()%1000000)

	h.ws.mu.RLock()
	rt := h.ws.runtime
	h.ws.mu.RUnlock()

	var claudeAgentID string
	if rt != nil {
		agents, err := rt.Agents(ctx)
		if err == nil {
			for _, a := range agents {
				if a.Status != model.AgentDisabled && a.ModelProvider == "claude" {
					claudeAgentID = a.ID
					break
				}
			}
		}
		if claudeAgentID == "" {
			newAgent, regErr := rt.RegisterAgent(ctx, app.RegisterAgentRequest{
				Name:          "claude-developer",
				Role:          model.RoleDeveloper,
				ModelProvider: "claude",
			})
			if regErr != nil {
				return "", fmt.Errorf("register claude agent: %w", regErr)
			}
			claudeAgentID = newAgent.ID
		}

		task := model.Task{
			ID:           taskID,
			Title:        prompt,
			Status:       model.TaskReady,
			Risk:         model.R1,
			OwnerAgentID: &claudeAgentID,
		}
		if _, err := rt.Store().ImportTasks(ctx, []model.Task{task}); err != nil {
			return "", fmt.Errorf("import task: %w", err)
		}
	}

	runRes, err := auth.DispatchClaudeTask(ctx, ClaudeTaskDispatchRequest{
		TaskID:  taskID,
		AgentID: claudeAgentID,
	})
	if err != nil {
		return fmt.Sprintf("Claude execution failed to start: %v", err), nil
	}

	modelName := runRes.Model
	if modelName == "" {
		modelName = runRes.RequestedModel
	}
	if modelName == "" {
		modelName = "(default)"
	}

	return fmt.Sprintf("CLAUDE TASK LAUNCHED:\n  Task ID:   %s\n  Run ID:    %s\n  Prompt:    %s\n  Model:     %s\n  Status:    %s\n\nExecution is running under MARSHAL governance. Track live in TUI or check /status.",
		taskID, runRes.RunID, prompt, modelName, runRes.Status), nil
}
