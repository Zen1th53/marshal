package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/inbox"
)

// boardKey is one key of the Board as the help overlay and the command
// palette show it. boardKeymap is the one table both read, so they cannot
// list different keys; TestBoardKeymapBound presses each against the real
// handlers.
type boardKey struct {
	// Group is the heading the key is listed under.
	Group string
	// Keys is how the key is written, alternatives separated by two
	// spaces: "8  b".
	Keys string
	// What is what the key does.
	What string
	// View names the view the key opens; the palette offers "Open <View>
	// view" for it.
	View string
	// Command is the command the palette runs for the key; Press the key
	// the palette sends instead. A key with neither is listed only.
	Command string
	Press   *KeyEvent
	// Class is what the entry's keys may do (keymap.go); Except gives a
	// key of the entry that differs its own class.
	Class  KeyClass
	Except map[string]KeyClass
	// Listed marks an entry whose Keys describes rather than names a key:
	// it is typed into a confirmation another key opened.
	Listed bool
	// Direct says why a confirm key acts at once instead of opening a
	// confirmation: a key the Board's spec keeps unchanged, or a view key
	// whose confirmation is still owed. TestBoardKeymapClasses pins the
	// list, so a new one cannot slip in.
	Direct string
	// ID is the action's stable name, unique across the table and written
	// lower-case dotted ("view.host", "marshal.say"). The help, the
	// palette, the hint bar and every mouse button name one action by it,
	// so the one registry drives them all and they cannot drift.
	ID string
	// Hint is the one-to-three word label the hint bar shows for the key
	// ("say", "help"); empty keeps the key out of the hint bar.
	Hint string
	// When reports whether the action can run right now; nil means it
	// always can. It reads only in-memory state (atomics, boardUI, the
	// inbox), never disk, tmux or the store, because it runs on the paint
	// path when the palette or the hint bar asks.
	When func(*Workspace) bool
	// Why is one plain sentence for why the action cannot run now, shown on
	// the palette's disabled rows and when a click lands on an off hint. It
	// is required whenever When is set.
	Why func(*Workspace) string
}

func pressRune(r rune) *KeyEvent { return &KeyEvent{Type: KeyRune, Rune: r} }

// reason wraps a fixed sentence as a Why, the common case where the one
// reason an action is off does not depend on more of the workspace.
func reason(s string) func(*Workspace) string { return func(*Workspace) string { return s } }

