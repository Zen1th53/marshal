package tui

import (
	"context"
	"strings"
	"testing"
	"time"
)

// seedMarshalCandidates replaces the candidate seam with a fixed list for the
// test and restores it afterwards.
func seedMarshalCandidates(t *testing.T, cands []pickerCand) {
	t.Helper()
	old := boardMarshalCandidates
	boardMarshalCandidates = func(*Workspace, context.Context) []pickerCand { return cands }
	t.Cleanup(func() { boardMarshalCandidates = old })
}

// recordBoardRunCommand captures the switch command the picker and the queue
// drain issue, instead of running it.
func recordBoardRunCommand(t *testing.T) *[]string {
	t.Helper()
	var recorded []string
	old := boardRunCommand
	boardRunCommand = func(_ *Workspace, _ context.Context, cmd string) { recorded = append(recorded, cmd) }
	t.Cleanup(func() { boardRunCommand = old })
	return &recorded
}

func pickerLastOutput(w *Workspace) string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state.LastOutput
}

func TestMarshalPickerRowsAndKeys(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	w.marshalSession().provider = "codex"
	out := time.Date(time.Now().Year(), 1, 1, 15, 40, 0, 0, time.Local)
	seedMarshalCandidates(t, []pickerCand{
		{provider: "codex", left: 42, known: true, busy: true},
		{provider: "claude", known: false},
		{provider: "opencode", out: true, until: out},
	})
	ctx := t.Context()

	if !w.handleShortcut(ctx, KeyEvent{Type: KeyRune, Rune: 'm'}) {
		t.Fatal("m did not open the Marshal picker")
	}
	if !w.marshalPickerOpen() {
		t.Fatal("the Marshal picker is not open")
	}
	frame := plainFrame(w, 120, 40)
	for _, want := range []string{"WHO PLANS?", "Codex", "now", "Claude Code", "limit unknown", "OpenCode", "out of limit until 15:40"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("picker frame lacks %q:\n%s", want, frame)
		}
	}

	// ↓ moves the cursor, Esc keeps the current Marshal and leaves it unchanged.
	w.handleMarshalPickerKey(ctx, KeyEvent{Type: KeyDown})
	w.handleMarshalPickerKey(ctx, KeyEvent{Type: KeyEsc})
	if w.marshalPickerOpen() {
		t.Fatal("Esc did not close the picker")
	}
	if got := w.marshalSession().provider; got != "codex" {
		t.Fatalf("Esc changed the Marshal to %q", got)
	}
	if got := pickerLastOutput(w); got != "The Marshal stays Codex." {
		t.Fatalf("Esc answered %q", got)
	}

	// Choosing the out-of-limit provider answers the out-of-limit text.
	if !w.handleShortcut(ctx, KeyEvent{Type: KeyRune, Rune: 'm'}) {
		t.Fatal("m did not reopen the picker")
	}
	w.handleMarshalPickerKey(ctx, KeyEvent{Type: KeyRune, Rune: '3'})
	if got := pickerLastOutput(w); got != "OpenCode is out of limit until 15:40." {
		t.Fatalf("out-of-limit answer %q", got)
	}
}

func TestMarshalPickerQueuesWhileBusy(t *testing.T) {
	marshalSwitchEnvironment(t)
	w, _ := boardWorkspace(t)
	defer w.Close()
	recorded := recordBoardRunCommand(t)
	seedMarshalCandidates(t, []pickerCand{
		{provider: "codex", left: 50, known: true},
		{provider: "claude", left: 90, known: true},
	})
	m := w.marshalSession()
	m.provider = "codex"
	m.busy = true
	ctx := t.Context()

	if !w.handleShortcut(ctx, KeyEvent{Type: KeyRune, Rune: 'm'}) {
		t.Fatal("m did not open the picker")
	}
	// Choose claude (the second row) while a step is running: it is queued.
	w.handleMarshalPickerKey(ctx, KeyEvent{Type: KeyRune, Rune: '2'})
	if got := m.nextProvider; got != "claude" {
		t.Fatalf("queued provider %q, want claude", got)
	}
	if got := pickerLastOutput(w); got != "The Marshal is in the middle of a step. Claude Code takes over when it finishes." {
		t.Fatalf("queue answer %q", got)
	}
	if len(*recorded) != 0 {
		t.Fatalf("a switch ran while busy: %v", *recorded)
	}

	// When the step finishes the queued switch runs.
	m.finish(nil)
	if len(*recorded) != 1 || (*recorded)[0] != "/marshal model claude" {
		t.Fatalf("drained commands %v", *recorded)
	}
}

