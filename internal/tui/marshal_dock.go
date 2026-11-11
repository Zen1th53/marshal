package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// marshalDock keeps the Marshal chat pane joined at the left edge of the
// Board's tmux window for as long as the Board is the screen. It is not a
// toggle: m only moves keyboard focus to the chat, F11 (bound in the chat
// pane while it is docked) moves it back, and nothing but exit or
// /view classic returns the chat to its own window.
//
// reconcile is the one place that moves panes. It reads where the chat pane
// is and makes it so: it joins the chat when it is elsewhere (first start, a
// recreated chat window), sets its width when the window was resized, and
// breaks it back out when the dock is no longer wanted.
type marshalDock struct {
	mu      sync.Mutex // serialises reconcile; held across tmux commands
	enabled bool       // the TUI owns a screen and has not begun to exit
	pane    string     // chat pane joined now; "" when undocked
	board   string     // Board pane the chat is joined beside
	table   string     // dock key table bound for pane
	window  string     // chat window name to restore on undock
	title   string     // last title installed in tmux

	docked atomic.Bool // read by paint without taking mu
	kick   chan struct{}
}

// dockWait bounds the tmux work of one reconcile, so a wedged server cannot
// hold the dock's lock (and with it exit) indefinitely.
const dockWait = 5 * time.Second

// startDock enables the dock and runs its reconciler until ctx ends. The
// caller defers endDock.
func (w *Workspace) startDock(ctx context.Context) {
	if !w.isTmuxActive() {
		return
	}
	w.dock.mu.Lock()
	w.dock.enabled = true
	if w.dock.kick == nil {
		w.dock.kick = make(chan struct{}, 1)
	}
	kick := w.dock.kick
	w.dock.mu.Unlock()
	w.startBackground(ctx, func(ctx context.Context) {
		for {
			if err := w.reconcileDock(ctx); err != nil && ctx.Err() == nil {
				w.RecordActivity("Marshal dock: " + err.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-kick:
			}
		}
	})
}

// kickDock asks the reconciler to look again: after a resize, a view change
// or a chat restart. Kicks coalesce.
func (w *Workspace) kickDock() {
	w.dock.mu.Lock()
	kick := w.dock.kick
	w.dock.mu.Unlock()
	if kick == nil {
		return
	}
	select {
	case kick <- struct{}{}:
	default:
	}
}

// endDock disables the dock and returns the chat to its own window, so
// nothing the operator had open is lost when the Board exits.
func (w *Workspace) endDock() {
	w.dock.mu.Lock()
	w.dock.enabled = false
	w.dock.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), dockWait)
	defer cancel()
	if err := w.reconcileDock(ctx); err != nil {
		w.RecordActivity("Marshal dock: " + err.Error())
	}
}

// dockedChat reports the docked chat pane, if any.
func (w *Workspace) dockedChat() string {
	w.dock.mu.Lock()
	defer w.dock.mu.Unlock()
	return w.dock.pane
}

// boardLayoutMode is the layout mode the Board paints with: Split while the
// chat holds the left of its window.
func (w *Workspace) boardLayoutMode() layout.Mode {
	if w.dock.docked.Load() || w.nativeChatShown() {
		return layout.Split
	}
	return layout.Home
}

// dockWidth is the chat's width in a window cols wide. Below the split
// minimum the chat stays docked anyway with the same share, and the Board
// paints its compact form in what is left.
func dockWidth(cols int) int {
	chat, _, _ := layout.SplitWidths(cols)
	return max(chat, 1)
}

// reconcileStartupDock adopts a chat already beside the Board before the
// terminal dock loop starts. It never moves or resizes a startup layout.
func (w *Workspace) reconcileStartupDock(ctx context.Context) error {
	w.dock.mu.Lock()
	defer w.dock.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, dockWait)
	defer cancel()
	w.tmuxMu.Lock()
	board := w.tmuxMarshalPaneID
	chat := copyAgentLocked(w.tmuxActiveWins["marshal-chat"])
	w.tmuxMu.Unlock()
	if chat == nil || !isPaneID(chat.paneID) || !isPaneID(board) || chat.paneID == board {
		return nil
	}
	boardGeo, err := tmux.QueryPaneGeometry(ctx, board)
	if err != nil {
		return err
	}
	chatGeo, err := tmux.QueryPaneGeometry(ctx, chat.paneID)
	if err != nil {
		return err
	}
	if chatGeo.WindowID != boardGeo.WindowID {
		return nil
	}
	return w.adoptDockLocked(ctx, chat, board, w.providerRoot(), chatGeo.PaneWidth)
}

