package tui

import (
	"strings"
	"testing"
)

func TestProviderConfigInspectsAndNamesTheHarness(t *testing.T) {
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	ws := NewWorkspace(nil, "proj", "sess")
	ws.workDir = t.TempDir()
	h := &CommandHandler{ws: ws}
	ctx := t.Context()

	out, _ := h.Handle(ctx, "/provider config anthropic")
	if !strings.Contains(out, "Harness claude (provider anthropic is reached through the claude harness)") ||
		!strings.Contains(out, "changes no configuration") || !strings.Contains(out, "/model select") {
		t.Fatalf("anthropic: %q", out)
	}
	out, _ = h.Handle(ctx, "/provider config openai")
	if !strings.Contains(out, "Harness codex (provider openai") {
		t.Fatalf("openai: %q", out)
	}
	out, _ = h.Handle(ctx, "/provider config codex sk-secret-value")
	if !strings.Contains(out, "Refusing to accept a credential") || strings.Contains(out, "sk-secret-value") {
		t.Fatalf("credential: %q", out)
	}
	out, _ = h.Handle(ctx, "/provider config mistral")
	if !strings.Contains(out, "Unknown harness or provider") || !strings.Contains(out, "Known harnesses") {
		t.Fatalf("unknown: %q", out)
	}
}
