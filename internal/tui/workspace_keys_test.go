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
		if ws.navView.IsOpen() {
			t.Fatalf("key %v opened navigation", event.Type)
		}
		if ws.state.LastOutput != initialOutput {
			t.Fatalf("key %v changed LastOutput to %q", event.Type, ws.state.LastOutput)
		}
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
