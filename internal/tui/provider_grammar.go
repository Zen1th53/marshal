package tui

import "strings"

// providerSubcommands is shared by command parsing and completion. Vendor-owned
// syntax belongs behind cli; these names describe MARSHAL's command surface.
var providerSubcommands = map[string][]string{
	"codex":    []string{"status", "info", "health", "help", "doctor", "models", "model", "select", "review", "sessions", "runs", "history", "mcp", "plugin", "plugins", "skills", "apply", "diff", "resume", "fork", "agents", "features", "sandbox", "approval", "search", "login", "logout", "skill", "run", "dispatch", "exec", "cli", "interactive", "chat", "open", "tui", "new", "continue"},
	"claude":   []string{"new", "continue", "resume", "fork", "cli", "status", "models", "model", "doctor", "sessions", "exec", "run", "mcp", "plugin", "auth", "agents", "login", "logout", "info", "health", "help", "select", "runs", "history", "dispatch", "interactive", "chat", "open", "tui"},
	"opencode": []string{"sessions", "runs", "history", "new", "continue", "resume", "fork", "cli", "status", "models", "providers", "auth", "mcp", "agent", "session", "stats", "run", "debug", "help", "open", "tui", "interactive", "chat", "github", "pr", "attach", "acp", "serve", "web"},
	"agy":      []string{"sessions", "runs", "history", "new", "continue", "resume", "cli", "status", "models", "agents", "mcp", "plugin", "changelog", "help", "open", "interactive", "chat", "agent", "plugins", "prompt"},
}

// parseProviderCommand gates work before provider discovery or control calls.
// A quote at the start of the raw tail explicitly selects the prompt shortcut.
func parseProviderCommand(provider, line string, args []string) (prompt, rejection string) {
	if len(args) == 0 {
		return "", ""
	}
	root := strings.ToLower(strings.Fields(line)[0])
	// Shared management aliases (/mcp, /review, /doctor, etc.) already
	// selected their operation; their first quoted value is not a prompt.
	if !oneOf(root, "/codex", "/claude", "/opencode", "/agy", "/antigravity") {
		return "", ""
	}
	tail := strings.TrimSpace(line[len(strings.Fields(line)[0]):])
	if strings.HasPrefix(tail, "\"") || strings.HasPrefix(tail, "'") {
		words, err := nativeArgs(tail)
		if err != nil {
			return "", "Nothing was run. Invalid quoted prompt: " + err.Error()
		}
		if len(words) == 0 || strings.TrimSpace(strings.Join(words, " ")) == "" {
			return "", "Nothing was run. A prompt must contain text."
		}
		return strings.Join(words, " "), ""
	}
	if provider == "antigravity" {
		provider = "agy"
	}
	word := strings.ToLower(args[0])
	for _, sub := range providerSubcommands[provider] {
		if word == sub {
			return "", ""
		}
	}
	closest := commandTypoSuggestion(word, providerSubcommands[provider], false)
	verb := "exec"
	if provider == "opencode" {
		verb = "run"
	}
	if provider == "agy" {
		verb = "prompt"
	}
	hint := "To send a prompt use " + root + " " + verb + " <text> (or quote the prompt)."
	if closest != "" {
		return "", "Nothing was run. Did you mean " + root + " " + closest + "?\n" + hint
	}
	return "", "Unknown subcommand. " + hint
}
