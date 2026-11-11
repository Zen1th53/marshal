package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// The Board's task table has no evidence column: nothing fills it, so the
// header must not offer one at any width.
func TestBoardTasksHaveNoEvidenceColumn(t *testing.T) {
	now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
	b := demoBoard(now)
	th := NewTheme(ThemeNoColor, true, true)
	v := boardVisual{board: b, now: now, theme: th, selectedTask: "T-023", focus: layout.Tasks, edition: "Marshal", health: map[string]string{"codex": "●", "claude": "●", "agy": "◐", "opencode": "●"}}
	for _, tc := range []struct{ w, h int }{{120, 40}, {200, 50}} {
		l := layout.ComputeFor(tc.w, tc.h, layout.Split, v.layoutCounts())
		var header string
		for _, line := range paintBoard(l, th, v.render) {
			plain := StripANSI(line)
			if strings.Contains(plain, "ID") && strings.Contains(plain, "state") && strings.Contains(plain, "scope") {
				header = plain
				break
			}
		}
		if header == "" {
			t.Fatalf("%dx%d: no TASKS header line in the frame", tc.w, tc.h)
		}
		if strings.Contains(header, " ev ") || strings.Contains(header, " ev") {
			t.Errorf("%dx%d: TASKS header still has an ev column: %q", tc.w, tc.h, header)
		}
		if !strings.Contains(header, "sent back") {
			t.Errorf("%dx%d: TASKS header lost the sent back column: %q", tc.w, tc.h, header)
		}
	}
}