// boardKeymap is every key the Board binds, in the order help lists them.
var boardKeymap = []boardKey{
	{Group: "VIEWS", Keys: "1  Esc", What: "back to the Board", View: "Board", Press: pressRune('1'), Class: KeyOpen, ID: "view.board", Hint: "board", When: func(w *Workspace) bool { return w.boardVisible() && w.anyBodyView() }, Why: reason("No view is open to leave.")},
	{Group: "VIEWS", Keys: "2  h", What: "host: temperature, disk, guards", View: "Host", Press: pressRune('2'), Class: KeyOpen, ID: "view.host", Hint: "host", When: func(w *Workspace) bool { return w.hostWatch.Load() != nil }, Why: reason("Host vitals are not available in this session.")},
	{Group: "VIEWS", Keys: "3", What: "memory (MCP): review queue", View: "Memory", Press: pressRune('3'), Class: KeyOpen, ID: "view.memory", Hint: "memory", When: func(w *Workspace) bool { return w.memorySrc() != nil }, Why: reason("The memory review queue has nothing to show in this session.")},
	{Group: "VIEWS", Keys: "4", What: "providers: harness health", View: "Providers", Press: pressRune('4'), Class: KeyOpen, ID: "view.providers", Hint: "providers", When: func(w *Workspace) bool { return w.providerSrc() != nil }, Why: reason("Provider health has nothing to show in this session.")},
	{Group: "VIEWS", Keys: "5", What: "verify: claims and their evidence", View: "Verify", Press: pressRune('5'), Class: KeyOpen, ID: "view.verify", Hint: "verify", When: func(w *Workspace) bool { return w.verifySrc() != nil }, Why: reason("There are no claims to verify in this session.")},
	{Group: "VIEWS", Keys: "6", What: securityKeyWhat, View: "Security", Press: pressRune('6'), Class: KeyOpen, ID: "view.security", Hint: "security", When: func(w *Workspace) bool { return w.securitySrc() != nil }, Why: reason("There is no security activity to show in this session.")},
	{Group: "VIEWS", Keys: "7", What: "plan: task graph and plan versions", View: "Plan", Press: pressRune('7'), Class: KeyOpen, ID: "view.plan", Hint: "plan", When: func(w *Workspace) bool { return w.planSrc() != nil }, Why: reason("There is no plan to show in this session.")},
	{Group: "VIEWS", Keys: "8  b", What: "architecture and business process", View: "Architecture", Press: pressRune('8'), Class: KeyOpen, ID: "view.architecture", Hint: "architecture", When: func(w *Workspace) bool { return w.archSrc() != nil }, Why: reason("There is no architecture to show in this session.")},

	{Group: "BOARD", Keys: "j k  ↑ ↓", What: "move in the focused pane, scroll", Class: KeyMove, ID: "board.move", Hint: "move"},
	{Group: "BOARD", Keys: "Enter", What: "inspect a task; a lane: its worker", Class: KeyOpen, ID: "board.inspect", Hint: "inspect", When: func(w *Workspace) bool { return w.boardUI.task != "" || w.boardUI.lane != "" }, Why: reason("Select a task or a lane first.")},
	{Group: "BOARD", Keys: "t  i  l", What: "focus tasks, inbox, lanes", Class: KeyMove, ID: "board.focus"},
	{Group: "BOARD", Keys: "Tab", What: "next pane; Shift+Tab: previous", Class: KeyMove, ID: "board.pane"},
	{Group: "BOARD", Keys: "Esc  q", What: "close the inspector, a view, help", Class: KeyOpen, ID: "board.close", Hint: "esc", When: func(w *Workspace) bool { return w.boardUI.inspect != "" || w.anyBodyView() || w.boardHelp.Load() }, Why: reason("Nothing is open to close.")},
	{Group: "BOARD", Keys: "e", What: "inspector: toggle all events", Class: KeyOpen, ID: "inspector.events", When: func(w *Workspace) bool { return w.boardUI.inspect != "" }, Why: reason("Open a task's inspector first.")},
	{Group: "BOARD", Keys: "Enter  ⌫", What: "inspector: child task, parent task", Class: KeyOpen, ID: "inspector.walk", When: func(w *Workspace) bool { return w.boardUI.inspect != "" }, Why: reason("Open a task's inspector first.")},
	{Group: "BOARD", Keys: "o", What: "inspector: show the worker's pane", Class: KeyOpen, ID: "inspector.pane"},
	{Group: "BOARD", Keys: "d  D", What: "diff; D: the task against base", Command: "/diff", Class: KeyOpen, ID: "task.diff", Hint: "diff"},
	{Group: "BOARD", Keys: "r", What: "stuck task: retry it, Enter", Hint: "retry", Press: pressRune('r'), Class: KeyConfirm, ID: "task.retry", When: func(w *Workspace) bool { _, ok := stuckTaskUnderKeyboard(w); return ok }, Why: reason("Highlight a stuck task first.")},

	{Group: "INBOX", Keys: "y  u  K", What: "reply; changed files: undo, keep", Class: KeyConfirm, Except: map[string]KeyClass{"K": KeyOpen}, ID: "inbox.reply", Hint: "reply", When: func(w *Workspace) bool { return w.boardReplyWaiting() }, Why: reason("Nothing is waiting for your reply.")},
	{Group: "INBOX", Keys: "a  A  d  r", What: "allow once, for the run, deny, return", Class: KeyConfirm, ID: "inbox.decide", When: func(w *Workspace) bool { return len(w.boardInbox()) > 0 }, Why: reason("The inbox has nothing to decide.")},
	{Group: "INBOX", Keys: "i  Enter", What: "show the request's reason", Class: KeyOpen, ID: "inbox.reason", Hint: "inbox", When: func(w *Workspace) bool { return len(w.boardInbox()) > 0 }, Why: reason("The inbox has nothing to show.")},
	{Group: "INBOX", Keys: "Enter", What: "run stopped: do what it suggests", Class: KeyOpen, Listed: true, ID: "run.stopped", When: func(w *Workspace) bool { return w.boardStopWaiting() }, Why: reason("No stopped run is waiting.")},
	{Group: "INBOX", Keys: "x", What: "cancel the decision being typed", Class: KeyOpen, ID: "inbox.cancel", When: func(w *Workspace) bool { return w.inboxView.Confirming() }, Why: reason("No decision is being typed.")},
	{Group: "INBOX", Keys: "hard gate", What: "type the canonical id, then Enter", Class: KeyConfirm, Listed: true, ID: "inbox.hardgate"},

	{Group: "IN A VIEW", Keys: "a  r  i", What: "memory: keep, reject, evidence", Class: KeyConfirm, Except: map[string]KeyClass{"i": KeyOpen}, ID: "memory.decide", When: func(w *Workspace) bool { return w.memView.Load() }, Why: reason("Open the Memory view first.")},
	{Group: "IN A VIEW", Keys: "PgUp PgDn", What: "memory: scroll", Class: KeyMove, ID: "memory.scroll", When: func(w *Workspace) bool { return w.memView.Load() }, Why: reason("Open the Memory view first.")},
	{Group: "IN A VIEW", Keys: "v", What: "providers: verify harness, Enter", Press: pressRune('v'), Class: KeyConfirm, ID: "providers.verify", Hint: "verify", When: func(w *Workspace) bool { return w.provView.Load() }, Why: reason("Open the Providers view first.")},
	{Group: "IN A VIEW", Keys: "r", What: "providers: route around, then Enter", Class: KeyConfirm, ID: "providers.route", When: func(w *Workspace) bool { return w.provView.Load() }, Why: reason("Open the Providers view first.")},
	{Group: "IN A VIEW", Keys: "x", What: "verify: return claims, then Enter", Press: pressRune('x'), Class: KeyConfirm, ID: "verify.return", When: func(w *Workspace) bool { return w.verView.Load() }, Why: reason("Open the Verify view first.")},
	{Group: "IN A VIEW", Keys: "a  d", What: "security: allow, deny, then Enter", Class: KeyConfirm, ID: "security.decide", When: func(w *Workspace) bool { return w.secView.Load() }, Why: reason("Open the Security view first.")},
	{Group: "IN A VIEW", Keys: "Enter", What: "plan: inspect the selected task", Class: KeyOpen, ID: "plan.inspect", When: func(w *Workspace) bool { return w.planView.Load() }, Why: reason("Open the Plan view first.")},
	{Group: "IN A VIEW", Keys: "o", What: "architecture: open in the browser", Press: pressRune('o'), Class: KeyOpen, ID: "arch.open", When: func(w *Workspace) bool { return w.archView.Load() }, Why: reason("Open the Architecture view first.")},

	{Group: "MARSHAL", Keys: "m  F11", What: "chat; F11 or Alt+arrow switch", Press: pressRune('m'), Class: KeyOpen, ID: "marshal.chat", Hint: "chat", When: func(w *Workspace) bool { return w.boardVisible() }, Why: reason("The Marshal chat opens on the Board.")},
	{Group: "MARSHAL", Keys: "s", What: "say it to the Marshal, Enter", Press: pressRune('s'), Class: KeyOpen, ID: "marshal.say", Hint: "say", When: func(w *Workspace) bool { return w.boardVisible() && w.boardUI.inspect == "" }, Why: reason("Leave the inspector to speak to the Marshal.")},
	{Group: "MARSHAL", Keys: "/", What: "command; /notify on|off alerts", Class: KeyOpen, ID: "command.line", Hint: "command"},
	{ID: "palette", Group: "MARSHAL", Keys: "space  Ctrl+K", What: "all actions; type to filter", Hint: "actions", Class: KeyOpen},
	{Group: "MARSHAL", Keys: "?", What: "this help", Press: pressRune('?'), Class: KeyOpen, ID: "help", Hint: "help"},
	{Group: "MARSHAL", Keys: "Ctrl+X", What: "stop all workers, never the Marshal", Command: "/stop all", Class: KeyConfirm, Direct: "spec: ^X unchanged, one key stops all workers (Decision 9)", ID: "workers.stop"},
	{Group: "MARSHAL", Keys: "Ctrl+C", What: "interrupt; twice to exit", Class: KeyConfirm, Direct: "spec: ^C unchanged, it interrupts; a second press exits", ID: "interrupt"},

	{Group: "FUNCTION KEYS", Keys: "F1", What: "help overview", Command: "/help", Class: KeyOpen, ID: "fkey.help"},
	{Group: "FUNCTION KEYS", Keys: "F2", What: "review", Command: "/review", Class: KeyOpen, ID: "fkey.review"},
	{Group: "FUNCTION KEYS", Keys: "F3", What: "diff", Command: "/diff", Class: KeyOpen, ID: "fkey.diff"},
	{Group: "FUNCTION KEYS", Keys: "F4", What: "status", Command: "/status", Class: KeyOpen, ID: "fkey.status"},
	{Group: "FUNCTION KEYS", Keys: "F5", What: "models", Command: "/models", Class: KeyOpen, ID: "fkey.models"},
	{Group: "FUNCTION KEYS", Keys: "F6", What: "MCP", Command: "/mcp", Class: KeyOpen, ID: "fkey.mcp"},
	{Group: "FUNCTION KEYS", Keys: "F7", What: "native Codex", Command: "/codex", Class: KeyOpen, ID: "fkey.codex"},
	{Group: "FUNCTION KEYS", Keys: "F8", What: "native Claude", Command: "/claude", Class: KeyOpen, ID: "fkey.claude"},
	{Group: "FUNCTION KEYS", Keys: "F9", What: "native OpenCode", Command: "/opencode", Class: KeyOpen, ID: "fkey.opencode"},
	{Group: "FUNCTION KEYS", Keys: "F10", What: "update: check or install", Press: &KeyEvent{Type: KeyF10}, Class: KeyConfirm, Direct: "spec: F-keys unchanged; it installs only the update its notice offered", ID: "fkey.update"},
	{Group: "FUNCTION KEYS", Keys: "F12", What: "native Antigravity", Command: "/agy", Class: KeyOpen, ID: "fkey.agy"},
}

