package tui

import (
	"strings"
	"testing"
)

// graphemeSamples are the clusters the audit proved were being split by
// rune-based editing.
var graphemeSamples = []struct {
	name string
	text string
	want int // user-visible characters
}{
	{"ascii", "hello", 5},
	{"uzbek", "o‘zbekcha", 9},
	{"cjk", "中文测试", 4},
	{"combining", "é", 1},
	{"flag", "🇺🇿", 1},
	{"skintone", "👍🏽", 1},
	{"familyZWJ", "👨‍👩‍👧‍👦", 1},
	{"heartVS", "❤️", 1},
}

// TestGraphemeCountMatchesUserVisibleCharacters proves segmentation groups the
// runes of each cluster into one character.
func TestGraphemeCountMatchesUserVisibleCharacters(t *testing.T) {
	for _, s := range graphemeSamples {
		if got := GraphemeCount(s.text); got != s.want {
			t.Errorf("%s: GraphemeCount(%q) = %d, want %d (runes=%d)",
				s.name, s.text, got, s.want, len([]rune(s.text)))
		}
	}
}

// TestBackspaceRemovesWholeGrapheme is the direct regression for the audit
// finding: one Backspace on a single cluster must empty the buffer, never leave
// a base character, a half flag or a dangling zero-width joiner.
func TestBackspaceRemovesWholeGrapheme(t *testing.T) {
	for _, s := range graphemeSamples {
		if s.want != 1 {
			continue
		}
		c := NewComposer(NewTheme(ThemeDefault, true, true))
		c.SetText(s.text)
		c.HandleKey(KeyEvent{Type: KeyBackspace})

		if got := c.Text(); got != "" {
			t.Errorf("%s: one Backspace on %q left %q (want empty)", s.name, s.text, got)
		}
	}
}

// TestBackspaceLeavesNeighboursIntact proves deletion of a cluster in the middle
// of a line removes exactly that cluster.
func TestBackspaceLeavesNeighboursIntact(t *testing.T) {
	c := NewComposer(NewTheme(ThemeDefault, true, true))
	c.SetText("A👨‍👩‍👧‍👦B")

	// Cursor sits at the end; remove B, then the family, leaving A.
	c.HandleKey(KeyEvent{Type: KeyBackspace})
	if got := c.Text(); got != "A👨‍👩‍👧‍👦" {
		t.Fatalf("after removing B got %q", got)
	}
	c.HandleKey(KeyEvent{Type: KeyBackspace})
	if got := c.Text(); got != "A" {
		t.Fatalf("one Backspace must remove the whole family cluster, got %q", got)
	}
}

// TestArrowsMoveByGrapheme proves Left/Right traverse whole characters, so the
// cursor never rests inside a cluster.
func TestArrowsMoveByGrapheme(t *testing.T) {
	for _, s := range graphemeSamples {
		c := NewComposer(NewTheme(ThemeDefault, true, true))
		c.SetText(s.text)

		// Walking left `want` times must reach the start exactly.
		for i := 0; i < s.want; i++ {
			c.HandleKey(KeyEvent{Type: KeyLeft})
		}
		if c.CursorPos() != 0 {
			t.Errorf("%s: %d Left presses left cursor at %d, want 0", s.name, s.want, c.CursorPos())
		}

		// And walking right the same number returns to the end.
		for i := 0; i < s.want; i++ {
			c.HandleKey(KeyEvent{Type: KeyRight})
		}
		if c.CursorPos() != len([]rune(s.text)) {
			t.Errorf("%s: %d Right presses left cursor at %d, want %d",
				s.name, s.want, c.CursorPos(), len([]rune(s.text)))
		}
	}
}

// TestDeleteRemovesWholeGraphemeForward covers the Delete key.
func TestDeleteRemovesWholeGraphemeForward(t *testing.T) {
	c := NewComposer(NewTheme(ThemeDefault, true, true))
	c.SetText("🇺🇿tail")
	c.SetCursor(0)
	c.HandleKey(KeyEvent{Type: KeyDelete})

	if got := c.Text(); got != "tail" {
		t.Errorf("Delete on a flag left %q, want %q", got, "tail")
	}
}

// TestMiddleInsertionAcrossGraphemes proves inserting between clusters does not
// corrupt either neighbour.
func TestMiddleInsertionAcrossGraphemes(t *testing.T) {
	c := NewComposer(NewTheme(ThemeDefault, true, true))
	c.SetText("👍🏽❤️")

	// Sit between the two clusters and type.
	c.HandleKey(KeyEvent{Type: KeyLeft})
	c.HandleKey(KeyEvent{Type: KeyRune, Rune: 'X'})

	if got := c.Text(); got != "👍🏽X❤️" {
		t.Errorf("middle insertion produced %q, want %q", got, "👍🏽X❤️")
	}
}

// TestSnapToGraphemeBoundaryNeverLandsInsideACluster proves an arbitrary offset
// is always corrected to a legal cursor position.
func TestSnapToGraphemeBoundaryNeverLandsInsideACluster(t *testing.T) {
	runes := []rune("👨‍👩‍👧‍👦abc")
	legal := map[int]bool{}
	for _, b := range GraphemeBoundaries(runes) {
		legal[b] = true
	}

	for i := 0; i <= len(runes); i++ {
		got := SnapToGraphemeBoundary(runes, i)
		if !legal[got] {
			t.Errorf("SnapToGraphemeBoundary(%d) = %d, which is inside a cluster", i, got)
		}
	}
}

// TestDisplayWidthStaysSeparateFromSegmentation guards the distinction the fix
// depends on: a CJK ideograph is one grapheme but two terminal cells.
func TestDisplayWidthStaysSeparateFromSegmentation(t *testing.T) {
	const cjk = "中"
	if GraphemeCount(cjk) != 1 {
		t.Errorf("%q should be one grapheme", cjk)
	}
	if w := VisibleLen(cjk); w < 1 {
		t.Errorf("%q should occupy at least one cell, got %d", cjk, w)
	}

	// A padded cell must still measure to the requested width.
	if got := VisibleLen(PadCell(strings.Repeat(cjk, 3), 12)); got != 12 {
		t.Errorf("PadCell width = %d, want 12", got)
	}
}
