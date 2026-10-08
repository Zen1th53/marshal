package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
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
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "sessions", "runs", "history":
			if len(args) != 1 {
				return "Usage: /antigravity sessions", nil
			}
			return h.handleSessionInventory(ctx, "antigravity")
		case "resume", "fork", "continue":
			return h.handleNativeSelection(ctx, "antigravity", args)
		}
	}
	interactive := h.ws.terminal != nil && h.ws.terminal.IsTerminal()
	if len(args) == 0 && interactive {
		return h.ws.runNativeAgent(ctx, "antigravity", nil)
	}
	if len(args) == 0 || strings.EqualFold(args[0], "help") || strings.EqualFold(args[0], "status") || (len(args) == 1 && hasHelpFlag(args)) {
		return antigravityHelp(ctx)
	}

	switch strings.ToLower(args[0]) {
	case "new", "open", "interactive", "chat":
		argv, err := openCodeCLIArgs(line)
		if err != nil {
			return "", err
		}
		return h.ws.runNativeAgent(ctx, "antigravity", argv)
	case "cli":
		argv, err := openCodeCLIArgs(line)
		if err != nil {
			return "", err
		}
		if hasHelpFlag(argv) {
			cleanArgv, refusal, _ := checkNativePassthroughSafety("antigravity", argv)
			if refusal != "" {
				return refusal, nil
			}
			argv = cleanArgv
		}
		return h.ws.runNativeAgent(ctx, "antigravity", argv)
	case "models", "agents", "agent", "mcp", "plugin", "plugins", "changelog":
		argv, err := nativeArgs(strings.TrimSpace(line[len(strings.Fields(line)[0]):]))
		if err != nil {
			return "", err
		}
		sub := strings.ToLower(args[0])
		if sub == "mcp" {
			if len(argv) < 2 {
				return "Usage: /agy mcp <list|add|remove|enable|disable>", nil
			}
			verb := strings.ToLower(argv[1])
			d := app.ObserveProviderDialect(ctx, "agy")
			switch verb {
			case "list":
			case "add", "remove", "enable", "disable":
				if !hasHelpFlag(argv) {
					clean, _ := extractConfirmation(argv[2:])
					if len(clean) == 0 {
						return fmt.Sprintf("Usage: /agy mcp %s <name>", verb), nil
					}
				}
			default:
				return fmt.Sprintf("Nothing was run. agy mcp %s is UNSUPPORTED for %s", verb, d.Version), nil
			}
		} else if sub == "plugin" || sub == "plugins" {
			if len(argv) < 2 {
				return "Usage: /agy plugin <list|install|uninstall|enable|disable|validate|import|link|help>", nil
			}
			verb := strings.ToLower(argv[1])
			d := app.ObserveProviderDialect(ctx, "agy")
			switch verb {
			case "list", "help", "import", "validate":
			case "install":
				if !hasHelpFlag(argv) {
					clean, _ := extractConfirmation(argv[2:])
					if len(clean) == 0 {
						return "Usage: /agy plugin install <target>", nil
					}
				}
			case "uninstall", "enable", "disable":
				if !hasHelpFlag(argv) {
					clean, _ := extractConfirmation(argv[2:])
					if len(clean) == 0 {
						return fmt.Sprintf("Usage: /agy plugin %s <name>", verb), nil
					}
				}
			case "link":
				if !hasHelpFlag(argv) {
					clean, _ := extractConfirmation(argv[2:])
					if len(clean) < 2 {
						return "Usage: /agy plugin link <marketplace> <target>", nil
					}
				}
			default:
				return fmt.Sprintf("Nothing was run. agy plugin %s is UNSUPPORTED for %s", verb, d.Version), nil
			}
		}
		if hasHelpFlag(argv) {
			cleanArgv, refusal, _ := checkNativePassthroughSafety("antigravity", argv)
			if refusal != "" {
				return refusal, nil
			}
			argv = cleanArgv
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
	d := app.ObserveProviderDialect(ctx, "agy")
	available := d.Binary != ""
	return fmt.Sprintf("ANTIGRAVITY NATIVE SESSION:\n  Available: %t\n%s", available, d.Help(providerHelpOperations("agy"), true)), nil
}