// boardReplyWaiting reports whether a reply is waiting on the Board: an
// owner gate still to send, or the plan guard's undo alert. inbox.reply's
// keys (y, u, K) do nothing without one, so the registry marks it off.
func (w *Workspace) boardReplyWaiting() bool {
	g := &w.guard
	g.mu.Lock()
	alert := g.alert != nil
	g.mu.Unlock()
	if alert {
		return true
	}
	w.mu.RLock()
	p := w.state.Marshal
	w.mu.RUnlock()
	for _, gate := range w.ownerGates(p) {
		if !gate.sent {
			return true
		}
	}
	return false
}

// boardStopWaiting reports whether a stopped run is waiting with an empty
// inbox, the only time run.stopped's Enter arms what the stop suggests and a
// second Enter does it.
func (w *Workspace) boardStopWaiting() bool {
	if len(w.boardInbox()) > 0 {
		return false
	}
	w.mu.RLock()
	p := w.state.Marshal
	var board boardmodel.Board
	note, stopped := "", false
	if p != nil {
		board, note, stopped = p.Board, p.Note, p.Stopped
	}
	w.mu.RUnlock()
	if p == nil {
		return false
	}
	// The same notice the inbox's Enter reads (handleBoardKey), so the row
	// is on exactly when that Enter would arm the stop.
	_, ok := (boardVisual{board: board, note: note, stopped: stopped}).stopNotice()
	return ok
}

