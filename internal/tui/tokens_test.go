package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
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

// Orange (needs, 256-colour 215) marks only "the owner must act": an owner
// wait shows it; an ordinary parked task row uses amber (waiting, 179).
func TestNeedsColourOnlyForOwner(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("BOARD_TRUECOLOR", "")
	th := NewTheme(ThemeDefault, true, false)

	wait := boardVisual{theme: th, guard: &guardAlert{}}
	if chip := wait.stateChip("run"); !strings.Contains(chip, "\x1b[38;5;215m") {
		t.Errorf("owner-wait chip lacks the needs colour: %q", chip)
	}

	b := model.Board{Tasks: []model.TaskRow{{ID: "T-1", State: model.TaskParked, Worker: "codex"}}}
	v := boardVisual{board: b, now: time.Now(), theme: th, frame: anim.Frame{}}
	lines := v.tasks(layout.Pane{ID: layout.Tasks, Rect: layout.Rect{W: 120, H: 8}})
	var row string
	for _, l := range lines {
		if strings.Contains(StripANSI(l), "T-1") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("no row for T-1 in:\n%s", StripANSI(strings.Join(lines, "\n")))
	}
	if strings.Contains(row, "\x1b[38;5;215m") {
		t.Errorf("parked row uses the needs colour: %q", row)
	}
	if !strings.Contains(row, "38;5;179") {
		t.Errorf("parked row lacks the waiting colour: %q", row)
	}
}
