package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// TestPadCellMeasuresVisibleWidth proves the padding helper ignores ANSI escape
// bytes. fmt's %-Ns counts them as characters, which is what silently removed
// the padding from coloured cells and shifted every column to their right.
func TestPadCellMeasuresVisibleWidth(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)

	for _, name := range []string{"claude", "codex", "opencode", "antigravity"} {
		cell := PadCell(th.Colorize(th.Bold, name), 12)
		if got := VisibleLen(cell); got != 12 {
			t.Errorf("PadCell(%q, 12) has visible width %d, want 12", name, got)
		}
	}

	// A plain value must pad identically to a coloured one of the same text.
	plain := PadCell("claude", 12)
	coloured := PadCell(th.Colorize(th.Bold, "claude"), 12)
	if VisibleLen(plain) != VisibleLen(coloured) {
		t.Errorf("colour changed the padded width: plain=%d coloured=%d",
			VisibleLen(plain), VisibleLen(coloured))
	}
}

// TestPadCellTruncatesWithoutBreakingEscapes proves an over-long coloured value
// is cut by visible width and never sliced through an escape sequence, which
// would leak a colour across the rest of the screen.
func TestPadCellTruncatesWithoutBreakingEscapes(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)

	long := th.Colorize(th.Bold, "an-extremely-long-agent-identifier")
	cell := PadCell(long, 12)

	if got := VisibleLen(cell); got != 12 {
		t.Fatalf("truncated cell has visible width %d, want 12", got)
	}
	if strings.Count(cell, "\x1b[") > 0 && !strings.HasSuffix(cell, "\x1b[0m") {
		t.Errorf("truncated cell leaves colour open: %q", cell)
	}
	// A partial escape would leave a bare ESC with no terminating 'm'.
	for i, r := range cell {
		if r == '\x1b' && !strings.ContainsRune(cell[i:], 'm') {
			t.Errorf("truncation split an escape sequence: %q", cell)
		}
	}
}

// TestRosterColumnsAlign renders the team roster and proves each column starts
// at the same offset on every row, regardless of how long an agent name is or
// whether the row carries colour. This is the defect visible as ragged
// "Role:" / "Harness:" columns in the dashboard.
func TestRosterColumnsAlign(t *testing.T) {
	state := UIState{
		ProjectID:   "PROJECT-local",
		SessionID:   "sess-align",
		SessionMode: "ULTRA",
		Participants: []model.Participant{
			{AgentID: "claude", Role: model.RoleArchitect, Harness: "claude", Model: "UNKNOWN", IsActive: true},
			{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex", Model: "UNKNOWN", IsActive: true},
			{AgentID: "opencode", Role: model.RoleQA, Harness: "opencode", Model: "UNKNOWN", IsActive: true},
			{AgentID: "antigravity", Role: model.RoleAppSec, Harness: "antigravity", Model: "UNAVAILABLE", IsActive: false},
		},
	}

	rendered := RenderScreen(state, 120)

	for _, label := range []string{"Role:", "Harness:", "Model:"} {
		var offsets []int
		var rows []string
		for _, line := range strings.Split(rendered, "\n") {
			plain := StripANSI(line)
			if !strings.Contains(plain, label) {
				continue
			}
			// Only roster rows carry all three labels.
			if !strings.Contains(plain, "Role:") || !strings.Contains(plain, "Harness:") {
				continue
			}
			offsets = append(offsets, strings.Index(plain, label))
			rows = append(rows, plain)
		}

		if len(offsets) < 2 {
			t.Fatalf("expected several roster rows containing %q, found %d", label, len(offsets))
		}
		for i, off := range offsets {
			if off != offsets[0] {
				t.Errorf("column %q is ragged: row 0 starts at %d, row %d starts at %d\n  row0: %s\n  row%d: %s",
					label, offsets[0], i, off, rows[0], i, rows[i])
			}
		}
	}
}

// TestRosterRowsShareRightEdge proves every rendered dashboard line is exactly
// the requested width, so the box border forms a straight column.
func TestRosterRowsShareRightEdge(t *testing.T) {
	state := UIState{
		ProjectID:   "PROJECT-local",
		SessionID:   "sess-edge",
		SessionMode: "ULTRA",
		Participants: []model.Participant{
			{AgentID: "claude", Role: model.RoleArchitect, Harness: "claude", Model: "UNKNOWN", IsActive: true},
			{AgentID: "antigravity", Role: model.RoleAppSec, Harness: "antigravity", Model: "UNAVAILABLE", IsActive: false},
		},
	}

	for _, width := range []int{80, 100, 120, 160} {
		rendered := RenderScreen(state, width)
		for _, line := range strings.Split(rendered, "\n") {
			plain := StripANSI(line)
			if plain == "" {
				continue
			}
			// Only measure framed rows.
			if !strings.HasPrefix(plain, "│") && !strings.HasPrefix(plain, "╭") &&
				!strings.HasPrefix(plain, "╰") && !strings.HasPrefix(plain, "├") {
				continue
			}
			if got := VisibleLen(plain); got != width {
				t.Errorf("width %d: framed line has visible width %d\n  %s", width, got, plain)
			}
		}
	}
}

// TestPaletteRowsShareRightEdge proves selected and unselected palette rows are
// rendered to the same visible width. They previously differed by two columns,
// which showed as a ragged right border in the palette overlay.
func TestPaletteRowsShareRightEdge(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	p := NewCommandPalette(th, GlobalRegistry.ToPaletteActions())
	p.Toggle()

	var widths []int
	var rows []string
	for _, line := range p.Render(120, 24) {
		plain := StripANSI(line)
		if !strings.HasPrefix(plain, "│") {
			continue
		}
		widths = append(widths, VisibleLen(plain))
		rows = append(rows, plain)
	}

	if len(widths) < 3 {
		t.Fatalf("expected several palette rows, got %d", len(widths))
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Errorf("palette row %d has visible width %d, row 0 has %d\n  row0: %s\n  row%d: %s",
				i, w, widths[0], rows[0], i, rows[i])
		}
	}
}
