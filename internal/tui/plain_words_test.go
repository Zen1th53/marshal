//go:build linux

package tui

import (
	"strings"
	"testing"
	"time"
)

func activityHas(w *Workspace, needle string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return strings.Contains(w.state.LastOutput, needle)
}

// The first Activity line after the pick is a plain sentence naming the
// provider, never "Marshal: codex.".
func TestPickerActivityNamesTheMarshalInPlainWords(t *testing.T) {
	installPickerStubs(t, "codex", "claude")
	reset := time.Now().Add(3 * time.Hour)
	w := pickerWorkspace(t, quotaLeft("codex", 50, reset), quotaLeft("claude", 94, reset))
	if !w.decideMarshalPicker(t.Context()) || !w.handlePickerKey(KeyEvent{Type: KeyRune, Rune: '1'}) {
		t.Fatal("picker did not take the pick")
	}
	if !activityHas(w, "Codex is the Marshal for this project.") || activityHas(w, "Marshal: codex.") {
		w.mu.RLock()
		defer w.mu.RUnlock()
		t.Fatalf("Activity: %q", w.state.LastOutput)
	}
}

// The footer's "Opening the architecture…" is removed once the page has
// opened or been accepted, but a newer answer is never wiped.
func TestArchitectureOpeningAnswerIsCleared(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.memoryAnswer(architectureOpening, false)
	w.clearAnswer(architectureOpening)
	w.mu.RLock()
	answer, last := w.answer, w.state.LastOutput
	w.mu.RUnlock()
	if answer != "" || last != "" {
		t.Fatalf("opening answer kept: %q %q", answer, last)
	}
	w.memoryAnswer("something newer", false)
	w.clearAnswer(architectureOpening)
	w.mu.RLock()
	answer = w.answer
	w.mu.RUnlock()
	if answer != "something newer" {
		t.Fatalf("a newer answer was wiped: %q", answer)
	}
}