// action is the entry as a palette action: the view entry for a view's key,
// the key entry otherwise. runBoardAction and boardPaletteActions both build
// it, so a click, the palette and the hint bar run the one same thing.
func (k boardKey) action() PaletteAction {
	a := PaletteAction{ID: k.ID, Category: "KEY", Title: k.What, Description: k.Group, Shortcut: k.Keys, Command: k.Command, Key: k.Press, Pane: k.pane()}
	if k.View != "" {
		a.Category, a.Title, a.View = "VIEW", "Open "+k.View+" view", true
		if k.View == "Board" {
			a.Title = "Open the Board"
		}
	}
	return a
}

// available reports whether the action can run now and, when not, why.
func (k boardKey) available(w *Workspace) (bool, string) {
	if k.When == nil || k.When(w) {
		return true, ""
	}
	if k.Why != nil {
		return false, k.Why(w)
	}
	return false, ""
}

// boardAction is the registry entry with id; ok is false for an unknown id.
func boardAction(id string) (boardKey, bool) {
	for _, k := range boardKeymap {
		if k.ID == id {
			return k, true
		}
	}
	return boardKey{}, false
}

// boardPaletteActions is the keymap as palette entries: one "Open <View>
// view" per view first, then every other key. With a workspace it marks the
// entries that cannot run now disabled and why; a nil workspace (the static
// constructor) leaves every entry available.
func boardPaletteActions(w *Workspace) []PaletteAction {
	var views, keys []PaletteAction
	for _, k := range boardKeymap {
		a := k.action()
		if w != nil {
			if ok, why := k.available(w); !ok {
				a.Disabled, a.Reason = true, why
			}
		}
		if k.View != "" {
			views = append(views, a)
			continue
		}
		if k.Command == "" && k.Press == nil {
			a.Disabled, a.Reason = true, "press "+k.Keys
		}
		keys = append(keys, a)
	}
	return append(views, keys...)
}

