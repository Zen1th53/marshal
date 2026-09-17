package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/project"
)

func (h *CommandHandler) handleOpenCode(ctx context.Context, args []string, line string) (string, error) {
	interactive := h.ws.terminal != nil && h.ws.terminal.IsTerminal()
	if len(args) == 0 && interactive {
		return h.ws.runNativeAgent(ctx, "opencode", nil)
	}
	if len(args) == 0 || strings.EqualFold(args[0], "help") || strings.EqualFold(args[0], "status") {
		return openCodeHelp(ctx)
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "new", "open", "tui", "interactive", "chat":
		return h.ws.runNativeAgent(ctx, "opencode", nil)
	case "continue":
		return h.ws.runNativeAgent(ctx, "opencode", []string{"--continue"})
	case "resume":
		if len(args) == 1 || args[1] == "--last" {
			return h.ws.runNativeAgent(ctx, "opencode", []string{"--continue"})
		}
		return h.ws.runNativeAgent(ctx, "opencode", append([]string{"--session", args[1]}, args[2:]...))
	case "fork":
		if len(args) == 1 || args[1] == "--last" {
			return h.ws.runNativeAgent(ctx, "opencode", []string{"--continue", "--fork"})
		}
		argv := []string{"--session", args[1], "--fork"}
		return h.ws.runNativeAgent(ctx, "opencode", append(argv, args[2:]...))
	case "cli":
		argv, err := openCodeCLIArgs(line)
		if err != nil {
			return "", err
		}
		return h.ws.runNativeAgent(ctx, "opencode", argv)
	case "mcp", "providers", "auth", "agent", "models", "stats", "session", "debug", "github", "pr", "attach", "acp", "serve", "web", "run":
		argv, err := nativeArgs(strings.TrimSpace(line[len(strings.Fields(line)[0]):]))
		if err != nil {
			return "", err
		}
		return h.ws.runNativeAgent(ctx, "opencode", argv)
	default:
		prompt := strings.TrimSpace(line[len(strings.Fields(line)[0]):])
		return h.ws.runNativeAgent(ctx, "opencode", []string{"--prompt", prompt})
	}
}

func openCodeCLIArgs(line string) ([]string, error) {
	parsed, err := nativeArgs(line)
	if err != nil {
		return nil, err
	}
	if len(parsed) <= 2 {
		return nil, nil
	}
	return parsed[2:], nil
}

func openCodeHelp(ctx context.Context) (string, error) {
	binary, err := project.FindBinary("opencode")
	if err != nil {
		return "OPENCODE NATIVE SESSION:\n  Available: false\n  Install OpenCode and ensure `opencode` is on PATH.", nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, probeErr := exec.CommandContext(probeCtx, binary, "--version").CombinedOutput()
	version := strings.TrimSpace(string(out))
	if probeErr != nil {
		version = "probe failed: " + probeErr.Error()
	}
	return fmt.Sprintf(`OPENCODE NATIVE SESSION:
  Available: true
  Version:   %s

Available subcommands:
  /opencode new                 Start a new native OpenCode TUI
  /opencode continue            Continue the latest session
  /opencode resume [session]    Resume the latest or selected session
  /opencode fork [session]      Fork the latest or selected session
  /opencode cli <args...>       Pass native OpenCode arguments unchanged
  /opencode models [provider]   List OpenCode models
  /opencode auth / providers    Manage provider authentication
  /opencode mcp / agent         Manage MCP servers or agents
  /opencode session / stats     Inspect sessions or usage
  /opencode run <prompt...>     Run OpenCode in batch mode
  /opencode <prompt...>         Open the TUI with an initial prompt

Top-level launch: marshal opencode [native arguments...]`, version), nil
}
