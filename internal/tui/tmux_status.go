package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

// statusBar is Marshal's tmux status line. In a tmux session Marshal created
// (the one `marshal` starts and ends) it is the one row that spans the whole
// terminal above every pane, so it carries the vitals row on every screen:
// the Board, the chat docked beside it, the chat or a worker in its own
// window. While the chat is docked a second row carries both panes' titles,
// the chat's with the Marshal model, its limit and a clickable [m].
//
// Why the status line and not a pane: tmux draws it outside every pane, so
// no provider CLI, resize or split can cover or shift it; it needs no extra
// process; and it is one session option set, saved and restored. Pane
// border titles cannot carry a click target (tmux reports no mouse position
// on a border), the status line can (mouse_x and mouse_y, tmux 3.0+).
//
// A session the operator made keeps its own status line: there the Board
// paints the vitals row itself and the dock titles stay on the pane borders.
type statusBar struct {
	mu        sync.Mutex // serialises reconcile; held across tmux commands
	owned     bool       // the session is Marshal's; decided once
	decided   bool
	session   string
	installed bool
	saved     []savedOption
	rows      [2]string // last formats set
	lines     int       // last status line count set
	width     int       // window width the rows were laid out for
	kick      chan struct{}

	on   atomic.Bool // the status line carries the vitals row now
	dock atomic.Pointer[dockStatus]
}

// dockStatus is what the dock publishes for the title row.
type dockStatus struct {
	window, chat, board string
	chatW, boardW       int
	chatTitle           string // laid out chatW-3 cells wide (after the marker)
}

// savedOption is a session option as it was before Marshal set it: set says
// it had a session-local value, which is put back; otherwise it is unset.
type savedOption struct {
	name, value string
	set         bool
}

// statusOptions are the session options the status line sets.
var statusOptions = []string{"status", "status-position", "status-style", "status-format[0]", "status-format[1]"}

// statusEvery is how often the vitals row is refreshed between kicks.
const statusEvery = time.Second

// vitalsInStatus reports whether the tmux status line carries the vitals row,
// so no screen paints its own.
func (w *Workspace) vitalsInStatus() bool { return w.status.on.Load() }

// statusOwned reports whether the status line belongs to Marshal: the dock
// then puts its titles there instead of on the pane borders.
func (w *Workspace) statusOwned() bool {
	w.status.mu.Lock()
	defer w.status.mu.Unlock()
	w.decideStatusLocked()
	return w.status.owned
}

// decideStatusLocked decides, once, whether the session is Marshal's own.
// Caller holds status.mu.
func (w *Workspace) decideStatusLocked() {
	if w.status.decided || !w.isTmuxActive() {
		return
	}
	w.tmuxMu.Lock()
	session := w.tmuxSession
	w.tmuxMu.Unlock()
	if session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dockWait)
	defer cancel()
	w.status.decided = true
	w.status.owned, w.status.session = tmux.SessionOwned(ctx, session), session
}