// TestMarshalQueuedSwitchWaitsForRunningCommand: when the Marshal step ends
// while an ordinary command still runs, the queued switch is kept (the busy
// command lane would refuse it) and runs once that command finishes.
func TestMarshalQueuedSwitchWaitsForRunningCommand(t *testing.T) {
	marshalSwitchEnvironment(t)
	w, _ := boardWorkspace(t)
	defer w.Close()
	recorded := recordBoardRunCommand(t)
	m := w.marshalSession()
	m.provider = "codex"
	m.nextProvider = "claude"
	ctx := t.Context()

	release := make(chan struct{})
	w.runCommandFunc(ctx, "slow", func(context.Context) (string, error) { <-release; return "done", nil })
	w.applyQueuedMarshal(ctx)
	if len(*recorded) != 0 {
		t.Fatalf("switch ran while a command was still running: %v", *recorded)
	}
	m.mu.Lock()
	queued := m.nextProvider
	m.mu.Unlock()
	if queued != "claude" {
		t.Fatalf("queued provider after a refused drain = %q, want claude", queued)
	}
	close(release)
	w.commandWG.Wait()
	if len(*recorded) != 1 || (*recorded)[0] != "/marshal model claude" {
		t.Fatalf("after the command finished, drained %v", *recorded)
	}
}

func TestMarshalPickerClickApplies(t *testing.T) {
	marshalSwitchEnvironment(t)
	w, _ := boardWorkspace(t)
	defer w.Close()
	recorded := recordBoardRunCommand(t)
	seedMarshalCandidates(t, []pickerCand{
		{provider: "codex", left: 50, known: true},
		{provider: "claude", left: 90, known: true},
	})
	w.marshalSession().provider = "codex"
	ctx := t.Context()

	if !w.handleShortcut(ctx, KeyEvent{Type: KeyRune, Rune: 'm'}) {
		t.Fatal("m did not open the picker")
	}
	// Paint the frame so the picker's click regions are recorded, then click
	// the claude row.
	w.frameLines(120, 40)
	var claude boardHit
	for _, h := range w.boardHits.hits {
		if h.kind == hitPicker && h.id == "claude" {
			claude = h
		}
	}
	if claude.kind == "" {
		t.Fatalf("no click region for the claude row: %+v", w.boardHits.hits)
	}
	if !w.handleBoardMouse(ctx, KeyEvent{Type: KeyMouse, MouseRow: claude.row + 1, MouseColumn: claude.x0 + 1}) {
		t.Fatal("the click was not taken")
	}
	if len(*recorded) != 1 || (*recorded)[0] != "/marshal model claude" {
		t.Fatalf("after the click: %v", *recorded)
	}

	// The digit 2 applies the second row the same way.
	if !w.handleShortcut(ctx, KeyEvent{Type: KeyRune, Rune: 'm'}) {
		t.Fatal("m did not reopen the picker")
	}
	w.handleMarshalPickerKey(ctx, KeyEvent{Type: KeyRune, Rune: '2'})
	if len(*recorded) != 2 || (*recorded)[1] != "/marshal model claude" {
		t.Fatalf("after the digit: %v", *recorded)
	}
}

func TestBoardF11FocusesChat(t *testing.T) {
	w, _, _ := boardWithNativeChat(t)
	ctx := t.Context()
	if w.nativeChatFocused() {
		t.Fatal("the native chat starts focused")
	}
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyF11})
	if !w.nativeChatFocused() {
		t.Fatal("F11 on the Board did not focus the native chat")
	}
}
