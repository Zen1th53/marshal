//go:build linux

package tui

import (
	"context"
	"testing"
)

// TestPollBoardFocusFollowsActivePane: the Board learns from tmux whether its
// pane is the selected one, and only while the chat is docked; an unreadable
// answer leaves what it knew.
func TestPollBoardFocusFollowsActivePane(t *testing.T) {
	f := newDockFake(t, 160)
	w, _ := dockWorkspace(t)
	ctx := context.Background()

	w.pollBoardFocus(ctx)
	if w.dock.chatKeys.Load() {
		t.Fatal("chat marked as holding the keys while nothing is docked")
	}
	w.dock.docked.Store(true)
	f.write("active", "0")
	w.pollBoardFocus(ctx)
	if !w.dock.chatKeys.Load() {
		t.Fatal("Board pane is inactive but the chat is not marked as holding the keys")
	}
	f.write("active", "garbled")
	w.pollBoardFocus(ctx)
	if !w.dock.chatKeys.Load() {
		t.Fatal("an unreadable answer changed the focus")
	}
	f.write("active", "1")
	w.pollBoardFocus(ctx)
	if w.dock.chatKeys.Load() {
		t.Fatal("Board pane is active but the chat is still marked as holding the keys")
	}
	f.write("active", "0")
	w.pollBoardFocus(ctx)
	w.dock.docked.Store(false)
	w.pollBoardFocus(ctx)
	if w.dock.chatKeys.Load() {
		t.Fatal("focus cue kept after undock")
	}
}
