package theme

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

var updateBoardThemeGolden = flag.Bool("update-board-theme-golden", false, "rewrite testdata/*.golden")

// mockBoard paints a sample Board layout with state indicators and glyphs
// across panes to verify color vs monochrome rendering and escape invariants.
func mockBoard(l layout.Layout, th *Theme) []string {
	lines := make([]string, l.Rows)
	for i := range lines {
		lines[i] = strings.Repeat(" ", l.Cols)
	}

	for _, p := range l.Panes {
		var content string
		switch p.ID {
		case layout.Inbox:
			content = th.Colorize(th.StateColor(StateWaiting), th.Glyphs.HalfDot+" INBOX "+StateWord(StateWaiting))
		case layout.Tasks:
			content = th.Colorize(th.StateColor(StateWorking), th.Glyphs.BrailleSpinner[0]+" TASK-01 "+StateWord(StateWorking))
		case layout.Lanes:
			content = th.Colorize(th.StateColor(StateVerifying), th.Glyphs.Check+" LANE-01 "+StateWord(StateVerifying))
		case layout.Activity:
			content = th.Colorize(th.StateColor(StateParked), th.Glyphs.WIP+" PARKED "+StateWord(StateParked))
		default:
			content = string(p.ID)
		}

		if p.Rect.Y < len(lines) {
			lines[p.Rect.Y] = content
		}
	}
	return lines
}

func checkGolden(t *testing.T, name string, frame []string) {
	t.Helper()
	var b strings.Builder
	for _, line := range frame {
		b.WriteString(line + "|\n")
	}
	path := filepath.Join("testdata", name+".golden")
	if *updateBoardThemeGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-board-theme-golden to create it)", err)
	}
	if b.String() != string(want) {
		t.Errorf("%s differs from %s:\n%s", name, path, b.String())
	}
}

func TestNoColorFrameContainsNoEscapeCodes(t *testing.T) {
	l := layout.Compute(100, 30, layout.Home)
	monoTh := New(true, false)

	frame := mockBoard(l, monoTh)
	for rowIdx, line := range frame {
		if strings.Contains(line, "\x1b") {
			t.Fatalf("row %d of NO_COLOR frame contains escape code: %q", rowIdx, line)
		}
	}
}

func TestColorVsMonochromeGoldenFrames(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cols, rows int
	}{
		{"board-080x24", 80, 24},
		{"board-100x30", 100, 30},
	} {
		l := layout.Compute(tc.cols, tc.rows, layout.Home)
		colorTh := New(false, true)
		monoTh := New(true, false)

		colorFrame := mockBoard(l, colorTh)
		monoFrame := mockBoard(l, monoTh)

		// Golden comparisons
		checkGolden(t, tc.name+"-color", colorFrame)
		checkGolden(t, tc.name+"-mono", monoFrame)

		// Invariants
		for rowIdx, line := range monoFrame {
			if strings.Contains(line, "\x1b") {
				t.Fatalf("%s mono row %d contains escape code: %q", tc.name, rowIdx, line)
			}
		}

		hasEscapes := false
		for _, line := range colorFrame {
			if strings.Contains(line, "\x1b") {
				hasEscapes = true
				break
			}
		}
		if !hasEscapes {
			t.Fatalf("%s color frame expected to contain escape codes", tc.name)
		}
	}
}
