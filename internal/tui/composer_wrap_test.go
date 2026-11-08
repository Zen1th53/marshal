package tui

import (
	"strings"
	"testing"
)

func TestComposerRenderWrappedFitsWidthAndTracksCursor(t *testing.T) {
	c := NewComposer(NewTheme(ThemeNoColor, true, false))
	text := "/goal create " + strings.Repeat("word ", 40)
	c.SetText(text)
	const cols = 40
	lines, row, col := c.RenderWrapped(cols)
	if len(lines) < 2 {
		t.Fatalf("long input must wrap, got %d line(s)", len(lines))
	}
	for i, line := range lines {
		if w := VisibleLen(line); w >= cols {
			t.Fatalf("line %d is %d columns wide, terminal width is %d: %q", i, w, cols, line)
		}
	}
	if row != len(lines)-1 {
		t.Fatalf("cursor at end belongs on the last row, got %d of %d", row, len(lines))
	}
	if col <= 0 || col >= cols {
		t.Fatalf("cursor column %d outside the line", col)
	}
	joined := ""
	for _, line := range lines {
		joined += strings.TrimLeft(StripANSI(line), "❯ ")
	}
	if strings.ReplaceAll(joined, " ", "") != strings.ReplaceAll(text, " ", "") {
		t.Fatal("wrapping must not lose or duplicate input")
	}
}