// runBoardAction runs one registry action the way its key would: the one
// path the palette, the hint bar and every mouse button take. An unknown id
// does nothing; an action that cannot run now says why instead.
func (w *Workspace) runBoardAction(ctx context.Context, id string) {
	k, ok := boardAction(id)
	if !ok {
		return
	}
	if run, why := k.available(w); !run {
		w.setOutput(why, false)
		return
	}
	w.runPaletteAction(ctx, k.action())
}

// pane is the Board pane or view the entry's keys act in: the inbox for
// the inbox's keys, the view a view's key names before its colon, none
// for a key that acts anywhere.
func (k boardKey) pane() string {
	switch k.Group {
	case "INBOX":
		return string(layout.Inbox)
	case "IN A VIEW":
		name, _, _ := strings.Cut(k.What, ":")
		return strings.ToUpper(name[:1]) + name[1:]
	}
	return ""
}

// inboxPaletteKeys are the inbox's keys the palette offers for each
// pending request, in the order it lists them.
var inboxPaletteKeys = []struct {
	key  rune
	verb string
}{
	{'a', "Allow once"},
	{'A', "Allow for the run"},
	{'d', "Deny"},
	{'i', "Inspect the request"},
	{'r', "Return with a reason"},
}

// inboxPaletteActions is the palette's entries for one pending request:
// each opens the inbox's own confirmation for it, never decides it.
func inboxPaletteActions(item boardmodel.InboxItem) []PaletteAction {
	subject := cleanBoard(item.Request.Kind) + " " + cleanBoard(item.Request.Object)
	if item.Request.TaskID != "" {
		subject = cleanBoard(item.Request.TaskID) + " " + subject
	}
	out := make([]PaletteAction, 0, len(inboxPaletteKeys))
	for _, k := range inboxPaletteKeys {
		out = append(out, PaletteAction{
			ID:          "inbox." + string(k.key) + "." + item.Canonical,
			Category:    "INBOX",
			Title:       k.verb + " · " + subject,
			Description: inbox.Blast(item) + " " + inbox.Reason(item),
			Shortcut:    string(k.key),
			Key:         pressRune(k.key),
			Pane:        string(layout.Inbox),
			Inbox:       item.Request.Key(),
		})
	}
	return out
}

