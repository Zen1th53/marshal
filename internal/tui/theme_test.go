package tui

import (
	"strings"
	"testing"
)

func TestThemeRendering(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	if th.BoxTopLeft != "╭" {
		t.Errorf("expected unicode top left '╭', got %q", th.BoxTopLeft)
	}

	badge := th.RenderBadge("VERIFIED")
	if !strings.Contains(badge, "VERIFIED") || !strings.Contains(badge, "✓") {
		t.Errorf("unexpected badge for VERIFIED: %q", badge)
	}

	// Test NoColor
	thNoColor := NewTheme(ThemeNoColor, false, false)
	if thNoColor.BoxTopLeft != "+" {
		t.Errorf("expected ascii top left '+', got %q", thNoColor.BoxTopLeft)
	}
	colored := thNoColor.Colorize(th.Success, "test")
	if colored != "test" {
		t.Errorf("expected plain test in NoColor mode, got %q", colored)
	}

	// Test VisibleLen and Truncate
	styled := th.Colorize(th.Marshal, "MARSHAL")
	if VisibleLen(styled) != 7 {
		t.Errorf("expected visible len 7, got %d", VisibleLen(styled))
	}

	truncated := Truncate("1234567890", 5)
	if truncated != "1234…" {
		t.Errorf("expected '1234…', got %q", truncated)
	}

	padded := PadRight("abc", 5)
	if len(padded) != 5 || padded != "abc  " {
		t.Errorf("expected 'abc  ', got %q", padded)
	}
}
