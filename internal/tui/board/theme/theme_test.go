package theme

import (
	"strings"
	"testing"
)

func TestStateColorsAndMonochrome(t *testing.T) {
	colorTh := New(false, true)
	if colorTh.Working == "" || colorTh.Waiting == "" || colorTh.Parked == "" ||
		colorTh.Failed == "" || colorTh.Done == "" || colorTh.Verifying == "" {
		t.Fatal("expected all six state colors to be set in color theme")
	}

	// Distinct colors for each state
	states := []State{
		StateWorking, StateWaiting, StateParked,
		StateFailed, StateDone, StateVerifying,
	}
	seen := make(map[string]State)
	for _, s := range states {
		c := colorTh.StateColor(s)
		if prev, exists := seen[c]; exists {
			t.Fatalf("states %v and %v share the same color %q", prev, s, c)
		}
		seen[c] = s
	}

	// Invariants: every state is distinguishable without colour
	words := make(map[string]State)
	for _, s := range states {
		w := StateWord(s)
		if prev, exists := words[w]; exists {
			t.Fatalf("state words collide for %v and %v: %q", prev, s, w)
		}
		words[w] = s
	}

	// NO_COLOR / monochrome theme produces no escape codes
	monoTh := New(true, false)
	for _, s := range states {
		if c := monoTh.StateColor(s); c != "" {
			t.Errorf("expected empty state color in monochrome theme for %v, got %q", s, c)
		}
		formatted := monoTh.Colorize(monoTh.StateColor(s), string(s))
		if strings.Contains(formatted, "\x1b") {
			t.Errorf("monochrome Colorize contains escape code: %q", formatted)
		}
	}
	if monoTh.Selection != "" || monoTh.Dim != "" || monoTh.Bold != "" || monoTh.Reset != "" {
		t.Error("expected styling escapes to be empty in monochrome theme")
	}
}

func TestGlyphFallbacks(t *testing.T) {
	unicodeTh := New(false, true)
	asciiTh := New(false, false)

	// Invariant: braille -> |/-\, 🚧 -> [WIP], ● -> *
	if unicodeTh.Glyphs.WIP != "[WIP]" {
		t.Errorf("unicode WIP = %q, want [WIP]", unicodeTh.Glyphs.WIP)
	}
	if asciiTh.Glyphs.WIP != "[WIP]" {
		t.Errorf("ascii WIP = %q, want [WIP]", asciiTh.Glyphs.WIP)
	}

	if unicodeTh.Glyphs.Bullet != "●" {
		t.Errorf("unicode Bullet = %q, want ●", unicodeTh.Glyphs.Bullet)
	}
	if asciiTh.Glyphs.Bullet != "*" {
		t.Errorf("ascii Bullet = %q, want *", asciiTh.Glyphs.Bullet)
	}

	// Braille spinner fallback
	if len(unicodeTh.Glyphs.BrailleSpinner) != 8 {
		t.Errorf("unicode braille spinner has %d frames, want 8", len(unicodeTh.Glyphs.BrailleSpinner))
	}
	if len(asciiTh.Glyphs.BrailleSpinner) != 4 {
		t.Errorf("ascii braille spinner fallback has %d frames, want 4", len(asciiTh.Glyphs.BrailleSpinner))
	}
	wantASCII := []string{"|", "/", "-", "\\"}
	for i, frame := range asciiTh.Glyphs.BrailleSpinner {
		if frame != wantASCII[i] {
			t.Errorf("ascii braille frame %d = %q, want %q", i, frame, wantASCII[i])
		}
	}

	// GlyphFallback function string replacement
	raw := "Status: 🚧 working ● task ⣾"
	fallback := GlyphFallback(raw)
	if strings.Contains(fallback, "🚧") || strings.Contains(fallback, "●") || strings.Contains(fallback, "⣾") {
		t.Errorf("GlyphFallback left unicode glyphs: %q", fallback)
	}
	if !strings.Contains(fallback, "[WIP]") || !strings.Contains(fallback, "*") || !strings.Contains(fallback, "|") {
		t.Errorf("GlyphFallback missing expected fallbacks: %q", fallback)
	}
}