// reconcileDock makes tmux match the dock's intent. It is idempotent: a call
// with nothing to change runs only the two geometry queries.
func (w *Workspace) reconcileDock(ctx context.Context) error {
	w.dock.mu.Lock()
	defer w.dock.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, dockWait)
	defer cancel()

	w.mu.RLock()
	want := w.dock.enabled && !w.boardClassic
	w.mu.RUnlock()
	w.tmuxMu.Lock()
	board := w.tmuxMarshalPaneID
	var chat *activeTmuxAgent
	if a := w.tmuxActiveWins["marshal-chat"]; a != nil {
		chat = copyAgentLocked(a)
	}
	w.tmuxMu.Unlock()
	if chat == nil || !isPaneID(chat.paneID) || !isPaneID(board) {
		want = false
	}
	root := w.providerRoot()

	if !want || (w.dock.pane != "" && w.dock.pane != chat.paneID) {
		// Undock, or forget a docked pane that has been replaced: the
		// replacement is joined below.
		err := w.undockLocked(ctx, root, chat)
		if !want {
			// The dock does not own the session's mouse option now: the
			// reconciler does, so a click still works on an undocked Board.
			w.reconcileBoardMouse(ctx, board)
			return err
		}
		if err != nil {
			return err
		}
	}

	boardGeo, err := tmux.QueryPaneGeometry(ctx, board)
	if err != nil {
		return err
	}
	chatGeo, err := tmux.QueryPaneGeometry(ctx, chat.paneID)
	if err != nil {
		// The chat pane is gone; its monitor restarts it and the restart
		// kicks the dock again.
		w.forgetDockLocked(ctx)
		return nil
	}
	width := dockWidth(boardGeo.WindowWidth)
	if chatGeo.WindowID != boardGeo.WindowID {
		// remain-on-exit was set on the chat's own window; in the Board's
		// window it must be the pane's, so a crash is restarted in place.
		_ = tmux.SetPaneOption(ctx, chat.paneID, "remain-on-exit", "on")
		if err := tmux.DockPane(ctx, chat.paneID, board, width); err != nil {
			return err
		}
	} else if chatGeo.PaneWidth != width {
		if err := tmux.ResizePaneWidth(ctx, chat.paneID, width); err != nil {
			return err
		}
	}
	return w.adoptDockLocked(ctx, chat, board, root, width)
}

// reconcileBoardMouse owns the tmux session's mouse option while the dock is
// not active. The dock turns it on for the pair when a chat is docked
// (styleDockLocked) and off when it undocks (forgetDockLocked); here, with no
// dock, it stays on whenever the Board is the screen inside this MARSHAL
// session so a click still selects and opens, and is unset otherwise — a
// non-Board screen, or the exit that clears w.dock.enabled before this runs.
// Caller holds dock.mu.
func (w *Workspace) reconcileBoardMouse(ctx context.Context, board string) {
	if !isPaneID(board) {
		return
	}
	if w.dock.enabled && w.boardMouseWanted() {
		_, _ = tmux.RunCommand(ctx, "set-option", "-t", board, "mouse", "on")
		return
	}
	_, _ = tmux.RunCommand(ctx, "set-option", "-u", "-t", board, "mouse")
}

// adoptDockLocked installs the same keys and title for every path that puts
// the chat beside the Board. width is the chat pane's width, for fitting the
// title. Caller holds dock.mu.
func (w *Workspace) adoptDockLocked(ctx context.Context, chat *activeTmuxAgent, board, root string, width int) error {
	if w.dock.pane != chat.paneID || w.dock.board != board {
		if err := w.bindDockKeys(ctx, chat.paneID, board, root); err != nil {
			return err
		}
		if err := w.bindBoardDockKeys(ctx, board, chat.paneID, root); err != nil {
			return err
		}
		w.dock.pane, w.dock.board, w.dock.table, w.dock.window = chat.paneID, board, dockKeyTable(root), chat.window
	}
	// tmux owns the border row, outside the provider's PTY. The CLI still
	// receives every ordinary key and may repaint its entire own screen.
	title := w.dockTitle(chat, width)
	if title != w.dock.title {
		if err := tmux.SetPaneOption(ctx, chat.paneID, "@marshal_dock_title", title); err != nil {
			return err
		}
		w.dock.title = title
	}
	if w.dock.pane == chat.paneID && !w.dock.docked.Load() {
		if err := w.styleDockLocked(ctx, board); err != nil {
			return err
		}
	}
	w.tmuxMu.Lock()
	if current := w.tmuxActiveWins[chat.id]; current != nil && current.paneID == chat.paneID {
		current.isJoined = true
	}
	w.tmuxMu.Unlock()
	if !w.dock.docked.Swap(true) {
		w.requestRepaint()
	}
	return nil
}

