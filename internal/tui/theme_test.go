package tui

import (
	"regexp"
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
	styled := th.Colorize(th.Marshal, "Marshal")
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

func TestUnicodeRuneWidth(t *testing.T) {
	// ASCII
	if VisibleLen("hello") != 5 {
		t.Errorf("expected len 5 for 'hello', got %d", VisibleLen("hello"))
	}
	// CJK (each character occupies 2 columns on terminal)
	if VisibleLen("你好") != 4 {
		t.Errorf("expected len 4 for '你好', got %d", VisibleLen("你好"))
	}
	// Emoji (2 columns)
	if VisibleLen("🚀") != 2 {
		t.Errorf("expected len 2 for '🚀', got %d", VisibleLen("🚀"))
	}
	// Truncate with wide characters
	trunc := Truncate("你好世界", 5)
	if VisibleLen(trunc) > 5 {
		t.Errorf("truncated string %q exceeds width 5: %d", trunc, VisibleLen(trunc))
	}
}

// ansiOracle is the pattern StripANSI and VisibleLen scan for by hand.
var ansiOracle = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// StripANSI and VisibleLen agree with the regexp they replaced on every
// sequence the Board writes and on malformed ones.
func TestStripANSIMatchesTheRegexp(t *testing.T) {
	parts := []string{"", "a", "ab c", "✓", "日本", "⣾", "\x1b", "\x1b[", "\x1b[0m", "\x1b[38;5;111m", "\x1b[48;2;11;14;20m", "\x1b[1m", "\x1b[;m", "\x1b[12", "\x1b[3x", "\x1b[3;", "\x1b]0;t\a", "[0m", "\xff", "é", "\x1b\x1b[0m", "…"}
	var cases []string
	for _, a := range parts {
		for _, b := range parts {
			for _, c := range parts {
				cases = append(cases, a+b+c)
			}
		}
	}
	for _, s := range cases {
		want := ansiOracle.ReplaceAllString(s, "")
		if got := StripANSI(s); got != want {
			t.Fatalf("StripANSI(%q) = %q, want %q", s, got, want)
		}
		w := 0
		for _, r := range want {
			w += RuneWidth(r)
		}
		if got := VisibleLen(s); got != w {
			t.Fatalf("VisibleLen(%q) = %d, want %d", s, got, w)
		}
	}
}
