package tui

import (
	"strings"
	"testing"
)

// Every banner renders with the subtitle and the provider name, and only the
// banner and subtitle are written: no briefing text leaks in.
func TestMarshalSplashShowsAWordmark(t *testing.T) {
	th := NewTheme(ThemeDefault, true, false)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		var b strings.Builder
		playMarshalSplash(&b, th, false)
		out := b.String()
		if !strings.Contains(out, "Summoning the Marshal") {
			t.Fatalf("splash lacks its subtitle:\n%s", out)
		}
		for _, provider := range []string{"Codex", "Claude", "OpenCode", "Antigravity", "codex", "claude"} {
			if strings.Contains(out, provider) {
				t.Fatalf("splash named the underlying provider %q:\n%s", provider, out)
			}
		}
		seen[strings.Split(out, "Summoning")[0]] = true
	}
	if len(seen) < 2 {
		t.Fatalf("the splash did not rotate between wordmarks: %d seen", len(seen))
	}
}

// A terminal without Unicode is only ever shown ASCII wordmarks, so no box or
// block glyph reaches it.
func TestMarshalSplashAsciiOnlyWithoutUnicode(t *testing.T) {
	th := NewTheme(ThemeNoColor, false, false)
	for i := 0; i < 200; i++ {
		var b strings.Builder
		playMarshalSplash(&b, th, false)
		out := b.String()
		if strings.ContainsAny(out, "█╔╗╝╚═║╦╩╠╣") {
			t.Fatalf("a Unicode wordmark reached an ASCII terminal:\n%s", out)
		}
		if strings.Contains(out, "\x1b[") {
			t.Fatalf("no-color splash emitted escape codes:\n%q", out)
		}
	}
}

// A nil theme must not panic.
func TestMarshalSplashNilThemeIsSafe(t *testing.T) {
	var b strings.Builder
	playMarshalSplash(&b, nil, true)
}

// TestMarshalSplashRender prints a few wordmarks so a person can eyeball them
// with `go test -run TestMarshalSplashRender -v`. It always passes.
func TestMarshalSplashRender(t *testing.T) {
	th := NewTheme(ThemeDefault, true, false)
	for i := 0; i < len(marshalBanners); i++ {
		var b strings.Builder
		playMarshalSplash(&b, th, false)
		t.Logf("\n%s", b.String())
	}
}
