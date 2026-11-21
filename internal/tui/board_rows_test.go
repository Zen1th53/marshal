package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

func rowsVisual(b model.Board, mode ThemeMode, utf8 bool) boardVisual {
	now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
	return boardVisual{board: b, now: now, theme: NewTheme(mode, utf8, true), frame: anim.Frame{}}
}

func taskLine(t *testing.T, lines []string, id string) string {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(StripANSI(l), id) {
			return l
		}
	}
	t.Fatalf("no row for %s in:\n%s", id, StripANSI(strings.Join(lines, "\n")))
	return ""
}
