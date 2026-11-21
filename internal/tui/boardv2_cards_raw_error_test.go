//go:build linux

package tui

import (
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The stalled run's card offers d only for a recorded technical cause, and d
// shows that cause, never the card's own cause line again (the dead "raw
// error" key on the live rv06 stall). A stop with no such cause has no d.
func TestPausedCardRawErrorIsTheRecordedCause(t *testing.T) {
	_ = recordStopCommands(t)
	stall := marshal.Pause{Reason: "the run stopped making progress", Resolutions: []string{"resolve the escalation or amend the plan"}, Detail: "31 rounds without progress; unfinished tasks: T03 queued", Code: marshal.CodeRunStalled, Action: marshal.PauseResume}
	w := pausedWorkspace(t, "claude", stall)
	z := cardsFrame(w).Paused
	if z == nil || !slices.Contains(keyNames(z.Actions), "d raw error") {
		t.Fatalf("stall card without its raw error: %+v", z)
	}
	if !w.handleV2CardKey(KeyEvent{Type: KeyRune, Rune: 'd'}) || lastOutput(w) != "Raw error: "+stall.Detail {
		t.Fatalf("d: %q", lastOutput(w))
	}
	bare := stall
	bare.Detail = ""
	w = pausedWorkspace(t, "claude", bare)
	if z := cardsFrame(w).Paused; z == nil || slices.Contains(keyNames(z.Actions), "d raw error") {
		t.Fatalf("a stop with no recorded cause offers d: %+v", z)
	}
}