// undockLocked breaks the docked pane back into its own window, if it is
// still beside the Board, removes the dock's key table and gives the pane
// the ordinary agent key table again. Caller holds dock.mu.
func (w *Workspace) undockLocked(ctx context.Context, root string, chat *activeTmuxAgent) error {
	pane, board := w.dock.pane, w.dock.board
	if pane == "" {
		return nil
	}
	var errs []error
	if paneGeo, err := tmux.QueryPaneGeometry(ctx, pane); err == nil {
		if boardGeo, err := tmux.QueryPaneGeometry(ctx, board); err != nil || boardGeo.WindowID == paneGeo.WindowID {
			if err := tmux.UndockPane(ctx, pane, w.dock.window); err != nil {
				errs = append(errs, err)
			} else {
				// The chat's window is new; its id is what F11 and the
				// exit return compare against.
				windowID := ""
				if g, err := tmux.QueryPaneGeometry(ctx, pane); err == nil {
					windowID = g.WindowID
				}
				w.tmuxMu.Lock()
				for _, a := range w.tmuxActiveWins {
					if a.paneID == pane {
						a.isJoined = false
						if windowID != "" {
							a.windowID = windowID
						}
					}
				}
				w.tmuxMu.Unlock()
			}
		}
		if chat != nil && chat.paneID == pane {
			errs = append(errs, w.bindWorkspaceKeys(ctx, pane, root))
		}
	}
	errs = append(errs, w.forgetDockLocked(ctx))
	if isPaneID(board) {
		_ = tmux.SelectPane(ctx, board)
	}
	return errors.Join(errs...)
}

// forgetDockLocked removes the dock's key binding and marks it undocked.
func (w *Workspace) forgetDockLocked(ctx context.Context) error {
	// Best effort: a missing pane is the reason this path is sometimes called.
	// Clear the Board window too, so later splits do not inherit the title row.
	root := w.providerRoot()
	var errs []error
	if isPaneID(w.dock.board) {
		for _, option := range []string{"pane-border-status", "pane-border-format", "pane-border-style", "pane-active-border-style"} {
			_, _ = tmux.RunCommand(ctx, "set-option", "-wu", "-t", w.dock.board, option)
		}
		_, _ = tmux.RunCommand(ctx, "set-option", "-pu", "-t", w.dock.board, "@marshal_board_title")
		// Mouse focus is the dock's; the session's own setting returns.
		_, _ = tmux.RunCommand(ctx, "set-option", "-u", "-t", w.dock.board, "mouse")
		// The Board pane gets the agent key table back: F11 there is
		// the Board's own key again.
		errs = append(errs, tmux.UnbindKeyTable(ctx, boardDockKeyTable(root)), w.bindWorkspaceKeys(ctx, w.dock.board, root))
	}
	if isPaneID(w.dock.pane) {
		_, _ = tmux.RunCommand(ctx, "set-option", "-pu", "-t", w.dock.pane, "@marshal_dock_title")
	}
	if w.dock.table != "" {
		errs = append(errs, tmux.UnbindKeyTable(ctx, w.dock.table))
	}
	err := errors.Join(errs...)
	w.dock.pane, w.dock.board, w.dock.table, w.dock.window, w.dock.title = "", "", "", "", ""
	if w.dock.docked.Swap(false) {
		w.requestRepaint()
	}
	return err
}

// dockBorderFormat titles both docked panes on tmux's border row: the
// selected pane's title is drawn on the accent colour with a marker, the
// other dimmed, so where the keyboard is shows at a glance from either side.
// The chat's title is @marshal_dock_title, the Board's @marshal_board_title.
var dockBorderFormat = "#{?pane_active,#[fg=" + tmuxColour("ink") + "#,bg=" + tmuxColour("accent") + "#,bold] ▶ ,#[fg=" + tmuxColour("dim") + "#,bg=default#,nobold]   }" +
	"#{?@marshal_dock_title,#{@marshal_dock_title},#{@marshal_board_title}}#[default]"

// boardDockTitle is the Board pane's border title while the chat is docked:
// how to reach the chat, and that typing here reaches it too.
const boardDockTitle = "BOARD  ·  F11 / Alt+← chat  ·  s: say it to the Marshal from here "

