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

// A bare prompt must not start a provider on its own, and must not silently
// pick one. Spending a session, and choosing whose session it is, belongs to
// the operator; defaulting to a vendor also makes the runtime look like that
// vendor's tool rather than a neutral one.
func TestBarePromptDoesNotAutoStartAProvider(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	out, err := ws.ExecuteCommand(ctx, "build me a rest api")
	if err != nil {
		t.Fatalf("bare prompt returned error: %v", err)
	}
	if !strings.Contains(out, "No agent session is open") {
		t.Fatalf("bare prompt must say no agent is open, got: %q", out)
	}
	for _, leaked := range []string{"CODEX TASK LAUNCHED", "CLAUDE TASK LAUNCHED"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("bare prompt started a provider unasked: %q", out)
		}
	}
	// The refusal must name both providers, not steer toward one.
	for _, want := range []string{"/claude", "/codex"} {
		if !strings.Contains(out, want) {
			t.Fatalf("refusal omits %s, which is not provider neutral: %q", want, out)
		}
	}
}

// The idle workspace header is the first thing an operator reads. It must not
// advertise one vendor's commands as though they were the runtime's own.
func TestQuickCommandsStayProviderNeutral(t *testing.T) {
	lines := activitySection(UIState{}, NewTheme(ThemeNoColor, false, false), 200)
	line := ""
	for _, candidate := range lines {
		if strings.Contains(candidate, "Quick Commands:") {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Fatal("idle activity section no longer renders quick commands")
	}
	if strings.Contains(line, "/codex") && !strings.Contains(line, "/claude") {
		t.Fatalf("quick commands name only Codex: %q", line)
	}
	if !strings.Contains(line, "/claude") {
		t.Fatalf("quick commands omit Claude entirely: %q", line)
	}
}
