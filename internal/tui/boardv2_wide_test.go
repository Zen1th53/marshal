//go:build linux

package tui

import "testing"

// On a wide terminal with a run's tasks the layout docks an inspector, and
// the first task is always selected. That used to paint the classic Board
// with the docked inspector before Board v2 was considered; v2 now draws
// wherever it fits.
func TestWideBoardWithTasksDrawsV2(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	text := frameText(w, 240, 50)
	if w.boardUI.task == "" {
		t.Fatal("no task selected: the docked route was not exercised")
	}
	// paintV2 records the screen it drew; any other paint records "".
	if w.v2CardShown() == "" {
		t.Fatalf("Board v2 not drawn at 240x50:\n%s", text)
	}
}
