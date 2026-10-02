package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/project"
)

// neutralProviders lists the providers a provider-neutral command can reach,
// in the order they are named to the operator. The order carries no
// preference: no provider is chosen unless the operator chose it or it is the
// only one installed.
var neutralProviders = []string{"codex", "claude", "opencode", "agy"}

var neutralProviderNames = map[string]string{
	"codex": "Codex", "claude": "Claude", "opencode": "OpenCode", "agy": "Antigravity",
}

// neutralOps maps each provider-neutral command onto the provider subcommand
// that performs it. A provider missing from an entry has no such command in
// its qualified CLI.
var neutralOps = map[string]map[string]string{
	"models":   {"codex": "models", "claude": "models", "opencode": "models", "agy": "models"},
	"model":    {"codex": "model", "claude": "model"},
	"mcp":      {"codex": "mcp", "claude": "mcp", "opencode": "mcp", "agy": "mcp"},
	"plugin":   {"codex": "plugin", "claude": "plugin", "agy": "plugin"},
	"login":    {"codex": "login", "claude": "login", "opencode": "auth login"},
	"logout":   {"codex": "logout", "claude": "logout", "opencode": "auth logout"},
	"resume":   {"codex": "resume", "claude": "resume", "opencode": "resume", "agy": "resume"},
	"fork":     {"codex": "fork", "claude": "fork", "opencode": "fork"},
	"review":   {"codex": "review"},
	"apply":    {"codex": "apply"},
	"features": {"codex": "features"},
	"search":   {"codex": "search"},
	"skills":   {"codex": "skills"},
	"skill":    {"codex": "skill"},
}

func canonicalNeutralProvider(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "antigravity" {
		return "agy"
	}
	if _, ok := neutralProviderNames[name]; ok {
		return name
	}
	return ""
}

func defaultProviderPath(root string) string {
	return filepath.Join(root, ".marshal", "default-provider")
}

func loadDefaultProvider(root string) string {
	if root == "" {
		// Without a project directory the path would resolve against the
		// process's working directory, which is not the operator's project.
		return ""
	}
	data, err := os.ReadFile(defaultProviderPath(root))
	if err != nil {
		return ""
	}
	return canonicalNeutralProvider(string(data))
}

func saveDefaultProvider(root, provider string) error {
	if root == "" {
		return fmt.Errorf("no project is open; run MARSHAL in an initialized project (marshal init)")
	}
	path := defaultProviderPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(provider+"\n"), 0o600)
}

// providerRoot is the project directory that holds the default-provider
// choice: the attached runtime's project, otherwise the workspace directory.
func (w *Workspace) providerRoot() string {
	w.mu.RLock()
	root, runtime := w.workDir, w.runtime
	w.mu.RUnlock()
	if runtime != nil {
		root = runtime.ProjectRoot()
	}
	return root
}

// installedNeutralProviders reports which provider CLIs are discoverable. It
// only looks the binaries up; it never runs one, so reading status or the
// default provider cannot start a provider process.
func installedNeutralProviders(ctx context.Context) []string {
	var installed []string
	for _, p := range neutralProviders {
		if ctx.Err() != nil {
			break
		}
		if _, err := project.FindBinary(p); err == nil {
			installed = append(installed, p)
		}
	}
	return installed
}

func neutralProviderList(providers []string) string {
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, neutralProviderNames[p])
	}
	return strings.Join(names, ", ")
}

// defaultProvider returns the provider that provider-neutral commands use:
// the operator's choice, otherwise the only installed provider. When neither
// settles it, the reply explains how to choose and the provider is empty.
func (h *CommandHandler) defaultProvider(ctx context.Context) (string, string) {
	root := h.ws.providerRoot()
	if p := loadDefaultProvider(root); p != "" {
		return p, ""
	}
	installed := installedNeutralProviders(ctx)
	switch len(installed) {
	case 1:
		return installed[0], ""
	case 0:
		return "", "No AI agent was found. Install Codex, Claude Code, OpenCode or Antigravity (agy), then check with /provider status."
	default:
		return "", fmt.Sprintf("Several AI agents are installed (%s). Choose the one these commands use with /provider use <%s>.",
			neutralProviderList(installed), strings.Join(installed, "|"))
	}
}

