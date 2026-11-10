package tui

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter/codex"
)

func TestCodexModelSelectionAndRefusal(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()

	// 1. Discovered default
	def, err := auth.SelectedCodexModel(ctx)
	if err != nil || def != "gpt-5.6-terra" {
		t.Fatalf("selected model = %q, want gpt-5.6-terra, err = %v", def, err)
	}

	// 2. Select eligible model
	if _, err := auth.CodexSelectModel(ctx, "gpt-6-astra", 0); err != nil {
		t.Fatalf("failed to select valid model: %v", err)
	}
	cur, err := auth.SelectedCodexModel(ctx)
	if err != nil || cur != "gpt-6-astra" {
		t.Fatalf("selected model = %q, want gpt-6-astra, err = %v", cur, err)
	}

	// 3. Reject dangerous flag in model selection
	if err := codex.ValidateDangerousFlags([]string{"--dangerously-bypass-approvals-and-sandbox"}); err == nil {
		t.Fatal("expected dangerous flag check to fail")
	}
}

func TestCodexAppServerHostConfigOverrideIsBlockedInTUIStatus(t *testing.T) {
	t.Setenv("MARSHAL_CODEX_APP_SERVER_ALLOW_HOST_CONFIG", "1")
	status, reason := codexAppServerBoundaryStatus()
	if status != "BLOCKED" || reason != "HOST_CONFIG_OVERRIDE" {
		t.Fatalf("override boundary status = %s/%s", status, reason)
	}
}
