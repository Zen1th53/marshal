package tui

import (
	"strings"
	"testing"
)

func TestNoNavigationHints(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	h := &CommandHandler{ws: ws}
	if help := h.helpText(); strings.Contains(help, "Ctrl+N") || strings.Contains(help, "/menu") {
		t.Fatalf("help contains navigation hint: %s", help)
	}
	s := ws.GetUIState()
	frame := StripANSI(strings.Join(activitySection(s, NewTheme(ThemeDefault, true, true), 160), "\n"))
	if strings.Contains(frame, "Ctrl+N") || strings.Contains(frame, "[Esc] Navigation") {
		t.Fatalf("activity frame contains navigation hint: %s", frame)
	}
}
