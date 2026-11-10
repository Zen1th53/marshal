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
}

func pressRune(r rune) *KeyEvent { return &KeyEvent{Type: KeyRune, Rune: r} }

// boardKeymap is every key the Board binds, in the order help lists them.
var boardKeymap = []boardKey{
	{Group: "VIEWS", Keys: "1  Esc", What: "back to the Board", View: "Board", Press: pressRune('1'), Class: KeyOpen},
	{Group: "VIEWS", Keys: "2  h", What: "host: temperature, disk, guards", View: "Host", Press: pressRune('2'), Class: KeyOpen},
	{Group: "VIEWS", Keys: "3", What: "memory (MCP): review queue", View: "Memory", Press: pressRune('3'), Class: KeyOpen},
	{Group: "VIEWS", Keys: "4", What: "providers: harness health", View: "Providers", Press: pressRune('4'), Class: KeyOpen},
	{Group: "VIEWS", Keys: "5", What: "verify: claims and their evidence", View: "Verify", Press: pressRune('5'), Class: KeyOpen},
	{Group: "VIEWS", Keys: "6", What: securityKeyWhat, View: "Security", Press: pressRune('6'), Class: KeyOpen},
	{Group: "VIEWS", Keys: "7", What: "plan: task graph and plan versions", View: "Plan", Press: pressRune('7'), Class: KeyOpen},
	{Group: "VIEWS", Keys: "8  b", What: "architecture and business process", View: "Architecture", Press: pressRune('8'), Class: KeyOpen},

	{Group: "BOARD", Keys: "j k  ↑ ↓", What: "move in the focused pane, scroll", Class: KeyMove},
	{Group: "BOARD", Keys: "Enter", What: "inspect a task; a lane: its worker", Class: KeyOpen},
	{Group: "BOARD", Keys: "t  i  l", What: "focus tasks, inbox, lanes", Class: KeyMove},
	{Group: "BOARD", Keys: "Esc", What: "close the inspector, a view, help", Class: KeyOpen},
	{Group: "BOARD", Keys: "e", What: "inspector: toggle all events", Class: KeyOpen},
	{Group: "BOARD", Keys: "Enter  ⌫", What: "inspector: child task, parent task", Class: KeyOpen},
	{Group: "BOARD", Keys: "d  D", What: "diff; D: the task against base", Command: "/diff", Class: KeyOpen},

	{Group: "INBOX", Keys: "y  u  K", What: "reply; changed files: undo, keep", Class: KeyConfirm, Except: map[string]KeyClass{"K": KeyOpen}},
	{Group: "INBOX", Keys: "a  A  d  r", What: "allow once, for the run, deny, return", Class: KeyConfirm},
	{Group: "INBOX", Keys: "i  Enter", What: "show the request's reason", Class: KeyOpen},
	{Group: "INBOX", Keys: "Enter", What: "run stopped: type its command", Class: KeyOpen, Listed: true},
	{Group: "INBOX", Keys: "x", What: "cancel the decision being typed", Class: KeyOpen},
	{Group: "INBOX", Keys: "hard gate", What: "type the canonical id, then Enter", Class: KeyConfirm, Listed: true},

	{Group: "IN A VIEW", Keys: "a  r  i", What: "memory: keep, reject, evidence", Class: KeyConfirm, Except: map[string]KeyClass{"i": KeyOpen}},
	{Group: "IN A VIEW", Keys: "PgUp PgDn", What: "memory: scroll", Class: KeyMove},
	{Group: "IN A VIEW", Keys: "v", What: "providers: verify harness, Enter", Press: pressRune('v'), Class: KeyConfirm},
	{Group: "IN A VIEW", Keys: "r", What: "providers: route around, then Enter", Class: KeyConfirm},
	{Group: "IN A VIEW", Keys: "x", What: "verify: return claims, then Enter", Press: pressRune('x'), Class: KeyConfirm},
	{Group: "IN A VIEW", Keys: "a  d", What: "security: allow, deny, then Enter", Class: KeyConfirm},
	{Group: "IN A VIEW", Keys: "Enter", What: "plan: inspect the selected task", Class: KeyOpen},
	{Group: "IN A VIEW", Keys: "o", What: "architecture: open in the browser", Press: pressRune('o'), Class: KeyOpen},
	{Group: "IN A VIEW", Keys: "d", What: "architecture: diff since accepted", Class: KeyOpen},

	{Group: "MARSHAL", Keys: "m  F11", What: "chat; F11 or Alt+arrow switch", Press: pressRune('m'), Class: KeyOpen},
	{Group: "MARSHAL", Keys: "s", What: "say it to the Marshal, Enter", Press: pressRune('s'), Class: KeyOpen},
	{Group: "MARSHAL", Keys: "/", What: "command; /notify on|off alerts", Class: KeyOpen},
	{Group: "MARSHAL", Keys: "Ctrl+P", What: "command palette", Class: KeyOpen},
	{Group: "MARSHAL", Keys: "?", What: "this help", Class: KeyOpen},
	{Group: "MARSHAL", Keys: "Ctrl+X", What: "stop all workers, never the Marshal", Command: "/stop all", Class: KeyConfirm, Direct: "spec: ^X unchanged, one key stops all workers (Decision 9)"},
	{Group: "MARSHAL", Keys: "Ctrl+C", What: "interrupt; twice to exit", Class: KeyConfirm, Direct: "spec: ^C unchanged, it interrupts; a second press exits"},

	{Group: "FUNCTION KEYS", Keys: "F1", What: "help overview", Command: "/help", Class: KeyOpen},
	{Group: "FUNCTION KEYS", Keys: "F2", What: "review", Command: "/review", Class: KeyOpen},
	{Group: "FUNCTION KEYS", Keys: "F3", What: "diff", Command: "/diff", Class: KeyOpen},
	{Group: "FUNCTION KEYS", Keys: "F4", What: "status", Command: "/status", Class: KeyOpen},
	{Group: "FUNCTION KEYS", Keys: "F5", What: "models", Command: "/models", Class: KeyOpen},
	{Group: "FUNCTION KEYS", Keys: "F6", What: "MCP", Command: "/mcp", Class: KeyOpen},
	{Group: "FUNCTION KEYS", Keys: "F7", What: "native Codex", Command: "/codex", Class: KeyOpen},
	{Group: "FUNCTION KEYS", Keys: "F8", What: "native Claude", Command: "/claude", Class: KeyOpen},
	{Group: "FUNCTION KEYS", Keys: "F9", What: "native OpenCode", Command: "/opencode", Class: KeyOpen},
	{Group: "FUNCTION KEYS", Keys: "F10", What: "update: check or install", Press: &KeyEvent{Type: KeyF10}, Class: KeyConfirm, Direct: "spec: F-keys unchanged; it installs only the update its notice offered"},
	{Group: "FUNCTION KEYS", Keys: "F12", What: "native Antigravity", Command: "/agy", Class: KeyOpen},
}

