package tui

import (
	"fmt"
	"strings"
)

// integrationCommand parses the wrappers' argv without losing quoted IDs or
// names. Native CLI pass-through commands retain their vendor-owned grammar.
func integrationCommand(cmd string) bool {
	return oneOf(cmd, "/codex", "/claude", "/opencode", "/agy", "/antigravity", "/mcp", "/plugin", "/plugins", "/skill", "/skills", "/apply", "/sessions", "/resume", "/fork", "/review")
}

func governedAgentUsage(provider string, args []string, interactive bool) string {
	if len(args) == 0 {
		return ""
	}
	if hasHelpFlag(args) {
		return ""
	}
	destructive, _ := isDestructiveNativeSubcommand(provider, args)
	if destructive {
		args, _ = extractConfirmation(args)
	}
	sub := strings.ToLower(args[0])
	if provider == "claude" && !oneOf(sub, "status", "info", "health", "help", "doctor", "models", "model", "select", "sessions", "runs", "history", "run", "dispatch") {
		return ""
	}
	max := -1
	tail := ""
	switch sub {
	case "status", "info", "health", "help", "sessions", "runs", "history", "skills", "diff", "models":
		max = 1
	case "doctor", "agents", "login", "logout":
		if !interactive {
			max = 1
		}
	case "review":
		if !interactive && len(args) > 1 {
			return "Review instructions are unavailable in governed mode. Open marshal tui in a terminal and use /review <instructions> for native Codex review."
		}
	case "model", "select":
		max, tail = 2, " [slug]"
	case "run", "dispatch":
		max, tail = 3, " <task_id> [model]"
	case "sandbox":
		max, tail = 2, " [read-only|workspace-write]"
	case "approval":
		max, tail = 2, " [on-request|never]"
	case "search":
		max, tail = 2, " [on|off]"
	case "apply":
		max, tail = 2, " [task_id]"
	case "new", "continue":
		if provider == "codex" {
			max = 1
		}
	case "skill":
		if len(args) != 3 || !strings.EqualFold(args[1], "install") || args[2] == "" {
			return "Usage: /codex skill install <skill_name>"
		}
	case "mcp", "plugin", "plugins", "features":
		if interactive {
			return ""
		}
		if len(args) == 1 {
			return ""
		}
		nested := strings.ToLower(args[1])
		switch nested {
		case "list":
			max = 2
		case "get", "remove", "rm", "delete", "uninstall", "enable", "disable":
			max = 3
			tail = " " + nested + " <name>"
		case "add", "install":
			if sub != "mcp" {
				max = 3
				tail = " " + nested + " <name>"
			}
		}
		if nested == "list" {
			tail = " list"
		}
	}
	if max >= 0 && (len(args) > max || (destructive && max == 3 && len(args) < 3)) {
		return fmt.Sprintf("Usage: /%s %s%s", provider, sub, tail)
	}
	return ""
}

func nativeSessionUsage(provider string, args []string) string {
	if len(args) == 0 {
		return ""
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "new", "open", "tui", "interactive", "chat", "continue", "status", "help":
		if provider == "agy" && sub == "tui" {
			return ""
		}
		if len(args) > 1 {
			return fmt.Sprintf("Usage: /%s %s", provider, sub)
		}
	case "resume", "fork":
		// Session targets must be IDs or the explicit latest-session selector.
		// Native options after that target remain vendor-owned.
		if provider == "agy" && sub == "fork" {
			return ""
		}
		if len(args) >= 2 && (args[1] == "" || (strings.HasPrefix(args[1], "-") && args[1] != "--last")) {
			return fmt.Sprintf("Usage: /%s %s [id|--last]; use /%s cli for native arguments", provider, sub, provider)
		}
	}
	return ""
}

func agentPrompt(line string) string {
	tail := strings.TrimSpace(line[len(strings.Fields(line)[0]):])
	if tail == "" {
		return ""
	}
	return strings.TrimSpace(tail[len(strings.Fields(tail)[0]):])
}