// handleProviderUse shows or sets the default provider.
func (h *CommandHandler) handleProviderUse(ctx context.Context, args []string) (string, error) {
	root := h.ws.providerRoot()
	if len(args) == 0 {
		if p, note := h.defaultProvider(ctx); p != "" {
			return fmt.Sprintf("Default provider: %s. Change it with /provider use <%s>.", neutralProviderNames[p], strings.Join(neutralProviders, "|")), nil
		} else {
			return note, nil
		}
	}
	if len(args) != 1 {
		return fmt.Sprintf("Usage: /provider use <%s>", strings.Join(neutralProviders, "|")), nil
	}
	p := canonicalNeutralProvider(args[0])
	if p == "" {
		return fmt.Sprintf("Nothing was changed: %q is not a provider. Use one of: %s.", args[0], strings.Join(neutralProviders, ", ")), nil
	}
	if err := saveDefaultProvider(root, p); err != nil {
		return fmt.Sprintf("Default provider was NOT saved: %v", err), nil
	}
	reply := fmt.Sprintf("Default provider: %s. Commands such as /models, /mcp and /resume now use it.", neutralProviderNames[p])
	if _, err := project.FindBinary(p); err != nil {
		reply += fmt.Sprintf("\nNote: %s is not installed or not on PATH yet.", neutralProviderNames[p])
	}
	return reply, nil
}

// handleNeutral runs a provider-neutral command on the default provider, or on
// the provider named as its only argument where the command allows that. A
// command only one provider has goes to that provider. Otherwise a provider
// without the command is never replaced by another one: the reply names the
// providers that have it.
func (h *CommandHandler) handleNeutral(ctx context.Context, op string, args []string, line string) (string, error) {
	ops := neutralOps[op]
	provider := ""
	if (op == "models" || op == "login" || op == "logout") && len(args) == 1 {
		provider = canonicalNeutralProvider(args[0])
		if provider == "" {
			return fmt.Sprintf("Usage: /%s [%s]  (%q is not a provider; nothing was run)", op, strings.Join(neutralProviders, "|"), args[0]), nil
		}
		args = nil
	}
	if provider == "" && len(ops) == 1 {
		// A command only one provider has cannot mean anything else, so it
		// needs no default; help labels it with that provider.
		for p := range ops {
			provider = p
		}
	}
	if provider == "" {
		var note string
		provider, note = h.defaultProvider(ctx)
		if provider == "" && op == "models" && len(args) == 0 {
			if installed := installedNeutralProviders(ctx); len(installed) > 1 {
				return h.modelsForAll(ctx, installed)
			}
		}
		if provider == "" {
			return "Nothing was run. " + note, nil
		}
	}
	sub, ok := ops[provider]
	if !ok {
		return neutralUnsupported(op, provider), nil
	}
	rest := ""
	if len(args) > 0 {
		fields := strings.Fields(line)
		rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
	}
	argv := append(strings.Fields(sub), args...)
	providerLine := strings.TrimSpace("/" + provider + " " + sub + " " + rest)
	switch provider {
	case "codex":
		return h.handleCodex(ctx, argv, providerLine)
	case "claude":
		return h.handleClaude(ctx, argv, providerLine)
	case "opencode":
		return h.handleOpenCode(ctx, argv, providerLine)
	default:
		return h.handleAntigravity(ctx, argv, providerLine)
	}
}

// modelsForAll lists models for every installed provider when no default is
// chosen. Codex and Claude are read in process; OpenCode and Antigravity list
// models through their own CLI, which opens a terminal, so they are named with
// the command that shows them instead of being launched one after another.
func (h *CommandHandler) modelsForAll(ctx context.Context, installed []string) (string, error) {
	var b strings.Builder
	for _, p := range installed {
		switch p {
		case "codex":
			out, err := h.handleCodex(ctx, []string{"models"}, "/codex models")
			if err != nil {
				return "", err
			}
			b.WriteString(out)
		case "claude":
			out, err := h.handleClaude(ctx, []string{"models"}, "/claude models")
			if err != nil {
				return "", err
			}
			b.WriteString(out)
		default:
			fmt.Fprintf(&b, "%s MODELS: use /models %s to list them in its own interface.\n", strings.ToUpper(neutralProviderNames[p]), p)
		}
		b.WriteString("\n")
	}
	b.WriteString("Show one provider with /models <provider>, or set a default with /provider use <name>.")
	return b.String(), nil
}

func neutralUnsupported(op, provider string) string {
	var with []string
	for _, p := range neutralProviders {
		if _, ok := neutralOps[op][p]; ok {
			with = append(with, p)
		}
	}
	reply := fmt.Sprintf("Nothing was run. %s has no /%s command.", neutralProviderNames[provider], op)
	switch {
	case op == "login" && provider == "agy":
		reply += " Sign in to Antigravity through its own app."
	case op == "model":
		reply += fmt.Sprintf(" Choose the model in %s's own settings.", neutralProviderNames[provider])
	}
	if len(with) == 1 {
		return reply + fmt.Sprintf(" It is a %s command: use /%s %s.", neutralProviderNames[with[0]], with[0], neutralOps[op][with[0]])
	}
	return reply + fmt.Sprintf(" It is available for %s; use /<provider> %s, or change the default with /provider use <name>.", neutralProviderList(with), op)
}