// boardPaletteActions is the keymap as palette entries: one "Open <View>
// view" per view first, then every other key.
func boardPaletteActions() []PaletteAction {
	var views, keys []PaletteAction
	for _, k := range boardKeymap {
		a := PaletteAction{Category: "KEY", Title: k.What, Description: k.Group, Shortcut: k.Keys, Command: k.Command, Key: k.Press, Pane: k.pane()}
		if k.View != "" {
			a.ID, a.Category, a.Title, a.View = "view."+strings.ToLower(k.View), "VIEW", "Open "+k.View+" view", true
			if k.View == "Board" {
				a.Title = "Open the Board"
			}
			views = append(views, a)
			continue
		}
		a.ID = "key." + k.Keys
		if k.Command == "" && k.Press == nil {
			a.Disabled, a.Reason = true, "press "+k.Keys
		}
		keys = append(keys, a)
	}
	return append(views, keys...)
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
	{"type › cmd", "run stopped: fill command line"},
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
		*g = append(*g, PadCell(" "+v.fg("white", PadCell(k.Keys, 11))+" "+k.What, w))
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
	if !w.launchBlocked() || !w.boardVisible() || w.navView.IsOpen() || w.palette.IsOpen() || w.diffViewer.IsOpen() || w.completionOpen {
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
