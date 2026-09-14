package tui

import (
	"os"
	"path/filepath"
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

// A session ID carries no provider marker, so a Codex thread ID is
// indistinguishable from a Claude one by shape alone. /claude resume must not
// forward a Codex ID to the Claude CLI: the operator would get the CLI's own
// "No conversation found" and no hint that they addressed the wrong agent.
func TestClaudeResumeRefusesACodexSessionID(t *testing.T) {
	config, codexHome := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("CODEX_HOME", codexHome)

	// A real Claude session, so the lookup has something to succeed against.
	projects := filepath.Join(config, "projects", "fixture")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	const claudeID = "11111111-2222-3333-4444-555555555555"
	if err := os.WriteFile(filepath.Join(projects, claudeID+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A Codex rollout carrying a different ID, named the way Codex names them.
	const codexID = "01a09de5-bb96-7ca2-a02c-4fe0913be447"
	rollouts := filepath.Join(codexHome, "sessions", "2026", "09", "14")
	if err := os.MkdirAll(rollouts, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rollouts, "rollout-2026-09-14T08-11-13-"+codexID+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if claudeSessionExists(config, "", codexID) {
		t.Fatal("a Codex session ID must not resolve as a Claude session")
	}
	if !claudeSessionExists(config, "", claudeID) {
		t.Fatal("a real Claude session must resolve")
	}
	if !codexSessionExists(codexID) {
		t.Fatal("a real Codex rollout must resolve, so the refusal can name the right agent")
	}
	if codexSessionExists(claudeID) {
		t.Fatal("a Claude session ID must not resolve as a Codex rollout")
	}
}