// startStatus takes over the status line of a session Marshal created and
// keeps it current until ctx ends. The caller defers endStatus.
func (w *Workspace) startStatus(ctx context.Context) {
	w.status.mu.Lock()
	w.decideStatusLocked()
	if !w.status.owned {
		w.status.mu.Unlock()
		return
	}
	if w.status.kick == nil {
		w.status.kick = make(chan struct{}, 1)
	}
	kick := w.status.kick
	w.status.mu.Unlock()
	w.startBackground(ctx, func(ctx context.Context) {
		t := time.NewTicker(statusEvery)
		defer t.Stop()
		measure := true
		for {
			if err := w.reconcileStatus(ctx, measure); err != nil && ctx.Err() == nil {
				w.RecordActivity("Marshal status line: " + err.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-kick:
				measure = true
			case <-t.C:
				measure = false
			}
		}
	})
}

// kickStatus asks the status line to look again, measuring the window: after
// a resize or a dock change. Kicks coalesce.
func (w *Workspace) kickStatus() {
	w.status.mu.Lock()
	kick := w.status.kick
	w.status.mu.Unlock()
	if kick == nil {
		return
	}
	select {
	case kick <- struct{}{}:
	default:
	}
}

// publishDockStatus hands the dock's title row to the status line; nil says
// the chat is not docked.
func (w *Workspace) publishDockStatus(d *dockStatus) {
	old := w.status.dock.Swap(d)
	if old == nil && d == nil || old != nil && d != nil && *old == *d {
		return
	}
	w.kickStatus()
}

// statusTarget is the session target the options are set on.
func statusTarget(session string) string { return "=" + session + ":" }

// reconcileStatus sets the status rows that changed. measure re-reads the
// window width.
func (w *Workspace) reconcileStatus(ctx context.Context, measure bool) error {
	w.status.mu.Lock()
	defer w.status.mu.Unlock()
	if !w.status.owned {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, dockWait)
	defer cancel()
	target := statusTarget(w.status.session)
	if measure || w.status.width <= 0 {
		w.tmuxMu.Lock()
		board := w.tmuxMarshalPaneID
		w.tmuxMu.Unlock()
		if isPaneID(board) {
			if g, err := tmux.QueryPaneGeometry(ctx, board); err == nil && g.WindowWidth > 0 {
				w.status.width = g.WindowWidth
			}
		}
		if w.status.width <= 0 {
			return nil
		}
	}
	if !w.status.installed {
		saved, err := saveStatusOptions(ctx, target)
		if err != nil {
			return err
		}
		w.status.saved, w.status.installed = saved, true
		w.status.rows, w.status.lines = [2]string{}, 0
		for _, o := range [][2]string{{"status-position", "top"}, {"status-style", "default"}} {
			if _, err := tmux.RunCommand(ctx, "set-option", "-t", target, o[0], o[1]); err != nil {
				return err
			}
		}
	}
	w.mu.RLock()
	th := w.theme
	w.mu.RUnlock()
	rows := [2]string{ansiToTmux(w.vitalsRow(th, w.status.width)), ""}
	lines := 1
	if d := w.status.dock.Load(); d != nil {
		rows[1], lines = w.dockStatusRow(th, *d), 2
	}
	for i, row := range rows {
		if row == "" || row == w.status.rows[i] {
			continue
		}
		if _, err := tmux.RunCommand(ctx, "set-option", "-t", target, "status-format["+strconv.Itoa(i)+"]", row); err != nil {
			return err
		}
		w.status.rows[i] = row
	}
	if lines != w.status.lines {
		value := "on"
		if lines == 2 {
			value = "2"
		}
		if _, err := tmux.RunCommand(ctx, "set-option", "-t", target, "status", value); err != nil {
			return err
		}
		w.status.lines = lines
	}
	if !w.status.on.Swap(true) {
		// The Board drops its own vitals row on the next paint.
		w.requestRepaint()
	}
	return nil
}

// endStatus gives the session its own status line back.
func (w *Workspace) endStatus() {
	w.status.mu.Lock()
	defer w.status.mu.Unlock()
	if w.status.on.Swap(false) {
		w.requestRepaint()
	}
	if !w.status.installed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dockWait)
	defer cancel()
	target := statusTarget(w.status.session)
	var errs []error
	for _, o := range w.status.saved {
		args := []string{"set-option", "-u", "-t", target, o.name}
		if o.set {
			args = []string{"set-option", "-t", target, o.name, o.value}
		}
		if _, err := tmux.RunCommand(ctx, args...); err != nil {
			errs = append(errs, err)
		}
	}
	w.status.installed, w.status.saved, w.status.rows, w.status.lines = false, nil, [2]string{}, 0
	if err := errors.Join(errs...); err != nil {
		w.RecordActivity("Marshal status line: " + err.Error())
	}
}

