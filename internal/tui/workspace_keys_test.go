package tui

import (
	"context"
	"strings"
	"testing"
)

func TestCtrlNAndEscOpenNothing(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess")
	ctx := context.Background()
	initialOutput := ws.state.LastOutput
	for _, event := range []KeyEvent{{Type: KeyCtrlN}, {Type: KeyEsc}} {
		ws.handleTerminalKey(ctx, event)
		if ws.state.LastOutput != initialOutput {
			t.Fatalf("key %v changed LastOutput to %q", event.Type, ws.state.LastOutput)
		}
	}
}

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

func TestMenuCommandIsUnknown(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess")
	got, err := ws.ExecuteCommand(context.Background(), "/menu")
	if err != nil {
		t.Fatalf("ExecuteCommand returned error: %v", err)
	}
	if !strings.Contains(got, "Unknown command") {
		t.Fatalf("/menu response = %q, want Unknown command", got)
	}
}
