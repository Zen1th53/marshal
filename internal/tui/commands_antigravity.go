package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/project"
)

// handleAntigravity implements /agy and its long form /antigravity.
func (h *CommandHandler) handleAntigravity(ctx context.Context, args []string, line string) (string, error) {
	prompt, rejection := parseProviderCommand("agy", line, args)
	if rejection != "" {
		return rejection, nil
	}
	if prompt != "" {
		return h.ws.runNativeAgent(ctx, "antigravity", []string{"--prompt-interactive", prompt})
	}

	if usage := nativeSessionUsage("agy", args); usage != "" {
		return usage, nil
	}
	interactive := h.ws.terminal != nil && h.ws.terminal.IsTerminal()
	if len(args) == 0 && interactive {
		return h.ws.runNativeAgent(ctx, "antigravity", nil)
	}
	if len(args) == 0 || strings.EqualFold(args[0], "help") || strings.EqualFold(args[0], "status") {
		return antigravityHelp(ctx)
	}

	switch strings.ToLower(args[0]) {
	case "new", "open", "interactive", "chat":
		return h.ws.runNativeAgent(ctx, "antigravity", nil)
	case "continue":
		return h.ws.runNativeAgent(ctx, "antigravity", []string{"--continue"})
	case "resume":
		if len(args) == 1 {
			return h.ws.runNativeAgent(ctx, "antigravity", []string{"--continue"})
		}
		if args[1] == "--last" {
			return h.ws.runNativeAgent(ctx, "antigravity", append([]string{"--continue"}, args[2:]...))
		}
		return h.ws.runNativeAgent(ctx, "antigravity", append([]string{"--conversation", args[1]}, args[2:]...))
	case "cli":
		argv, err := openCodeCLIArgs(line)
		if err != nil {
			return "", err
		}
		return h.ws.runNativeAgent(ctx, "antigravity", argv)
	case "models", "agents", "agent", "mcp", "plugin", "plugins", "changelog":
		argv, err := nativeArgs(strings.TrimSpace(line[len(strings.Fields(line)[0]):]))
		if err != nil {
			return "", err
		}
		return h.ws.runNativeAgent(ctx, "antigravity", argv)
	case "prompt":
		if len(args) < 2 {
			return "Usage: /agy prompt <text>", nil
		}
		// agy's own flag for "start interactively with this prompt", so the
		// session stays open for the operator after the first answer.
		prompt := agentPrompt(line)
		return h.ws.runNativeAgent(ctx, "antigravity", []string{"--prompt-interactive", prompt})
	}
	return "Usage: /agy prompt <text>", nil
}

func antigravityHelp(ctx context.Context) (string, error) {
	binary, err := project.FindBinary(antigravityBinary)
	if err != nil {
		return "ANTIGRAVITY NATIVE SESSION:\n  Available: false\n  Install the Antigravity CLI and ensure `agy` is on PATH.\n  Send a prompt: /agy prompt <text> or /agy \"<text>\".\n  Unknown subcommands run nothing.", nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, probeErr := exec.CommandContext(probeCtx, binary, "--version").CombinedOutput()
	version := strings.TrimSpace(string(out))
	if probeErr != nil {
		version = "probe failed: " + probeErr.Error()
	}
	return fmt.Sprintf(`ANTIGRAVITY NATIVE SESSION:
  Available: true
  Version:   %s

Available subcommands:
  /agy new                      Start a new native Antigravity session
  /agy continue                 Continue the most recent conversation
  /agy resume [conversation]    Resume the latest or a selected conversation
  /agy cli <args...>            Pass native agy arguments unchanged
  /agy models / agents          List models or agents
  /agy mcp / plugin             Manage MCP servers or plugins
  /agy prompt <text>              Open the session with an initial prompt

/antigravity is the same command.
Top-level launch: marshal agy [native arguments...]`, version), nil
}
