//go:build linux

package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestPaletteGoldens(t *testing.T) {
	actions := append(boardPaletteActions(nil), paletteRegistryActions()...)
	total := len(actions)
	more := total - 8

	cases := []struct {
		name    string
		width   int
		height  int
		unicode bool
		query   string
		wantCol int // 2 or 1 for empty query, 0 for search
	}{
		{"empty-132x40", 132, 40, true, "", 2},
		{"empty-80x24", 80, 24, true, "", 1},
		{"empty-ascii-132x40", 132, 40, false, "", 2},
		{"empty-ascii-80x24", 80, 24, false, "", 1},
		{"search-retry-132x40", 132, 40, true, "retry", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			th := NewTheme(ThemeNoColor, tc.unicode, false)
			cp := NewCommandPalette(th, actions)
			cp.Open()
			for _, r := range tc.query {
				cp.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
			}
			lines := cp.Render(tc.width, tc.height)
			boxWidth, _, _ := cp.Layout(tc.width, tc.height)
			for i, line := range lines {
				if got := VisibleLen(line); got != boxWidth {
					t.Errorf("line %d width = %d, want %d: %q", i, got, boxWidth, line)
				}
				if !tc.unicode {
					for _, r := range line {
						if r > 127 {
							t.Errorf("ASCII line %d contains non-ASCII rune %U: %q", i, r, line)
							break
						}
					}
				}
			}

			joined := strings.Join(lines, "\n")
			wantHead := fmt.Sprintf("ACTIONS  type to search all %d", total)
			if !strings.Contains(joined, wantHead) {
				t.Errorf("missing heading %q:\n%s", wantHead, joined)
			}

			if tc.query == "" {
				// 3 header/input/sep lines + item rows + 1 faint more line + 1 footer.
				wantItemRows := 8
				if tc.wantCol == 2 {
					wantItemRows = 4
				}
				if gotRows := len(lines) - 5; gotRows != wantItemRows {
					t.Errorf("item rows = %d, want %d:\n%s", gotRows, wantItemRows, joined)
				}
				wantMore := fmt.Sprintf("── %d more · type a word, e.g. \"retry\", \"budget\", \"telemetry\"", more)
				if !tc.unicode {
					wantMore = fmt.Sprintf("-- %d more . type a word, e.g. \"retry\", \"budget\", \"telemetry\"", more)
				}
				if !strings.Contains(joined, wantMore) {
					t.Errorf("missing faint more line %q:\n%s", wantMore, joined)
				}
				for _, wantLabel := range []string{
					"say to the Marshal",
					"open the architecture",
					"restart a task",
					"change model or provider",
					"pause everything",
					"stop all workers",
					"limits and workers",
					"help",
				} {
					if !strings.Contains(joined, wantLabel) {
						t.Errorf("missing core label %q:\n%s", wantLabel, joined)
					}
				}
			} else {
				if len(cp.filtered) == 0 || cp.filtered[0].ID != "task.retry" {
					t.Fatalf("search %q top = %+v, want task.retry", tc.query, cp.filtered)
				}
			}

			checkGolden(t, paletteGoldenName+"-"+tc.name, lines)
		})
	}
}

// Typing searches all actions and ranks core actions first on ties.
func TestPaletteSearchRanksCoreFirstOnTies(t *testing.T) {
	actions := append(boardPaletteActions(nil), paletteRegistryActions()...)
	th := NewTheme(ThemeNoColor, true, false)

	pauseID := "run.pause"
	if !editionExtreme {
		pauseID = "goal.pause"
	}

	for _, tc := range []struct {
		query  string
		wantID string
	}{
		{"retry", "task.retry"},
		{"architecture", "architecture.browse"},
		{"help", "help"},
		{"model", "marshal.pick"},
		{"pause", pauseID},
		{"l", "view.providers"},
	} {
		cp := NewCommandPalette(th, actions)
		cp.Open()
		for _, r := range tc.query {
			cp.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
		}
		if len(cp.filtered) == 0 || cp.filtered[0].ID != tc.wantID {
			got := ""
			if len(cp.filtered) > 0 {
				got = cp.filtered[0].ID
			}
			t.Errorf("query %q: first result = %q, want core %q", tc.query, got, tc.wantID)
		}
	}
}
