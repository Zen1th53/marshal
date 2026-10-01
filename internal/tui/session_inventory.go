package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/project"
)

type sessionInventoryAuthority interface {
	Sessions(context.Context, string) (app.SessionInventory, error)
	ResolveNativeConversation(context.Context, string, string) (app.NativeConversation, error)
	ExecuteNativeCodexConversation(context.Context, string, string, []string) (string, error)
}

func (a *runtimeControlAuthority) Sessions(ctx context.Context, provider string) (app.SessionInventory, error) {
	if a == nil || a.runtime == nil {
		return app.SessionInventory{}, errNoRuntime
	}
	return a.runtime.Sessions(ctx, provider)
}
func (a *runtimeControlAuthority) ResolveNativeConversation(ctx context.Context, provider, target string) (app.NativeConversation, error) {
	if a == nil || a.runtime == nil {
		return app.NativeConversation{}, errNoRuntime
	}
	return a.runtime.ResolveNativeConversation(ctx, provider, target)
}
func (a *runtimeControlAuthority) ExecuteNativeCodexConversation(ctx context.Context, operation, target string, tail []string) (string, error) {
	if a == nil || a.runtime == nil {
		return "", errNoRuntime
	}
	return a.runtime.ExecuteNativeCodexConversation(ctx, operation, target, tail)
}
func (h *CommandHandler) sessionAuthority() sessionInventoryAuthority {
	source := h.ws.controlSource()
	if source == nil {
		return nil
	}
	auth, _ := source.Authority.(sessionInventoryAuthority)
	return auth
}
func (h *CommandHandler) handleSessionInventory(ctx context.Context, provider string) (string, error) {
	auth := h.sessionAuthority()
	if auth == nil {
		return "Session inventory unavailable: no runtime attached to workspace.", nil
	}
	inv, err := auth.Sessions(ctx, provider)
	if err != nil {
		return "Session inventory unavailable: " + err.Error(), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "NATIVE conversations (%d):\n", len(inv.Native))
	for _, c := range inv.Native {
		state := "resumability unverified"
		if c.ProviderListed {
			state = "provider-listed; resume/fork subject to provider dialect"
		}
		fmt.Fprintf(&b, "  %s\n    provider=%s project=%s source=%s; %s\n", c.ID, c.Provider, c.Project, c.Source, state)
	}
	if len(inv.Native) == 0 {
		b.WriteString("  No native conversations recorded for this project.\n")
	}
	fmt.Fprintf(&b, "GOVERNED runs (%d):\n", len(inv.Governed))
	for _, r := range inv.Governed {
		name := inv.GovernedModels[r.ID]
		if name == "" {
			name = "UNKNOWN"
		}
		fmt.Fprintf(&b, "  %s provider=%s source=worker run; session=%s task=%s model=%s status=%s started=%s", r.ID, r.Adapter, r.SessionID, r.TaskID, name, r.Status, r.StartedAt.Format("2006-01-02 15:04:05"))
		if r.EndedAt != nil {
			fmt.Fprintf(&b, " ended=%s", r.EndedAt.Format("2006-01-02 15:04:05"))
		}
		b.WriteByte('\n')
	}
	if len(inv.Governed) == 0 {
		b.WriteString("  No governed runs recorded yet.\n")
	}
	for _, warning := range inv.Warnings {
		fmt.Fprintf(&b, "  %s\n", warning)
	}
	return b.String(), nil
}

// Native selection is canonical; this adapter only builds the vendor argv.
func (h *CommandHandler) handleNativeSelection(ctx context.Context, provider string, args []string) (string, error) {
	interactive := h.ws.terminal != nil && h.ws.terminal.IsTerminal()
	// Preserve the terminal prerequisite and the qualified batch dialect.
	if !interactive && provider != "codex" {
		return "", fmt.Errorf("native %s requires an interactive terminal", provider)
	}
	if !interactive && h.sessionAuthority() == nil {
		return "Native selection authority unavailable: no runtime attached to workspace.", nil
	}
	binaryName := provider
	if provider == "antigravity" {
		binaryName = "agy"
	}
	if _, err := project.FindBinary(binaryName); err != nil {
		return fmt.Sprintf("%v; Install %s and make it available on PATH, then retry", err, strings.Title(provider)), nil
	}
	op := strings.ToLower(args[0])
	target := "--last"
	tail := []string(nil)
	if len(args) > 1 {
		target = args[1]
		tail = args[2:]
	}
	if op == "continue" {
		target = "--last"
		tail = args[1:]
	} else if len(args) > 1 && (target == "" || (strings.HasPrefix(target, "-") && target != "--last")) {
		return fmt.Sprintf("Usage: /%s %s [id|--last]; use /%s cli for native arguments", provider, op, provider), nil
	}
	if provider == "codex" && !interactive {
		checkArgs := append([]string{"exec", op, target}, tail...)
		if op == "continue" {
			checkArgs[1] = "resume"
		}
		if err := app.ObserveProviderDialect(ctx, provider).Check(app.ProviderArgOperation(provider, checkArgs), false); err != nil {
			return "", err
		}
	}
	auth := h.sessionAuthority()
	if auth == nil {
		return "Native selection unavailable: no runtime attached to workspace.", nil
	}
	c, err := auth.ResolveNativeConversation(ctx, provider, target)
	if err != nil {
		return "Native " + op + " refused: " + err.Error(), nil
	}
	var argv []string
	switch provider {
	case "codex":
		if op == "continue" {
			op = "resume"
		}
		argv = append([]string{op, c.SourceID}, tail...)
	case "claude":
		argv = append([]string{"--resume", c.SourceID}, tail...)
		if op == "fork" {
			argv = append(argv, "--fork-session")
		}
	case "opencode":
		argv = []string{"--session", c.SourceID}
		if op == "fork" {
			argv = append(argv, "--fork")
		}
		argv = append(argv, tail...)
	case "antigravity":
		argv = append([]string{"--conversation", c.SourceID}, tail...)
	}
	note := "Resumability unverified: transcript import/history index does not prove provider resume support.\n"
	if c.ProviderListed {
		note = "Provider listing includes this conversation; operation remains subject to provider dialect.\n"
	}
	if interactive {
		out, err := h.ws.runNativeAgent(ctx, provider, argv)
		return note + out, err
	}
	out, err := auth.ExecuteNativeCodexConversation(ctx, op, c.ID, tail)
	return note + out, err
}