// paletteContext is what the palette ranks by as it opens on the Board:
// the open view or the focused pane, and the inbox's actions. The
// selection's request is the selected inbox item's when the inbox has the
// focus and the selected task's when the tasks have it (a parked task
// waits on it); the other pending requests follow it.
func (w *Workspace) paletteContext() PaletteContext {
	if !w.boardVisible() {
		return PaletteContext{}
	}
	var c PaletteContext
	for _, v := range []struct {
		open func() bool
		name string
	}{{w.hostView.Load, "Host"}, {w.memView.Load, "Memory"}, {w.provView.Load, "Providers"}, {w.verView.Load, "Verify"}, {w.secView.Load, "Security"}, {w.planView.Load, "Plan"}, {w.archView.Load, "Architecture"}} {
		if v.open() {
			c.Pane = v.name
		}
	}
	items := w.boardInbox()
	w.inboxView.Update(items)
	ui := &w.boardUI
	focus := ui.pane(len(items) > 0)
	if c.Pane == "" {
		c.Pane = string(focus)
	}
	selected := ""
	switch {
	case c.Pane == string(layout.Inbox):
		selected = w.inboxView.Selected()
	case c.Pane == string(layout.Tasks) && ui.task != "":
		task := ui.task
		if ui.inspect != "" {
			task = ui.inspect
		}
		if i := slices.IndexFunc(items, func(it boardmodel.InboxItem) bool { return it.Request.TaskID == task }); i >= 0 {
			selected = items[i].Request.Key()
		}
	}
	for _, it := range items {
		if it.Request.Key() == selected {
			c.Selection = inboxPaletteActions(it)
		} else {
			c.Others = append(c.Others, inboxPaletteActions(it)...)
		}
	}
	return c
}

// beginInboxFromPalette selects the entry's request in the inbox and
// opens the inbox's confirmation for its key over the Board, as the key
// pressed in the inbox would.
func (w *Workspace) beginInboxFromPalette(a PaletteAction) {
	items := w.boardInbox()
	want := slices.IndexFunc(items, func(it boardmodel.InboxItem) bool { return it.Request.Key() == a.Inbox })
	if !w.boardVisible() || want < 0 || a.Key == nil {
		w.setOutput("That request is no longer waiting in the inbox.", false)
		return
	}
	w.closeBodyViews()
	w.boardHelp.Store(false)
	w.boardUI.inspect = ""
	w.boardUI.focus = layout.Inbox
	w.inboxView.Update(items)
	cur := slices.IndexFunc(items, func(it boardmodel.InboxItem) bool { return it.Request.Key() == w.inboxView.Selected() })
	w.inboxView.Move(want - cur)
	w.inboxView.Begin(a.Key.Rune)
}

// runPaletteAction does what a palette entry chose: opens the inbox's
// confirmation, opens its view, sends its key, or runs its command.
func (w *Workspace) runPaletteAction(ctx context.Context, a PaletteAction) {
	switch {
	case a.Inbox != "":
		w.beginInboxFromPalette(a)
	case a.View:
		w.openViewFromPalette(a)
	case a.Key != nil:
		w.pressBoardKey(ctx, *a.Key)
	case a.Command != "":
		w.runCommand(ctx, a.Command)
	}
}

