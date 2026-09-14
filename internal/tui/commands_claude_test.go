package tui

import (
	"strings"
	"testing"
)

func TestClaudeSlashCommands_WithoutAuthority(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	out, err := ws.ExecuteCommand(ctx, "/claude")
	if err != nil {
		t.Fatalf("unexpected error executing /claude: %v", err)
	}
	// With no runtime attached the command must say so rather than appear to
	// have inspected a provider it never reached.
	if !strings.Contains(out, "Claude control authority unavailable") {
		t.Fatalf("expected unattached authority message, got: %q", out)
	}
}

func TestClaudeSlashCommands_HelpDocumentation(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	help, err := ws.ExecuteCommand(ctx, "/help")
	if err != nil {
		t.Fatalf("/help returned error: %v", err)
	}
	if !strings.Contains(help, "/claude") {
		t.Fatalf("expected /claude to be documented in /help, got:\n%s", help)
	}
}

func TestClaudeSlashCommands_WithAttachedAuthority(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	source, _ := testControl(t)
	ws.AttachControlSource(source)

	for _, cmd := range []string{"/claude", "/claude status", "/claude info", "/claude health"} {
		out, err := ws.ExecuteCommand(ctx, cmd)
		if err != nil {
			t.Fatalf("%s returned error: %v", cmd, err)
		}
		for _, want := range []string{
			"CLAUDE GOVERNED CONTROL PLANE:",
			"Status:",
			"Active Model:",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s output missing %q:\n%s", cmd, want, out)
			}
		}
	}
}

func TestClaudeSlashCommands_ModelsAndSelection(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	source, _ := testControl(t)
	ws.AttachControlSource(source)

	out, err := ws.ExecuteCommand(ctx, "/claude models")
	if err != nil {
		t.Fatalf("/claude models returned error: %v", err)
	}
	if !strings.Contains(out, "CLAUDE MODELS") {
		t.Fatalf("/claude models output unexpected:\n%s", out)
	}

	// An unknown subcommand with no further words must not be silently treated
	// as a prompt; that would start a billed session from a typo.
	out, err = ws.ExecuteCommand(ctx, "/claude notasubcommand")
	if err != nil {
		t.Fatalf("/claude notasubcommand returned error: %v", err)
	}
	if !strings.Contains(out, "Unknown Claude subcommand") {
		t.Fatalf("expected unknown-subcommand refusal, got:\n%s", out)
	}
}
