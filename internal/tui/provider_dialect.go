package tui

import (
	"context"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
)

var providerManagementCommands = map[string][]string{
	"mcp":    {"list", "add", "get", "remove", "rm", "delete", "login", "logout", "auth", "debug", "enable", "disable"},
	"plugin": {"list", "add", "install", "remove", "rm", "uninstall", "marketplace", "enable", "disable"},
}

func providerHelpOperations(provider string) []string {
	ops := append([]string(nil), providerSubcommands[provider]...)
	for _, group := range []string{"mcp", "plugin"} {
		for _, op := range providerManagementCommands[group] {
			ops = append(ops, group+" "+op)
		}
	}
	if provider == "codex" {
		ops = append(ops, "fork --last")
	}
	return ops
}

// This adapter only projects the canonical application's dialect record.
// Parsing keeps the complete wrapper grammar so qualification never converts
// an unqualified management command into a prompt or deletes pass-through.
func qualifyProviderCompletions(ctx context.Context, c *CompletionContext, terminal bool) {
	if c.allCommands == nil {
		c.allCommands = append([]string(nil), c.Commands...)
	}
	c.Commands = append([]string(nil), c.allCommands...)
	if c.Descriptions == nil {
		c.Descriptions = map[string]string{}
	}
	for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
		d := app.ObserveProviderDialect(ctx, provider)
		root := "/" + provider
		for _, group := range []string{"mcp", "plugin"} {
			c.Subcommands[root+" "+group] = providerManagementCommands[group]
		}
		if provider == "codex" {
			c.Subcommands[root+" plugins"] = providerManagementCommands["plugin"]
			c.Subcommands[root+" features"] = []string{"list", "enable", "disable"}
			c.Subcommands[root+" approval"] = []string{"on-request", "never"}
			c.Subcommands[root+" sandbox"] = []string{"read-only", "workspace-write"}
			c.Subcommands[root+" search"] = []string{"on", "off", "enable", "disable", "true", "false"}
			c.Subcommands[root+" resume"] = []string{"--last"}
			c.Subcommands[root+" fork"] = []string{"--last"}
		}
		var supported []string
		for _, op := range providerSubcommands[provider] {
			cap := d.WrapperOperation(op, terminal)
			c.Descriptions[root+" "+op] = cap.Label()
			// Unqualified operations stay offered, labelled as such: a CLI
			// version MARSHAL has not qualified must not lose its commands.
			// Only operations shown not to exist are withheld.
			if cap.Status != app.ProviderUnsupported {
				supported = append(supported, op)
			}
		}
		c.Subcommands[root] = supported
		for key, choices := range c.Subcommands {
			if !strings.HasPrefix(key, root+" ") {
				continue
			}
			op := strings.TrimPrefix(key, root+" ")
			if d.WrapperOperation(op, terminal).Status == app.ProviderUnsupported {
				c.Subcommands[key] = nil
				continue
			}
			if op == "mcp" || op == "plugin" || op == "plugins" || op == "fork" {
				var qualified []string
				for _, verb := range choices {
					cap := d.WrapperOperation(op+" "+verb, terminal)
					c.Descriptions[key+" "+verb] = cap.Label()
					if cap.Status != app.ProviderUnsupported {
						qualified = append(qualified, verb)
					}
				}
				c.Subcommands[key] = qualified
			}
		}
		// Native providers also expose nested completion without borrowing
		// Codex's verb grammar.
		if provider != "codex" {
			for _, group := range []string{"mcp", "plugin"} {
				var qualified []string
				for _, verb := range providerManagementCommands[group] {
					cap := d.WrapperOperation(group+" "+verb, terminal)
					c.Descriptions[root+" "+group+" "+verb] = cap.Label()
					if cap.Status == app.ProviderSupported {
						qualified = append(qualified, verb)
					}
				}
				c.Subcommands[root+" "+group] = qualified
			}
		}
		if provider == "codex" {
			for alias, op := range map[string]string{"/mcp": "mcp", "/plugin": "plugin", "/plugins": "plugin", "/fork": "fork", "/review": "review", "/apply": "apply", "/search": "search", "/sandbox": "sandbox"} {
				cap := d.WrapperOperation(op, terminal)
				c.Descriptions[alias] = cap.Label()
				if op == "mcp" || op == "plugin" {
					c.Subcommands[alias] = c.Subcommands[root+" "+op]
				}
				if cap.Status != app.ProviderSupported {
					c.Subcommands[alias] = nil
					for i, command := range c.Commands {
						if command == alias {
							c.Commands = append(c.Commands[:i], c.Commands[i+1:]...)
							break
						}
					}
				}
			}
		}
	}
	c.Subcommands["/antigravity"] = c.Subcommands["/agy"]
	for key, value := range c.Descriptions {
		if strings.HasPrefix(key, "/agy ") {
			c.Descriptions[strings.Replace(key, "/agy ", "/antigravity ", 1)] = value
		}
	}
}

func (h *CommandHandler) qualifiedHelp(ctx context.Context, original string) string {
	dialects := map[string]app.ProviderDialect{}
	for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
		dialects[provider] = app.ObserveProviderDialect(ctx, provider)
	}
	terminal := h.ws.terminal != nil && h.ws.terminal.IsTerminal()
	lines := strings.Split(original, "\n")
	aliases := map[string]string{"/mcp": "mcp", "/plugin": "plugin", "/plugins": "plugin", "/fork": "fork", "/review": "review", "/apply": "apply", "/search": "search", "/sandbox": "sandbox"}
	for i, line := range lines {
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		if op, ok := aliases[words[0]]; ok {
			lines[i] += " [" + dialects["codex"].WrapperOperation(op, terminal).Label() + "]"
		}
		for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
			if words[0] == "/"+provider {
				lines[i] = "  /" + provider + " help  Provider operations and version qualification (UNKNOWN means unqualified pass-through)"
			}
		}
	}
	text := strings.Join(lines, "\n")
	details := "\n  /antigravity is an alias for /agy.\n\n"
	for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
		details += dialects[provider].Help(providerHelpOperations(provider), terminal) + "\n"
	}
	// Keep the keyboard reference at the tail so the default transcript view
	// still shows it after /help, without requiring a scroll past dialects.
	if i := strings.Index(text, "Function Keys & Shortcuts:"); i >= 0 {
		text = text[:i] + details + text[i:]
	} else {
		text += details
	}

	return text
}

func (c *Completer) descriptionsFor(text string, cursor int) map[string]string {
	runes := []rune(text)
	if cursor > len(runes) {
		cursor = len(runes)
	}
	if cursor < 0 {
		cursor = 0
	}
	start := cursor
	for start > 0 && !isWordSeparator(runes[start-1]) {
		start--
	}
	prefix := strings.Join(strings.Fields(string(runes[:start])), " ")
	out := map[string]string{}
	for _, label := range c.activeMatches {
		out[label] = c.ctx.Descriptions[strings.TrimSpace(prefix+" "+label)]
	}
	// The live popup uses Suggest, which doesn't populate activeMatches.
	_, matches := c.Suggest(text, cursor)
	for _, label := range matches {
		out[label] = c.ctx.Descriptions[strings.TrimSpace(prefix+" "+label)]
	}
	return out
}