// openViewFromPalette opens the entry's view in the Board's body, or the
// Board itself, whatever is open now.
func (w *Workspace) openViewFromPalette(a PaletteAction) {
	name := strings.TrimSuffix(strings.TrimPrefix(a.Title, "Open "), " view")
	if !w.boardVisible() {
		w.setOutput("The "+name+" view opens on the Board; there is no run on screen.", false)
		return
	}
	w.closeBodyViews()
	w.boardUI.inspect = ""
	if a.Key.Rune == '1' {
		return
	}
	if !w.switchBodyView(*a.Key) {
		w.setOutput("The "+name+" view has nothing to read in this session.", false)
	}
}

// pressBoardKey sends a key from the palette through the Board's own
// handlers, in the order the input loop tries them.
func (w *Workspace) pressBoardKey(ctx context.Context, event KeyEvent) {
	if event.Type == KeyF10 {
		w.runCommand(ctx, w.updateKeyCommand())
		return
	}
	if w.handleBoardKey(event) || w.handleBoardInboxKey(ctx, event) {
		return
	}
	w.handleShortcut(ctx, event)
}

// boardMouseHelp is what a left click does on the Board, as help lists
// it after the keys. Each line names a region handleBoardMouse acts on
// (board_mouse.go); TestBoardHelpListsMouse keeps the two together.
var boardMouseHelp = []struct{ Where, What string }{
	{"task  lane", "select; again: inspect or show"},
	{"header chip", "focus its pane or open its view"},
	{"[a] [d] [y]…", "open its confirmation"},
	{"[Enter]", "run stopped: do what it suggests"},
	{"footer key", "press it; any click closes help"},
}

// boardHelpRows is the keymap as help lines, group by group, in columns
// of w cells, and the mouse's click targets after it.
func (v boardVisual) boardHelpRows(w int) [][]string {
	var groups [][]string
	for i, k := range boardKeymap {
		if i == 0 || boardKeymap[i-1].Group != k.Group {
			groups = append(groups, []string{v.bold(v.fg("accent", k.Group))})
		}
		g := &groups[len(groups)-1]
		// PadRight pads the common keys to a column but never clips a wider
		// entry ("space  Ctrl+K"), so every key still shows in full.
		*g = append(*g, PadCell(" "+v.fg("white", PadRight(k.Keys, 11))+" "+k.What, w))
	}
	mouse := []string{v.bold(v.fg("accent", "MOUSE")) + v.fg("dim", " left click; never decides")}
	for _, m := range boardMouseHelp {
		mouse = append(mouse, PadCell(" "+v.fg("white", PadCell(m.Where, 14))+" "+m.What, w))
	}
	return append(groups, mouse)
}

