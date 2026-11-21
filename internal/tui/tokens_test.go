package tui

import (
	"testing"
)

// Every provider has a one-cell glyph and a two-letter ASCII form, worker
// ids and store names resolve to their provider, the tmux names round-trip,
// and the two palette maps agree with "parked" gone from both.
func TestProviderMarks(t *testing.T) {
	for _, p := range []string{"claude", "codex", "agy", "opencode"} {
		if g := providerGlyph(p, true); VisibleLen(g) != 1 {
			t.Errorf("%s glyph %q width %d, want 1", p, g, VisibleLen(g))
		}
		if a := providerGlyph(p, false); len(a) != 2 {
			t.Errorf("%s ascii form %q, want two letters", p, a)
		}
	}
	for s, want := range map[string]string{
		"codex-2": "codex", "claude-code": "claude", "antigravity": "agy", "—": "",
	} {
		if got := providerOf(s); got != want {
			t.Errorf("providerOf(%q) = %q, want %q", s, got, want)
		}
	}
	if got := tmuxColour("accent"); got != "colour111" {
		t.Errorf("tmuxColour(accent) = %q, want colour111", got)
	}
	for k := range boardHex {
		if _, ok := board256[k]; !ok {
			t.Errorf("%q in boardHex but not board256", k)
		}
	}
	for k := range board256 {
		if _, ok := boardHex[k]; !ok {
			t.Errorf("%q in board256 but not boardHex", k)
		}
	}
	if _, ok := boardHex["parked"]; ok {
		t.Error("parked still in boardHex")
	}
	if _, ok := board256["parked"]; ok {
		t.Error("parked still in board256")
	}
}
