package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
)

func (h *CommandHandler) handleOpenCode(ctx context.Context, args []string, line string) (string, error) {
	prompt, rejection := parseProviderCommand("opencode", line, args)
	if rejection != "" {
		return rejection, nil
	}
	if prompt != "" {
		return h.ws.runNativeAgent(ctx, "opencode", []string{"--prompt", prompt})
	}

	if usage := nativeSessionUsage("opencode", args); usage != "" {
		return usage, nil
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
		return h.ws.runNativeAgent(ctx, "opencode", nil)
	case "continue":
		return h.ws.runNativeAgent(ctx, "opencode", []string{"--continue"})
	case "resume":
		if len(args) == 1 {
			return h.ws.runNativeAgent(ctx, "opencode", []string{"--continue"})
		}
		if args[1] == "--last" {
			return h.ws.runNativeAgent(ctx, "opencode", append([]string{"--continue"}, args[2:]...))
		}
		return h.ws.runNativeAgent(ctx, "opencode", append([]string{"--session", args[1]}, args[2:]...))
	case "fork":
		if len(args) == 1 {
			return h.ws.runNativeAgent(ctx, "opencode", []string{"--continue", "--fork"})
		}
		if args[1] == "--last" {
			return h.ws.runNativeAgent(ctx, "opencode", append([]string{"--continue", "--fork"}, args[2:]...))
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
