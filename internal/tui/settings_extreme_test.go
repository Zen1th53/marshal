//go:build !governed

package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestExtremeSettingsApplyAndFormat(t *testing.T) {
	s := marshal.DefaultSettings()

	formatted := formatExtremeSettings(s)
	if !strings.Contains(formatted, "codex-limit 4") ||
		!strings.Contains(formatted, "claude-limit 2") ||
		!strings.Contains(formatted, "agy-limit 4") ||
		!strings.Contains(formatted, "opencode-limit 4") ||
		!strings.Contains(formatted, "global-cap 12") {
		t.Fatalf("unexpected default formatted extreme settings:\n%s", formatted)
	}

	applied, out, err := applyExtremeSetting(&s, []string{"codex", "6"})
	if err != nil || !applied {
		t.Fatalf("apply codex 6 failed: %v, out: %s", err, out)
	}
	if s.ProviderLimits.Codex != 6 {
		t.Fatalf("expected codex limit 6, got %d", s.ProviderLimits.Codex)
	}

	applied, out, err = applyExtremeSetting(&s, []string{"claude-concurrency", "3"})
	if err != nil || !applied {
		t.Fatalf("apply claude 3 failed: %v, out: %s", err, out)
	}
	if s.ProviderLimits.Claude != 3 {
		t.Fatalf("expected claude limit 3, got %d", s.ProviderLimits.Claude)
	}

	applied, out, err = applyExtremeSetting(&s, []string{"antigravity", "8"})
	if err != nil || !applied {
		t.Fatalf("apply agy 8 failed: %v, out: %s", err, out)
	}
	if s.ProviderLimits.Agy != 8 {
		t.Fatalf("expected agy limit 8, got %d", s.ProviderLimits.Agy)
	}

	applied, out, err = applyExtremeSetting(&s, []string{"opencode-limit", "5"})
	if err != nil || !applied {
		t.Fatalf("apply opencode 5 failed: %v, out: %s", err, out)
	}
	if s.ProviderLimits.OpenCode != 5 {
		t.Fatalf("expected opencode limit 5, got %d", s.ProviderLimits.OpenCode)
	}

	applied, out, err = applyExtremeSetting(&s, []string{"global-cap", "20"})
	if err != nil || !applied {
		t.Fatalf("apply global-cap 20 failed: %v, out: %s", err, out)
	}
	if s.GlobalCap != 20 {
		t.Fatalf("expected global cap 20, got %d", s.GlobalCap)
	}

	formatted = formatExtremeSettings(s)
	if !strings.Contains(formatted, "codex-limit 6") ||
		!strings.Contains(formatted, "claude-limit 3") ||
		!strings.Contains(formatted, "agy-limit 8") ||
		!strings.Contains(formatted, "opencode-limit 5") ||
		!strings.Contains(formatted, "global-cap 20") {
		t.Fatalf("unexpected custom formatted extreme settings:\n%s", formatted)
	}

	// Negative values or missing values should error
	if _, _, err := applyExtremeSetting(&s, []string{"codex"}); err == nil {
		t.Fatal("expected error for missing value")
	}
	if _, _, err := applyExtremeSetting(&s, []string{"codex", "-1"}); err == nil {
		t.Fatal("expected error for negative value")
	}
}
