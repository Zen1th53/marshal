package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/inspector"
)

var updateBoardGolden = flag.Bool("update-board-golden", false, "rewrite testdata/board/*.golden")

// placeholder labels a pane with what the layout gave it and fills the rest
// with dots, so a golden frame shows geometry and nothing else.
func placeholder(p layout.Pane) []string {
	out := []string{fmt.Sprintf("%s %s %dx%d", strings.ToUpper(string(p.ID)), p.Density, p.Rect.W, p.Rect.H)}
	for range p.Rect.H - 1 {
		out = append(out, strings.Repeat(".", p.Rect.W))
	}
	return out
}

func overlayPlaceholder(name string, r layout.Rect) []string {
	out := []string{fmt.Sprintf("%s %dx%d", name, r.W, r.H)}
	for range r.H - 1 {
		out = append(out, strings.Repeat("#", r.W))
	}
	return out
}

// checkGolden compares a frame with testdata/board/<name>.golden. Each row
// ends in "|" so the right edge, and any trailing blanks, show in a diff.
func checkGolden(t *testing.T, name string, frame []string) {
	t.Helper()
	var b strings.Builder
	for _, line := range frame {
		b.WriteString(line + "|\n")
	}
	path := filepath.Join("testdata", "board", name+".golden")
	if *updateBoardGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-board-golden to create it)", err)
	}
	if b.String() != string(want) {
		t.Errorf("%s differs from %s:\n%s", name, path, b.String())
	}
}

func TestBoardGoldenFrames(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, false)
	for _, tc := range []struct {
		name       string
		cols, rows int
		mode       layout.Mode
		overlays   int
	}{
		{"home-080x24", 80, 24, layout.Home, 0},
		{"home-100x30", 100, 30, layout.Home, 0},
		{"home-120x40", 120, 40, layout.Home, 0},
		{"home-170x45", 170, 45, layout.Home, 0},
		{"home-220x60", 220, 60, layout.Home, 0},
		{"split-060x40", 60, 40, layout.Split, 0},
		{"overlay-090x30", 90, 30, layout.Home, 1},
		{"overlay-120x40", 120, 40, layout.Home, 2},
	} {
		l := layout.Compute(tc.cols, tc.rows, tc.mode)
		frame := paintBoard(l, th, placeholder)
		for depth := range tc.overlays {
			frame = overlayBoard(frame, l, th, depth, overlayPlaceholder(fmt.Sprintf("OVERLAY %d", depth), l.Overlay(depth)))
		}
		if len(frame) != tc.rows {
			t.Fatalf("%s: %d rows", tc.name, len(frame))
		}
		for y, line := range frame {
			if w := VisibleLen(line); w != tc.cols {
				t.Fatalf("%s row %d: %d cells", tc.name, y, w)
			}
		}
		checkGolden(t, tc.name, frame)
	}
}

// A widget that writes more than its rect, longer or taller, still lands
// only in its rect.
func TestPaintBoardKeepsEachPaneInsideItsRect(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, false)
	l := layout.Compute(170, 45, layout.Home)
	frame := paintBoard(l, th, func(p layout.Pane) []string {
		out := make([]string, p.Rect.H+5)
		for i := range out {
			out[i] = strings.Repeat(string(p.ID[0]), p.Rect.W+10)
		}
		return out
	})
	cells := make([][]rune, len(frame))
	for y, line := range frame {
		cells[y] = []rune(line)
	}
	for _, p := range l.Panes {
		for y := p.Rect.Y; y < p.Rect.Y+p.Rect.H; y++ {
			got := string(cells[y][p.Rect.X : p.Rect.X+p.Rect.W])
			want := strings.Repeat(string(p.ID[0]), p.Rect.W-1) + "…"
			if got != want {
				t.Fatalf("%s row %d: %q", p.ID, y, got)
			}
		}
	}
	for _, r := range l.Rules {
		for y := r.Y; y < r.Y+r.H; y++ {
			if cells[y][r.X] != '│' {
				t.Fatalf("rule at %d,%d: %q", r.X, y, cells[y][r.X])
			}
		}
	}
}