// saveStatusOptions reads each status option's session-local value.
func saveStatusOptions(ctx context.Context, target string) ([]savedOption, error) {
	var out []savedOption
	for _, name := range statusOptions {
		listed, err := tmux.RunCommand(ctx, "show-options", "-q", "-t", target, name)
		if err != nil {
			return nil, err
		}
		o := savedOption{name: name}
		if strings.TrimSpace(string(listed)) != "" {
			value, err := tmux.RunCommand(ctx, "show-options", "-qv", "-t", target, name)
			if err != nil {
				return nil, err
			}
			o.set, o.value = true, strings.TrimSuffix(string(value), "\n")
		}
		out = append(out, o)
	}
	return out, nil
}

// dockStatusRow is the title row while the chat is docked: in the dock's
// window, the chat's title above the chat and the Board's above the Board,
// the focused one marked as the pane borders marked it; in any other window
// its name.
func (w *Workspace) dockStatusRow(th *Theme, d dockStatus) string {
	v := boardVisual{theme: th}
	seg := func(text string, width int, active bool) string {
		marker := "   "
		if active {
			marker = " ▶ "
		}
		cell := PadCell(marker+text, width)
		if active {
			return "#[fg=" + tmuxColour("ink") + "]#[bg=" + tmuxColour("accent") + "]#[bold]" + tmuxText(cell) + "#[default]"
		}
		return "#[fg=" + tmuxColour("dim") + "]" + tmuxText(cell) + "#[default]"
	}
	choose := func(pane, active, inactive string) string {
		return "#{?#{==:#{pane_id}," + pane + "}," + escapeBranch(active) + "," + escapeBranch(inactive) + "}"
	}
	rule := "#[fg=" + tmuxColour("faint") + "]" + tmuxText(v.boxVert()) + "#[default]"
	dock := choose(d.chat, seg(d.chatTitle, d.chatW, true), seg(d.chatTitle, d.chatW, false)) + rule +
		choose(d.board, seg(boardDockTitle, d.boardW, true), seg(boardDockTitle, d.boardW, false))
	other := "#[fg=" + tmuxColour("dim") + "] #{window_name}#[default]"
	return "#{?#{==:#{window_id}," + d.window + "}," + escapeBranch(dock) + "," + escapeBranch(other) + "}"
}

// boxVert is the rule between the docked panes.
func (v boardVisual) boxVert() string {
	if v.theme != nil && v.theme.BoxVert != "" {
		return v.theme.BoxVert
	}
	return "│"
}

// tmuxText escapes literal text for a tmux format.
func tmuxText(s string) string { return strings.ReplaceAll(s, "#", "##") }

// escapeBranch escapes the commas and closing braces a #{?c,a,b} branch must
// not contain bare, leaving nested #{...} formats and #[...] styles whole.
func escapeBranch(s string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '#' && i+1 < len(s) && (s[i+1] == '{' || s[i+1] == '['):
			if s[i+1] == '{' {
				depth++
			}
			b.WriteString(s[i : i+2])
			i++
			continue
		case c == '#' && i+1 < len(s):
			b.WriteString(s[i : i+2])
			i++
			continue
		case c == '}' && depth > 0:
			depth--
		case c == '}':
			b.WriteString("#}")
			continue
		case c == ',' && depth == 0:
			b.WriteString("#,")
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// ansiToTmux turns a painted row's SGR escapes into tmux styles, so the
// status line shows the vitals row in the Board's colours. Each attribute is
// its own #[...] so the result holds no comma; other escapes are dropped.
func ansiToTmux(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			if s[i] == '#' {
				b.WriteByte('#')
			}
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';') {
			j++
		}
		if j >= len(s) {
			break
		}
		if s[j] == 'm' {
			b.WriteString(sgrToTmux(s[i+2 : j]))
		}
		i = j
	}
	return b.String()
}

