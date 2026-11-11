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

// Space on the bare Board opens the one palette; a second Space is a space in
// the filter; Ctrl+K closes it; and Ctrl+K while the composer holds text is
// the composer's kill-to-end-of-line, not the palette.
func TestPaletteOpensOnSpaceAndCtrlK(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	ctx := t.Context()

	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: ' '})
	if !w.palette.IsOpen() {
		t.Fatal("Space did not open the palette")
	}
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: ' '})
	if !w.palette.IsOpen() {
		t.Fatal("a second Space closed the palette")
	}
	if q := string(w.palette.query); q != " " {
		t.Fatalf("second Space typed %q, want a single space", q)
	}
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyCtrlK})
	if w.palette.IsOpen() {
		t.Fatal("Ctrl+K did not close the palette")
	}

	// Ctrl+K edits text while the composer holds some.
	w.composer.SetText("abc")
	w.composer.cursor = 1
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyCtrlK})
	if w.palette.IsOpen() {
		t.Fatal("Ctrl+K opened the palette while the composer held text")
	}
	if got := w.composer.Text(); got != "a" {
		t.Fatalf("Ctrl+K in the composer left %q, want %q", got, "a")
	}
}

// The workspace palette is built from the registry each time it opens: Board
// rows carry their availability (disabled with a plain reason), the registry's
// rows follow, and the per-provider Marshal-model rows are left to the Marshal
// picker (m, P5-02).
//
// NOTE: the task's example row (`task.retry` — "stuck task: retry it",
// "Highlight a stuck task first.") is a BOARD keymap row added by P5-07, a
// parallel wave-8 task that is not merged into this branch. This test proves
// the same contract (registry rows + availability + no marshal.model.* rows)
// with rows that exist here.
func TestPaletteListsRegistryWithAvailability(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	if w.hostWatch.Load() != nil {
		t.Fatal("the fixture already has host vitals")
	}

	w.palette.Open()
	defer w.palette.Close()

	find := func(id string) (PaletteAction, bool) {
		for _, a := range w.palette.actions {
			if a.ID == id {
				return a, true
			}
		}
		return PaletteAction{}, false
	}

	// A Board row gates itself: with no host vitals, view.host is disabled
	// and carries its registry Why.
	host, ok := find("view.host")
	if !ok {
		t.Fatal("the palette lacks the view.host row")
	}
	k, _ := boardAction("view.host")
	if want := k.Why(w); !host.Disabled || host.Reason != want {
		t.Fatalf("view.host: disabled=%v reason=%q, want disabled with %q", host.Disabled, host.Reason, want)
	}

	// The registry's rows follow the Board's.
	if _, ok := find("task.retry"); !ok {
		t.Fatal("the palette lacks the registry's task.retry row")
	}

	// The per-provider Marshal-model rows are not listed here.
	for _, a := range w.palette.actions {
		if strings.HasPrefix(a.ID, "marshal.model.") {
			t.Fatalf("the palette lists a Marshal-model row %q", a.ID)
		}
	}
}

// Ctrl+K opens the palette while the native chat is focused (the chat passes it
// on); Space before that is typed into the chat and leaves the palette closed.
func TestPaletteCtrlKFromNativeChat(t *testing.T) {
	w, _, _ := boardWithNativeChat(t)
	ctx := t.Context()
	if !w.handleShortcut(ctx, KeyEvent{Type: KeyRune, Rune: 'm'}) || !w.nativeChatFocused() {
		t.Fatal("m did not focus the native chat")
	}

	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: ' '})
	if w.palette.IsOpen() {
		t.Fatal("Space opened the palette from the focused chat")
	}
	w.chat.mu.Lock()
	typed := w.chat.view.Text()
	w.chat.mu.Unlock()
	if typed != " " {
		t.Fatalf("Space typed %q into the chat, want a single space", typed)
	}

	w.handleTerminalKey(ctx, KeyEvent{Type: KeyCtrlK})
	if !w.palette.IsOpen() {
		t.Fatal("Ctrl+K did not open the palette from the focused chat")
	}
}

// No palette row shows a /command: the right column shows the row's key or
// nothing, never the slash command text (which still filters the row).
func TestPaletteHidesSlashCommandText(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	w.palette.Open()
	defer w.palette.Close()
	// "marshal" surfaces the registry's /marshal row, whose command must not
	// appear in the right column.
	for _, r := range "marshal" {
		w.palette.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	for _, line := range w.palette.Render(120, 40) {
		if strings.Contains(StripANSI(line), "/marshal") {
			t.Fatalf("a palette row shows a /command:\n%s", StripANSI(line))
		}
	}
}