// The overlay dims the body and draws over it; outside its rect every cell of
// the Board is where it was, so opening an overlay never relays the Board.
func TestOverlayDimsTheBoardWithoutMovingIt(t *testing.T) {
	th := NewTheme(ThemeDefault, true, false)
	l := layout.Compute(120, 40, layout.Home)
	board := paintBoard(l, th, func(p layout.Pane) []string {
		return []string{th.Colorize(th.Success, "● "+string(p.ID)+" 漢字")}
	})
	r := l.Overlay(0)
	frame := overlayBoard(board, l, th, 0, []string{th.Colorize(th.Warning, "INSPECTOR")})
	for y := range frame {
		before, after := StripANSI(board[y]), StripANSI(frame[y])
		inBody := y >= l.Body.Y && y < l.Body.Y+l.Body.H
		if !inBody {
			if frame[y] != board[y] {
				t.Fatalf("row %d outside the body changed", y)
			}
			continue
		}
		if !strings.HasPrefix(frame[y], th.Dim) {
			t.Fatalf("row %d is not dimmed: %q", y, frame[y])
		}
		if y < r.Y || y >= r.Y+r.H {
			if after != before {
				t.Fatalf("row %d moved:\n%q\n%q", y, before, after)
			}
			continue
		}
		if cellRange(after, 0, r.X) != cellRange(before, 0, r.X) || cellRange(after, r.X+r.W, l.Cols) != cellRange(before, r.X+r.W, l.Cols) {
			t.Fatalf("row %d moved beside the overlay:\n%q\n%q", y, before, after)
		}
		if VisibleLen(frame[y]) != l.Cols {
			t.Fatalf("row %d: %d cells", y, VisibleLen(frame[y]))
		}
	}
	if !strings.Contains(frame[r.Y], th.Warning+"INSPECTOR") {
		t.Fatalf("the overlay keeps its own colour: %q", frame[r.Y])
	}
}

func TestCellRangeLeavesNoHalfGlyph(t *testing.T) {
	for _, tc := range []struct {
		s        string
		from, to int
		want     string
	}{
		{"abcdef", 2, 4, "cd"},
		{"ab", 1, 5, "b   "},
		{"ab", 4, 6, "  "},
		{"a漢b", 0, 2, "a "},
		{"a漢b", 2, 4, " b"},
		{"a漢b", 1, 3, "漢"},
	} {
		if got := cellRange(tc.s, tc.from, tc.to); got != tc.want {
			t.Errorf("cellRange(%q, %d, %d) = %q, want %q", tc.s, tc.from, tc.to, got, tc.want)
		}
	}
}

func TestParkedTaskInspectorGolden(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, false)
	run := marshal.Run{PlanID: "P-7", Budget: marshal.Budget{Tokens: marshal.Ceiling{Task: 500}}, Tasks: []marshal.Task{{PlanTaskID: "T-8", Title: "Release", Worker: "codex", State: marshal.Escalated, Files: []string{"internal/tui/board"}, Criteria: []string{"checks pass"}}}}
	b := model.Project(model.Snapshot{RunID: "R-3", Run: run}, []events.Event{{ID: "ev-1", Sequence: 1, RunID: "R-3", TaskID: "T-8", Type: events.EventTypeMarshalEscalated, Data: map[string]any{"reason": "operator decision"}}})
	for _, tc := range []struct {
		name       string
		cols, rows int
	}{{"inspector-parked-100x30", 100, 30}, {"inspector-parked-220x40", 220, 40}} {
		l := layout.Compute(tc.cols, tc.rows, layout.Home)
		frame := paintBoardInspector(l, th, inspector.New(b, "T-8"), placeholder, nil)
		checkGolden(t, tc.name, frame)
	}
}

// TestNoColorFrameContainsNoEscapeCodes proves that paintBoard and overlayBoard
// in ThemeNoColor mode emit strictly zero ANSI escape sequences.
func TestNoColorFrameContainsNoEscapeCodes(t *testing.T) {
	th := NewTheme(ThemeNoColor, false, false)
	l := layout.Compute(120, 40, layout.Home)
	frame := paintBoard(l, th, func(p layout.Pane) []string {
		return []string{th.Colorize(th.Success, "● "+string(p.ID))}
	})
	frame = overlayBoard(frame, l, th, 0, []string{th.Colorize(th.Warning, "INSPECTOR")})
	for y, line := range frame {
		if strings.Contains(line, "\x1b") {
			t.Fatalf("row %d in NO_COLOR frame contains escape code: %q", y, line)
		}
	}
}

// TestBoardMonochromeAndColorGoldenFrames verifies that golden frames for the Board
// render without escape codes in monochrome and match line widths in both modes.
func TestBoardMonochromeAndColorGoldenFrames(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cols, rows int
		mode       layout.Mode
	}{
		{"home-080x24", 80, 24, layout.Home},
		{"home-100x30", 100, 30, layout.Home},
	} {
		l := layout.Compute(tc.cols, tc.rows, tc.mode)

		// Monochrome (NO_COLOR, non-UTF-8 ASCII fallback)
		thMono := NewTheme(ThemeNoColor, false, false)
		frameMono := paintBoard(l, thMono, placeholder)
		for y, line := range frameMono {
			if strings.Contains(line, "\x1b") {
				t.Fatalf("%s mono row %d has escapes: %q", tc.name, y, line)
			}
			if w := VisibleLen(line); w != tc.cols {
				t.Fatalf("%s mono row %d: %d cells, want %d", tc.name, y, w, tc.cols)
			}
		}

		// Color
		thColor := NewTheme(ThemeDefault, true, false)
		frameColor := paintBoard(l, thColor, placeholder)
		for y, line := range frameColor {
			if w := VisibleLen(line); w != tc.cols {
				t.Fatalf("%s color row %d: %d cells, want %d", tc.name, y, w, tc.cols)
			}
		}
	}
}