func sgrToTmux(params string) string {
	if params == "" {
		return "#[default]"
	}
	p := strings.Split(params, ";")
	var b strings.Builder
	style := func(s string) { b.WriteString("#[" + s + "]") }
	for k := 0; k < len(p); k++ {
		n, err := strconv.Atoi(p[k])
		if err != nil {
			continue
		}
		switch {
		case n == 0:
			style("default")
		case n == 1:
			style("bold")
		case n == 2:
			style("dim")
		case n == 3:
			style("italics")
		case n == 4:
			style("underscore")
		case n == 7:
			style("reverse")
		case n == 22:
			style("nobold")
			style("nodim")
		case n == 23:
			style("noitalics")
		case n == 24:
			style("nounderscore")
		case n == 27:
			style("noreverse")
		case n == 39:
			style("fg=default")
		case n == 49:
			style("bg=default")
		case n >= 30 && n <= 37:
			style(fmt.Sprintf("fg=colour%d", n-30))
		case n >= 40 && n <= 47:
			style(fmt.Sprintf("bg=colour%d", n-40))
		case n >= 90 && n <= 97:
			style(fmt.Sprintf("fg=colour%d", n-90+8))
		case n >= 100 && n <= 107:
			style(fmt.Sprintf("bg=colour%d", n-100+8))
		case (n == 38 || n == 48) && k+1 < len(p):
			key := "fg"
			if n == 48 {
				key = "bg"
			}
			switch p[k+1] {
			case "5":
				if k+2 < len(p) {
					style(key + "=colour" + p[k+2])
					k += 2
				}
			case "2":
				if k+4 < len(p) {
					r, _ := strconv.Atoi(p[k+2])
					g, _ := strconv.Atoi(p[k+3])
					bl, _ := strconv.Atoi(p[k+4])
					style(fmt.Sprintf("%s=#%02x%02x%02x", key, r&255, g&255, bl&255))
					k += 4
				}
			}
		}
	}
	return b.String()
}

// marshalPickSeq is what a click on [m] sends the Board pane: a private CSI
// sequence no terminal sends, which the Board reads as KeyMarshalPick. It
// goes to the Board, never to the chat, so the provider CLI sees nothing.
var marshalPickSeq = []string{"1b", "5b", "39", "30", "30", "31", "7e"} // ESC [ 9 0 0 1 ~

// dockClickKey is the mouse key a click on the status line outside any range
// is (tmux 3.0+); the title row has no ranges, so every click on it is this.
const dockClickKey = "MouseDown1StatusDefault"

// bindDockClicks binds a click on the title row in the dock's own key tables
// (the chat's and the Board's; the client uses one of them while either pane
// is selected, and quit unbinds both): on [m] it selects the Board and opens
// the Marshal picker there; elsewhere above a pane it selects that pane. The
// root table and the operator's other sessions are never touched. x0..x1 are
// the window columns [m] covers.
func (w *Workspace) bindDockClicks(ctx context.Context, root, window, chat, board string, chatW, x0, x1 int) error {
	onRow := "#{&&:#{==:#{window_id}," + window + "},#{==:#{mouse_y},1}}"
	within := func(a, b int) string {
		return "#{&&:" + onRow + ",#{&&:#{e|>=:#{mouse_x}," + strconv.Itoa(a) + "},#{e|<:#{mouse_x}," + strconv.Itoa(b) + "}}}"
	}
	pick := "select-pane -t " + board + " ; send-keys -t " + board + " -H " + strings.Join(marshalPickSeq, " ")
	rest := "if-shell -F \"" + within(0, chatW) + "\" \"select-pane -t " + chat + "\" \"if-shell -F '" + within(chatW+1, 1<<16) + "' 'select-pane -t " + board + "'\""
	for _, table := range []string{dockKeyTable(root), boardDockKeyTable(root)} {
		if _, err := tmux.RunCommand(ctx, "bind-key", "-T", table, dockClickKey, "if-shell", "-F", within(x0, x1), pick, rest); err != nil {
			return err
		}
	}
	return nil
}