// styleDockLocked frames the docked panes: titles on the top border row,
// the selected pane's border in the accent colour, and mouse focus on so a
// click selects either pane. Caller holds dock.mu.
func (w *Workspace) styleDockLocked(ctx context.Context, board string) error {
	for _, o := range [][2]string{
		{"pane-border-format", dockBorderFormat},
		{"pane-border-status", "top"},
		{"pane-border-style", "fg=" + tmuxColour("faint")},
		{"pane-active-border-style", "fg=" + tmuxColour("accent") + ",bold"},
	} {
		if err := tmux.SetWindowOption(ctx, board, o[0], o[1]); err != nil {
			return err
		}
	}
	if err := tmux.SetPaneOption(ctx, board, "@marshal_board_title", boardDockTitle); err != nil {
		return err
	}
	// Session scope: the operator's other sessions keep their own setting.
	// Every MARSHAL key table carries the server's mouse bindings, so a
	// provider CLI that reads the mouse still gets it and the wheel scrolls
	// one that does not.
	_, err := tmux.RunCommand(ctx, "set-option", "-t", board, "mouse", "on")
	return err
}

// dockTitle is the chat pane's border title: the Marshal brand, the chat
// provider's glyph and name, and how close that provider's model is to its
// limit. It uses only the snapshots already in memory (the last usage reading
// and the theme); it never opens a file or consults the store on the
// rendering path. width is the chat pane's width in columns, used to drop the
// limit's less important parts when the pane is narrow; width <= 0 skips the
// fit.
func (w *Workspace) dockTitle(chat *activeTmuxAgent, width int) string {
	w.mu.RLock()
	unicode := w.theme == nil || w.theme.Unicode
	w.mu.RUnlock()
	sep := " · "
	if !unicode {
		sep = " . "
	}
	name := dockTitleText(chat.provider)
	if name == "" {
		name = "chat"
	}
	head := " MARSHAL" + sep + name
	if g := providerGlyph(name, unicode); g != "" {
		head = " MARSHAL" + sep + g + " " + name
	}
	// The chat provider's limit, when a reading has it: a used bar and the
	// percent with its reset. A read entry that is not known shows
	// "unavailable"; before the first reading, or for a provider with no
	// entry, there is no limit part.
	bar, fig := "", ""
	if qs, ok := w.usageQuotas(); ok {
		for _, q := range qs {
			if q.Provider != name {
				continue
			}
			if figure, pct, known := limitFigure(q, time.Now()); known {
				if !unicode {
					figure = strings.ReplaceAll(figure, "·", ".")
				}
				bar, fig = " "+limitBar(pct, unicode), " "+figure
			} else {
				fig = " unavailable"
			}
			break
		}
	}
	tail := "    F11 Board "
	title := head + bar + fig + tail
	if width <= 0 {
		return title
	}
	// Drop the limit's less important parts, in order, until the title fits
	// the pane (less the ` ▶ ` marker). head and tail are never dropped; if it
	// is still too wide, tmux cuts it.
	avail := width - 3
	for _, drop := range []func(){
		func() { bar = "" },
		func() { // the reset: keep the percent, drop " ·4d" / " .4d"
			if i := strings.Index(fig, " ·"); i >= 0 {
				fig = fig[:i]
			} else if i := strings.Index(fig, " ."); i >= 0 {
				fig = fig[:i]
			}
		},
		func() { fig = "" },
	} {
		if VisibleLen(title) <= avail {
			break
		}
		drop()
		title = head + bar + fig + tail
	}
	return title
}

func dockTitleText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= 40 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == ' ' {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// invalidateDockKeys makes the next reconcile bind the dock's keys to pane
// again: a chat restarted in place has had the ordinary agent table bound.
func (w *Workspace) invalidateDockKeys(pane string) {
	w.dock.mu.Lock()
	defer w.dock.mu.Unlock()
	if w.dock.pane == pane {
		w.dock.board = ""
	}
}

// focusMarshalChat moves keyboard focus to the docked chat pane. It docks
// first when the chat is not docked yet.
func (w *Workspace) focusMarshalChat(ctx context.Context) error {
	pane := w.dockedChat()
	if pane == "" {
		if err := w.reconcileDock(ctx); err != nil {
			return err
		}
		if pane = w.dockedChat(); pane == "" {
			return errors.New("No Marshal chat is open; /marshal chat starts it.")
		}
	}
	return tmux.SelectPane(ctx, pane)
}

func isPaneID(s string) bool {
	return len(s) > 1 && s[0] == '%'
}
