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
	"github.com/Zen1th53/marshal/internal/usage"
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
	// pick is the window columns [m] covers in the status title row, and
	// windowID the Board window the click binding was made for.
	pick     [2]int
	windowID string

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
	// The status line's width and title row follow the window too.
	w.kickStatus()
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
	return w.adoptDockLocked(ctx, chat, board, w.providerRoot(), chatGeo.PaneWidth, boardGeo.WindowID, boardGeo.WindowWidth)
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
	return w.adoptDockLocked(ctx, chat, board, root, width, boardGeo.WindowID, boardGeo.WindowWidth)
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
// title; window and windowW are the Board window's id and width. Caller holds
// dock.mu.
func (w *Workspace) adoptDockLocked(ctx context.Context, chat *activeTmuxAgent, board, root string, width int, window string, windowW int) error {
	rebound := false
	if w.dock.pane != chat.paneID || w.dock.board != board {
		rebound = true
		if err := w.bindDockKeys(ctx, chat.paneID, board, root); err != nil {
			return err
		}
		if err := w.bindBoardDockKeys(ctx, board, chat.paneID, root); err != nil {
			return err
		}
		w.dock.pane, w.dock.board, w.dock.table, w.dock.window = chat.paneID, board, dockKeyTable(root), chat.window
	}
	// tmux owns the title row, outside the provider's PTY. The CLI still
	// receives every ordinary key and may repaint its entire own screen.
	if w.statusOwned() {
		// The status line's second row carries the titles, so [m] can be
		// clicked; the click binding follows where [m] lands.
		title, at := w.dockTitleAt(chat, width, true)
		pick := [2]int{-1, -1}
		if at >= 0 {
			pick = [2]int{3 + at, 3 + at + len("[m]")}
		}
		if rebound || pick != w.dock.pick || window != w.dock.windowID {
			if err := w.bindDockClicks(ctx, root, window, chat.paneID, board, width, pick[0], pick[1]); err != nil {
				return err
			}
			w.dock.pick, w.dock.windowID = pick, window
		}
		w.publishDockStatus(&dockStatus{window: window, chat: chat.paneID, board: board, chatW: width, boardW: max(0, windowW-1-width), chatTitle: title})
	} else {
		title := w.dockTitle(chat, width)
		if title != w.dock.title {
			if err := tmux.SetPaneOption(ctx, chat.paneID, "@marshal_dock_title", title); err != nil {
				return err
			}
			w.dock.title = title
		}
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
	w.publishDockStatus(nil)
	w.dock.pane, w.dock.board, w.dock.table, w.dock.window, w.dock.title = "", "", "", "", ""
	w.dock.pick, w.dock.windowID = [2]int{}, ""
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
	opts := [][2]string{
		{"pane-border-format", dockBorderFormat},
		{"pane-border-status", "top"},
	}
	if w.statusOwned() {
		// The titles are on the status line's second row instead.
		opts = nil
	}
	opts = append(opts, [][2]string{
		{"pane-border-style", "fg=" + tmuxColour("faint")},
		{"pane-active-border-style", "fg=" + tmuxColour("accent") + ",bold"},
	}...)
	for _, o := range opts {
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

// dockTitle is the chat pane's title: the Marshal brand, the Marshal model
// (or the provider when the model is not known) with the provider's glyph,
// how close it is to its limit, and [m] to switch. It uses only the snapshots
// already in memory (the last usage reading, the chat's launch arguments and
// the theme); it never opens a file or consults the store on the rendering
// path. width is the chat pane's width in columns; the title then fits it
// less the 3-cell focus marker; width <= 0 skips the fit.
func (w *Workspace) dockTitle(chat *activeTmuxAgent, width int) string {
	title, _ := w.dockTitleAt(chat, width, false)
	return title
}

// dockTitleAt is dockTitle; pad lays it out exactly width-3 cells wide with
// "[m] switch" flush right (the status line's title row), and it also
// returns the cell offset of "[m]" in the title, -1 when it is not shown.
func (w *Workspace) dockTitleAt(chat *activeTmuxAgent, width int, pad bool) (string, int) {
	w.mu.RLock()
	unicode := w.theme == nil || w.theme.Unicode
	w.mu.RUnlock()
	qs, read := w.usageQuotas()
	return dockTitleFor(chat.provider, chat.args, qs, read, unicode, width, pad, time.Now())
}

// dockTitleFor lays the chat's title out. The parts give way in a fixed
// order until it fits: "F11 Board", then the model's long name (its vendor
// prefix goes: "GPT-6.1-Sol" → "6.1-Sol"), the reset, the bar, " switch"
// after [m], the model name (the glyph stays), and last the brand. The
// percent left and [m] are never dropped; if it is still too wide, tmux cuts
// it.
func dockTitleFor(provider string, args []string, qs []usage.Quota, read, unicode bool, width int, pad bool, now time.Time) (string, int) {
	sep := " · "
	if !unicode {
		sep = " . "
	}
	name := dockTitleText(provider)
	if name == "" {
		name = "chat"
	}
	glyph := providerGlyph(name, unicode)
	// The chat provider's limit, when a reading has it: a bar of what is left
	// and the percent left with its reset. A read entry that is not known
	// shows "limit unknown"; before the first reading, or for a provider with
	// no entry, there is no limit part.
	bar, pct, reset := "", "", ""
	modelID := chatModelArg(args)
	if read {
		for _, q := range qs {
			if q.Provider != name {
				continue
			}
			if modelID == "" {
				modelID = q.Model
			}
			if figure, p, known := limitFigure(q, now); known {
				if !unicode {
					figure = strings.ReplaceAll(figure, "·", ".")
				}
				bar = " " + limitBar(p, unicode)
				pct = " " + figure
				for _, cut := range []string{" ·", " ."} {
					if i := strings.Index(figure, cut); i >= 0 {
						pct, reset = " "+figure[:i], figure[i:]
						break
					}
				}
			} else {
				pct = " " + limitUnknown
			}
			break
		}
	}
	long, short := modelDisplayName(modelID)
	if long == "" {
		long, short = name, name
	}
	model := " " + long
	brand := " MARSHAL" + sep
	mkey, mword, tail := "[m]", " switch", "  F11 Board"
	build := func() (string, int) {
		head := brand
		if glyph != "" {
			head += glyph
		} else {
			head = strings.TrimSuffix(head, " ")
		}
		head += model + bar + pct + reset
		right := mkey + mword + tail + " "
		gap := 4
		if pad && width > 0 {
			gap = max(2, width-3-VisibleLen(head)-VisibleLen(right))
		}
		return head + strings.Repeat(" ", gap) + right, VisibleLen(head) + gap
	}
	title, at := build()
	if width <= 0 {
		return title, at
	}
	avail := width - 3
	for _, drop := range []func(){
		func() { tail = "" },
		func() { model = " " + short },
		func() { reset = "" },
		func() { bar = "" },
		func() { mword = "" },
		func() {
			if glyph != "" {
				model = ""
			}
		},
		func() { brand = " " },
	} {
		if VisibleLen(title) <= avail {
			break
		}
		drop()
		title, at = build()
	}
	return title, at
}

// chatModelArg is the model the chat was started with (--model/-m), or "".
func chatModelArg(args []string) string {
	for i, a := range args {
		if a == "--" {
			break
		}
		if v, ok := strings.CutPrefix(a, "--model="); ok {
			return v
		}
		if (a == "--model" || a == "-m") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// modelDisplayName is how a model id reads in the title, long and short:
// "gpt-6.1-sol" is "GPT-6.1-Sol" and "6.1-Sol", "claude-opus-5-5" is
// "Claude Opus 5.5" and "Opus 5.5" (a trailing date stamp goes). Any other
// id is shown as it is. An empty id gives "".
func modelDisplayName(id string) (long, short string) {
	var clean strings.Builder
	for _, r := range id {
		if clean.Len() < 40 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			clean.WriteRune(r)
		}
	}
	id = clean.String()
	if id == "" {
		return "", ""
	}
	title := func(s string) string {
		if s == "" {
			return s
		}
		return strings.ToUpper(s[:1]) + s[1:]
	}
	lower := strings.ToLower(id)
	switch {
	case strings.HasPrefix(lower, "gpt-"):
		parts := strings.Split(id[4:], "-")
		for i := range parts {
			parts[i] = title(parts[i])
		}
		rest := strings.Join(parts, "-")
		return "GPT-" + rest, rest
	case strings.HasPrefix(lower, "claude-"):
		parts := strings.Split(id[7:], "-")
		if n := len(parts); n > 1 && len(parts[n-1]) == 8 && strings.Trim(parts[n-1], "0123456789") == "" {
			parts = parts[:n-1]
		}
		var words, nums []string
		for _, p := range parts {
			if p != "" && strings.Trim(p, "0123456789") == "" {
				nums = append(nums, p)
			} else {
				words = append(words, title(p))
			}
		}
		rest := strings.Join(words, " ")
		if len(nums) > 0 {
			rest = strings.TrimSpace(rest + " " + strings.Join(nums, "."))
		}
		return "Claude " + rest, rest
	}
	return id, id
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
