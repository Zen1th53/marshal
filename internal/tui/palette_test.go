package tui

import (
	"strings"
	"testing"
)

func TestCommandPalette(t *testing.T) {
	actions := []PaletteAction{
		{ID: "goal.edit", Category: "GOAL", Title: "Edit Goal", Command: "/goal edit"},
		{ID: "checkpoint.rollback", Category: "SYSTEM", Title: "Rollback Checkpoint", Command: "/rollback"},
		{ID: "route.inspect", Category: "ROUTING", Title: "Inspect Routing", Command: "/route"},
		{ID: "doctor.run", Category: "SYSTEM", Title: "System Diagnostics", Command: "/doctor"},
	}

	palette := NewCommandPalette(NewTheme(ThemeDefault, true, true), actions)
	if palette.IsOpen() {
		t.Fatalf("palette should be closed initially")
	}

	palette.Open()
	if !palette.IsOpen() {
		t.Fatalf("palette should be open after Open()")
	}

	// Filter for "roll"
	for _, r := range "roll" {
		palette.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}

	// Should match "Rollback Checkpoint"
	rendered := palette.Render(80, 24)
	joined := strings.Join(rendered, "\n")
	if !strings.Contains(joined, "Rollback Checkpoint") {
		t.Fatalf("expected 'Rollback Checkpoint' in palette render, got:\n%s", joined)
	}

	// Press Enter to select
	selected, ok := palette.HandleKey(KeyEvent{Type: KeyEnter})
	if !ok || selected == nil || selected.ID != "checkpoint.rollback" {
		t.Fatalf("expected selected checkpoint.rollback, got ok=%v, act=%v", ok, selected)
	}
	if palette.IsOpen() {
		t.Fatalf("expected palette closed after execution")
	}

	// Test Esc to close
	palette.Open()
	palette.HandleKey(KeyEvent{Type: KeyEsc})
	if palette.IsOpen() {
		t.Fatalf("expected palette closed after Esc")
	}
}
