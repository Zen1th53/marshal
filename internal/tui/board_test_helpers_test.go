package tui

import (
	"strings"
	"testing"
)

// emptyBoardWorkspace is a workspace with no run whose screen is the Board,
// as the raw terminal makes it.
func emptyBoardWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w := NewWorkspace(nil, "demo", "session")
	w.workDir = t.TempDir()
	w.theme = NewTheme(ThemeNoColor, true, false)
	w.screen = NewScreen(NewTerminal(strings.NewReader(""), &strings.Builder{}))
	w.homeBoard = true
	return w
}

// lines0 is the first line of a frame's text: Board v2's top bar.
func lines0(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	return first
}