// helpOverlay is the Board's help (?): every key of boardKeymap in a box
// w by h cells, in two columns when it is wide enough, scrolled down by
// scroll lines. It returns the lines and the furthest useful scroll.
func (v boardVisual) helpOverlay(w, h, scroll int) ([]string, int) {
	th := v.theme
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}
	inner := max(1, w-4)
	cols := 1
	if inner >= 86 {
		cols = 2
	}
	colW := (inner - 3*(cols-1)) / cols
	groups := v.boardHelpRows(colW)
	var col [2][]string
	total := 0
	for _, g := range groups {
		total += len(g) + 1
	}
	c := 0
	for i, g := range groups {
		if cols == 2 && c == 0 && len(col[0]) > 0 && len(col[0])+len(g) > (total+1)/2 {
			c = 1
		}
		if cols == 2 && i == len(groups)-1 && len(col[0]) < len(col[1]) {
			// The mouse's group closes the shorter column, so help
			// scrolls no sooner than the keys alone make it.
			c = 0
		}
		if len(col[c]) > 0 {
			col[c] = append(col[c], "")
		}
		col[c] = append(col[c], g...)
	}
	var body []string
	for i := range max(len(col[0]), len(col[1])) {
		line := ""
		for k := range cols {
			s := ""
			if i < len(col[k]) {
				s = col[k][i]
			}
			if k > 0 {
				line += "   "
			}
			line += PadCell(s, colW)
		}
		body = append(body, line)
	}
	rows := max(1, h-3)
	maxScroll := max(0, len(body)-rows)
	scroll = min(max(0, scroll), maxScroll)
	body = body[scroll:min(len(body), scroll+rows)]
	hint := "Esc or ? closes"
	if maxScroll > 0 {
		hint += fmt.Sprintf(" · j/k scroll · %d more lines", maxScroll-scroll)
	}
	title := " KEYS "
	out := []string{th.BoxTopLeft + th.BoxHoriz + v.bold(v.fg("white", title)) + strings.Repeat(th.BoxHoriz, max(0, w-3-VisibleLen(title))) + th.BoxTopRight}
	for _, l := range body {
		out = append(out, th.BoxVert+" "+PadCell(l, inner)+" "+th.BoxVert)
	}
	for len(out) < h-1 {
		out = append(out, th.BoxVert+strings.Repeat(" ", w-2)+th.BoxVert)
	}
	out = append(out, th.BoxBottomLeft+th.BoxHoriz+v.fg("dim", " "+hint+" ")+strings.Repeat(th.BoxHoriz, max(0, w-5-VisibleLen(hint)))+th.BoxBottomRight)
	for i := range out {
		out[i] = PadCell(out[i], w)
	}
	if !v.unicode() {
		out = hostASCII(boardASCIIGlyphs(out), w)
	}
	return out, maxScroll
}

// handleBoardHelpKey is the help overlay's keys while it is open: j, k,
// the arrows and the page keys scroll it, and any other key closes it
// without doing anything else.
func (w *Workspace) handleBoardHelpKey(event KeyEvent) bool {
	if !w.boardHelp.Load() {
		return false
	}
	if !w.boardVisible() {
		w.boardHelp.Store(false)
		return false
	}
	ui := &w.boardUI
	switch {
	case event.Type == KeyDown || event.Type == KeyRune && event.Rune == 'j':
		ui.helpScroll = min(ui.helpMax, ui.helpScroll+1)
	case event.Type == KeyUp || event.Type == KeyRune && event.Rune == 'k':
		ui.helpScroll = max(0, ui.helpScroll-1)
	case event.Type == KeyPgDn:
		ui.helpScroll = min(ui.helpMax, ui.helpScroll+10)
	case event.Type == KeyPgUp:
		ui.helpScroll = max(0, ui.helpScroll-10)
	default:
		w.boardHelp.Store(false)
	}
	return true
}

// openBoardHelp opens the help overlay over the Board, at its top.
func (w *Workspace) openBoardHelp() {
	w.boardUI.helpScroll = 0
	w.boardHelp.Store(true)
}

// handleBlockedBoardKey is the Board's key path while the launch check left the
// project blocked: r (and a click on [r] Retry) re-runs the check, q quits, and
// every other key except help is inert, so no view opens behind the Blocked
// screen. It reports whether it consumed the key and whether the key asked the
// workspace to exit.
func (w *Workspace) handleBlockedBoardKey(event KeyEvent) (handled, exit bool) {
	if !w.launchBlocked() || !w.boardVisible() || w.palette.IsOpen() || w.diffViewer.IsOpen() || w.completionOpen {
		return false, false
	}
	switch {
	case event.Type == KeyRune && event.Rune == 'r':
		w.retryLaunchCheck()
		return true, false
	case event.Type == KeyRune && event.Rune == 'q':
		return true, true
	case event.Type == KeyRune && event.Rune == '?', event.Type == KeyF1:
		// Help keeps its normal meaning while blocked.
		return false, false
	}
	return true, false
}
