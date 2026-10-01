package tui

import (
	"context"
	"errors"
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
func runGovernedClaudeCmd(ctx context.Context, args []string) (result string, resultErr error) {
	args = app.NormalizeProviderArgs("claude", args)

	binary, err := project.FindBinary("claude")
	if err != nil {
		return "", fmt.Errorf("claude binary not found on PATH: %w; install Claude and make claude available on PATH, then retry", err)
	}
	if err := claude.ValidateDangerousFlags(args); err != nil {
		return "", err
	}
	dialect := app.ObserveProviderDialect(ctx, "claude")
	if dialect.Operation(app.ProviderArgOperation("claude", args)).Status == app.ProviderUnknown {
		defer func() {
			label := "UNKNOWN — unqualified pass-through: " + dialect.Provider + " " + app.ProviderArgOperation("claude", args)
			if resultErr != nil {
				resultErr = fmt.Errorf("%s: %w", label, resultErr)
			}
		}()
	}
	if err := dialect.Check(app.ProviderArgOperation("claude", args), false); err != nil {
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
func (h *CommandHandler) handleClaude(ctx context.Context, args []string, line string) (result string, resultErr error) {
	prompt, rejection := parseProviderCommand("claude", line, args)
	if rejection != "" {
		return rejection, nil
	}
	if prompt != "" {
		if h.ws.terminal != nil && h.ws.terminal.IsTerminal() {
			return h.ws.runNativeAgent(ctx, "claude", []string{"--", prompt})
		}
		args = []string{"exec", prompt}
		line = "/claude exec " + prompt
	}

	interactive := h.ws.terminal != nil && h.ws.terminal.IsTerminal()
	if usage := governedAgentUsage("claude", args, interactive); usage != "" {
		return usage, nil
	}
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "sessions", "runs", "history":
			if len(args) != 1 {
				return "Usage: /claude sessions", nil
			}
			return h.handleSessionInventory(ctx, "claude")
		case "resume", "fork", "continue":
			return h.handleNativeSelection(ctx, "claude", args)
		}
	}
	if len(args) > 0 && app.ObserveProviderDialect(ctx, "claude").WrapperOperation(strings.ToLower(args[0]), interactive).Status == app.ProviderUnknown {
		defer func() {
			result = "UNKNOWN — unqualified pass-through: claude " + strings.ToLower(args[0]) + "\n" + result
		}()
	}
	if len(args) > 0 {
		args[0] = strings.ToLower(args[0])
	}
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
		}
	}
	if len(args) > 0 && oneOf(args[0], "mcp", "plugin", "auth", "agents", "login", "logout") {
		return "Native Claude management requires an interactive terminal. Open marshal tui in a terminal with the Claude CLI on PATH, then retry /claude " + args[0] + ".", nil
	}
	source := h.ws.controlSource()
	if source == nil || source.Authority == nil {
		return "Claude control authority unavailable: no runtime attached to workspace. Open the TUI in an initialized MARSHAL project (marshal init); native Claude commands require an interactive terminal and the Claude CLI on PATH.", nil
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
		b.WriteString("\n" + app.ObserveProviderDialect(ctx, "claude").Help(providerHelpOperations("claude"), interactive))
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
		if strings.HasPrefix(targetModel, "-") {
			return fmt.Sprintf("Failed to select Claude model %q: expected a model slug, not a CLI flag. Use /claude models to see eligible models.", targetModel), nil
		}
		current, err := auth.ClaudeModelPreference(ctx)
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return fmt.Sprintf("Failed to read Claude model preference: %v; reopen the TUI and retry /claude model <slug>.", err), nil
		}
		pref, err := auth.ClaudeSelectModel(ctx, targetModel, current.Revision)
		if err != nil {
			return fmt.Sprintf("Failed to select Claude model %q: %v", targetModel, err), nil
		}
		return fmt.Sprintf("Selected Claude model successfully switched to %q (revision: %d).", pref.Model, pref.Revision), nil

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
		prompt := agentPrompt(line)
		return h.handleClaudeExec(ctx, auth, prompt)

	default:
		return "Unknown subcommand. To send a prompt use /claude exec <text>", nil
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
