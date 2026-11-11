package tui

import (
	"context"
	"testing"
)

func TestDockTitleStartupLayout(t *testing.T) {
	f := newDockFake(t, 120)
	w, chat := dockWorkspace(t)
	// The startup layout has already joined the chat, before startDock runs.
	w.dock.enabled = false
	chat.isJoined = true
	chat.provider, chat.runID = "codex", "R-startup"
	f.pane(chat.paneID, "@0", 59)
	w.setMarshalPanel(&MarshalPanel{RunID: "R-current", State: "reviewing"})
	ctx := context.Background()
	if err := w.reconcileStartupDock(ctx); err != nil {
		t.Fatal(err)
	}
	log := f.take()
	mustContain(t, log,
		"set-option -p -t %7 @marshal_dock_title "+w.dockTitle(chat, 59),
		"set-option -w -t %0 pane-border-format "+dockBorderFormat,
		"set-option -p -t %0 @marshal_board_title "+boardDockTitle,
		"set-option -w -t %0 pane-border-status top",
	)
	mustNotContain(t, log, "join-pane", "resize-pane", "break-pane")
	if w.dockedChat() != chat.paneID {
		t.Fatal("startup chat was not adopted for cleanup")
	}
	if err := w.reconcileStartupDock(ctx); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, f.take(), "set-option", "bind-key")

	w.endDock()
	mustContain(t, f.take(),
		"break-pane -d -s %7 -n marshal-chat-x",
		"set-option -wu -t %0 pane-border-status",
		"set-option -wu -t %0 pane-border-format",
		"set-option -pu -t %7 @marshal_dock_title",
	)
	if w.dockedChat() != "" || w.dock.title != "" || chat.isJoined {
		t.Fatal("undock retained startup title state")
	}
}

func TestDockTitleStartupLayoutSeparateWindow(t *testing.T) {
	f := newDockFake(t, 120)
	w, chat := dockWorkspace(t)
	w.dock.enabled = false
	f.pane(chat.paneID, "@1", 120)
	if err := w.reconcileStartupDock(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, f.take(), "set-option", "join-pane", "resize-pane", "bind-key")
	if w.dockedChat() != "" {
		t.Fatal("startup adopted a chat in a separate window")
	}
}
