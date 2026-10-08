package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
)

func (h *CommandHandler) handleOpenCode(ctx context.Context, args []string, line string) (string, error) {
	if len(args) > 0 && strings.EqualFold(args[0], "dispatch") {
		return h.ws.startOpenCodeDispatch(ctx, args)
	}

	prompt, rejection := parseProviderCommand("opencode", line, args)
	if rejection != "" {
		return rejection, nil
	}
	if prompt != "" {
		return h.ws.runNativeAgent(ctx, "opencode", []string{"--prompt", prompt})
	}

	argv, err := openCodeCLIArgs(line)
	if err != nil {
		return "", err
	}
	if len(args) > 0 && strings.EqualFold(args[0], "run") {
		argv = append([]string{"run"}, argv...)
	}
	if len(argv) > 0 && strings.EqualFold(argv[0], "run") && (len(argv) == 1 || strings.TrimSpace(strings.Join(argv[1:], " ")) == "") {
		return "Usage: /opencode run <text>", nil
	}
	if usage := nativeSessionUsage("opencode", args); usage != "" {
		return usage, nil
	}
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "sessions", "runs", "history":
			if len(args) != 1 {
				return "Usage: /opencode sessions", nil
			}
			return h.handleSessionInventory(ctx, "opencode")
		case "resume", "fork", "continue":
			return h.handleNativeSelection(ctx, "opencode", args)
		}
	}
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
		argv, err := openCodeCLIArgs(line)
		if err != nil {
			return "", err
		}
		return h.ws.runNativeAgent(ctx, "opencode", argv)
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
		return "Unknown subcommand. To send a prompt use /opencode run <text>", nil
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
	d := app.ObserveProviderDialect(ctx, "opencode")
	available := d.Binary != ""
	return fmt.Sprintf("OPENCODE NATIVE SESSION:\n  Available: %t\n%s", available, d.Help(providerHelpOperations("opencode"), true)), nil
}
