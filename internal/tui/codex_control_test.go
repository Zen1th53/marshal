package tui

import (
	"context"
	"os"
	"path/filepath"
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

func TestCodexHomeWithUserConfigIsBlockedInTUIStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("sandbox_mode = 'danger-full-access'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, reason := codexHomeBoundaryStatus(); status != "BLOCKED" || reason != "USER_CONFIG_PRESENT" {
		t.Fatalf("configured Codex home status = %s/%s", status, reason)
	}
	if err := os.Remove(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if status, reason := codexHomeBoundaryStatus(); status != "CONFIG_FREE" || reason != "CONFIG_FREE_VERIFIED" {
		t.Fatalf("config-free Codex home status = %s/%s", status, reason)
	}
}
