package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/usage"
)

// The Marshal picker is protocol v2 step 1: once the launch check is ready and
// before the Marshal chat starts, the owner names the model that plans. It
// shows only when the saved choice cannot be used — it is missing, not
// installed, or out of limit — so a returning owner whose provider still has
// quota is never stopped by it. Each candidate shows its live limit (the P3-01
// reader) and whether it is busy; a limit that cannot be read reads
// "unavailable", never a guess.

// pickerState is the launch picker's shared state.
type pickerState struct {
	mu     sync.Mutex
	open   bool
	cands  []pickerCand
	sel    int
	reason string
}

// pickerCand is one signed-in provider as the picker shows it. left is the
// P3-01 smallest-window percent left when known; out is set when it is out of
// limit (0% left, or a running cooldown); busy when a lane is running on it.
type pickerCand struct {
	provider string
	left     int
	known    bool
	out      bool
	busy     bool
	until    time.Time
}

// pickerView is a frame's copy of the picker, read under the lock once and
// painted without it.
type pickerView struct {
	cands  []pickerCand
	sel    int
	reason string
}

// providerLeft is the one reader of a P3-01 quota the picker and P2-14 share:
// the smallest-window percent left as an integer, whether it is known, and when
// it resets. It parses no provider file itself; usage.Read did that.
func providerLeft(q usage.Quota) (left int, known bool, reset time.Time) {
	win, ok := q.Binding()
	if !ok {
		return 0, false, time.Time{}
	}
	return usageLeft(win), true, win.ResetsAt
}

// pickerOpen reports whether the launch picker is waiting for an answer.
func (w *Workspace) pickerOpen() bool {
	w.picker.mu.Lock()
	defer w.picker.mu.Unlock()
	return w.picker.open
}

// pickerPaint copies the picker for one frame.
func (w *Workspace) pickerPaint() pickerView {
	w.picker.mu.Lock()
	defer w.picker.mu.Unlock()
	return pickerView{cands: slices.Clone(w.picker.cands), sel: w.picker.sel, reason: w.picker.reason}
}

// pickerInstallLine is shown when no coding CLI is installed. The command is
// the first-use card's install hint when it has one, else this default.
const pickerInstallLine = "No coding CLI is installed. Install one, for example npm i -g @openai/codex, then press r."

// readUsageNow reads every provider's quota, through the test reader when one
// is set (as the header does), so the picker never touches a provider file on
// the paint path or in a test.
func (w *Workspace) readUsageNow(now time.Time) []usage.Quota {
	w.usage.mu.Lock()
	read := w.usage.readFn
	w.usage.mu.Unlock()
	if read == nil {
		read = w.readUsage
	}
	return read(now)
}

// marshalCandidates builds one pickerCand per installed Marshal provider, in
// app.MarshalProviders() order, each filled with its P3-01 limit (percent left
// or unavailable, never a guess), whether a lane is busy on it, and whether it
// is out of limit. It reads PATH, the usage store and the provider overview
// once, so callers must never call it on the paint path. P2-04's launch picker
// and P5-02's Marshal picker share it: there is one limit/busy reader.
func (w *Workspace) marshalCandidates(ctx context.Context) []pickerCand {
	return w.candidatesForDrivers(ctx, app.MarshalProviders())
}

// candidatesForDrivers is marshalCandidates over an explicit driver list. The
// Marshal picker (m) passes app.MarshalProviders(); the task-model picker (w,
// board_task_model.go) passes the four worker drivers, so neither copies the
// one limit/busy reader. providers is the row order; a provider not installed
// is left out (P2-04's candidate rule).
func (w *Workspace) candidatesForDrivers(ctx context.Context, providers []string) []pickerCand {
	installed := installedNeutralProviders(ctx)
	var cands []pickerCand
	for _, p := range providers {
		if slices.Contains(installed, p) {
			cands = append(cands, pickerCand{provider: p})
		}
	}
	if len(cands) == 0 {
		return nil
	}
	now := time.Now()
	// The overview carries busy (active lanes) and a running cooldown; a read
	// that fails or is slow leaves every candidate not busy and not limited.
	overview := map[string]store.ProviderStatus{}
	if src := w.providerSrc(); src != nil {
		octx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if ps, err := src.ProviderOverview(octx); err == nil {
			for _, p := range ps {
				overview[providerWorker(p.Provider)] = p
			}
		}
		cancel()
	}
	quotas := map[string]usage.Quota{}
	reading := w.readUsageNow(now)
	// The vitals row shows this same reading at once, so it never says
	// "limit unknown" beside a picker that just showed the figure.
	w.storeUsage(reading)
	for _, q := range reading {
		quotas[q.Provider] = q
	}
	for i := range cands {
		p := cands[i].provider
		if q, ok := quotas[p]; ok {
			left, known, reset := providerLeft(q)
			cands[i].left, cands[i].known, cands[i].until = left, known, reset
			if known && left == 0 {
				cands[i].out = true
			}
		}
		if st, ok := overview[p]; ok {
			cands[i].busy = st.ActiveLanes > 0
			if providerState(st, now) == "limit" {
				cands[i].out = true
				if cands[i].until.IsZero() {
					cands[i].until = st.LimitUntil
				}
			}
		}
	}
	return cands
}

// decideMarshalPicker decides whether the launch picker opens. It returns true
// when it does; the caller then holds the chat-start callbacks until
// confirmPicker releases them. When it returns false it has already settled the
// provider (a returning owner's saved one, or the single candidate it saved),
// and the chat may start.
func (w *Workspace) decideMarshalPicker(ctx context.Context) bool {
	root := w.providerRoot()
	saved := loadDefaultProvider(root)
	cands := w.marshalCandidates(ctx)
	if len(cands) == 0 {
		w.openPicker(nil, 0, pickerInstallLine)
		return true
	}

	now := time.Now()
	savedIdx := slices.IndexFunc(cands, func(c pickerCand) bool { return c.provider == saved })
	// A saved provider that is installed and not out of limit needs no picker.
	if savedIdx >= 0 && !cands[savedIdx].out {
		w.closePicker()
		return false
	}
	// Exactly one candidate and it is usable: save it and continue, no picker.
	if len(cands) == 1 && !cands[0].out {
		_ = saveDefaultProvider(root, cands[0].provider)
		w.closePicker()
		return false
	}

	reason := ""
	if savedIdx >= 0 && cands[savedIdx].out {
		reason = saved + " is out of limit"
		if u := cands[savedIdx].until; !u.IsZero() {
			reason += " until " + u.In(now.Location()).Format("15:04")
		}
		reason += ". "
	}
	w.openPicker(cands, preselectPicker(cands, saved), reason)
	return true
}

// preselectPicker is the entry drawn selected: the saved provider when it is a
// usable candidate, otherwise the usable candidate with the most known percent
// left, otherwise the first usable candidate, otherwise the first.
func preselectPicker(cands []pickerCand, saved string) int {
	for i, c := range cands {
		if c.provider == saved && !c.out {
			return i
		}
	}
	best := -1
	for i, c := range cands {
		if c.known && !c.out && (best < 0 || c.left > cands[best].left) {
			best = i
		}
	}
	if best >= 0 {
		return best
	}
	for i, c := range cands {
		if !c.out {
			return i
		}
	}
	return 0
}

func (w *Workspace) openPicker(cands []pickerCand, sel int, reason string) {
	w.picker.mu.Lock()
	w.picker.open, w.picker.cands, w.picker.sel, w.picker.reason = true, cands, sel, reason
	w.picker.mu.Unlock()
	w.requestRepaint()
}

func (w *Workspace) closePicker() {
	w.picker.mu.Lock()
	w.picker.open, w.picker.cands, w.picker.reason, w.picker.sel = false, nil, "", 0
	w.picker.mu.Unlock()
}

// movePicker moves the selection and wraps around the ends.
func (w *Workspace) movePicker(delta int) {
	w.picker.mu.Lock()
	if n := len(w.picker.cands); n > 0 {
		w.picker.sel = ((w.picker.sel+delta)%n + n) % n
	}
	w.picker.mu.Unlock()
	w.requestRepaint()
}

// handlePickerKey answers the launch picker. Enter confirms the selection, the
// arrows move it, 1..n pick-and-confirm the nth candidate, r re-checks when no
// CLI is installed, Esc leaves the selection as it is, and q falls through to
// the global quit. The Board's other single keys are swallowed (the picker must
// be answered); function keys and Ctrl chords go on to the global keys.
func (w *Workspace) handlePickerKey(ev KeyEvent) bool {
	w.picker.mu.Lock()
	open, n, sel := w.picker.open, len(w.picker.cands), w.picker.sel
	w.picker.mu.Unlock()
	if !open {
		return false
	}
	switch {
	case ev.Type == KeyRune && ev.Rune == 'q':
		// q quits even while the picker waits, as on the Blocked screen.
		w.mu.Lock()
		w.exitRequested = true
		w.mu.Unlock()
		return true
	case ev.Type == KeyRune && ev.Rune == 'r' && n == 0:
		w.closePicker()
		w.retryLaunchCheck()
		return true
	case n == 0:
		// The install screen takes only r and q among the Board's keys.
		return ev.Type == KeyRune || ev.Type == KeyEsc
	case ev.Type == KeyEnter:
		w.confirmPicker(sel)
		return true
	case ev.Type == KeyUp || ev.Type == KeyLeft:
		w.movePicker(-1)
		return true
	case ev.Type == KeyDown || ev.Type == KeyRight:
		w.movePicker(1)
		return true
	case ev.Type == KeyRune && ev.Rune >= '1' && ev.Rune <= '9':
		if i := int(ev.Rune - '1'); i < n {
			w.confirmPicker(i)
		}
		return true
	}
	// Esc and the Board's other single keys keep the picker open: it must be
	// answered. Global keys (function keys, Ctrl chords) go on as usual.
	return ev.Type == KeyRune || ev.Type == KeyEsc
}

// confirmPicker settles the picker on the ith candidate: it saves the choice,
// records it in Activity, closes the picker and releases the held chat-start.
// An out-of-limit candidate cannot be kept, so it does nothing.
func (w *Workspace) confirmPicker(i int) {
	w.picker.mu.Lock()
	if !w.picker.open || i < 0 || i >= len(w.picker.cands) || w.picker.cands[i].out {
		w.picker.mu.Unlock()
		return
	}
	p := w.picker.cands[i].provider
	w.picker.open, w.picker.cands, w.picker.reason, w.picker.sel = false, nil, "", 0
	w.picker.mu.Unlock()

	if err := saveDefaultProvider(w.providerRoot(), p); err != nil {
		w.logTUIError("save the Marshal model", err)
		w.RecordActivity("The Marshal model was not saved. The details are in .marshal/logs/tui-errors.log.")
	}
	name := neutralProviderNames[canonicalNeutralProvider(p)]
	if name == "" {
		name = p
	}
	w.RecordActivity(name + " is the Marshal for this project.")
	w.requestRepaint()
	w.releaseLaunchPending()
}

// pickerGlyph is a provider's one glyph (the same as everywhere else), or ""
// without unicode.
func (v boardVisual) pickerGlyph(provider string) string {
	if !v.unicode() {
		return ""
	}
	return providerGlyph(provider, true)
}

// paintPicker paints the launch picker in the Board's body, with the Board's
// header and footer around it, as the first-use card is painted.
func (w *Workspace) paintPicker(l layout.Layout, th *Theme, v boardVisual) []string {
	out := make([]string, l.Rows)
	place := func(id layout.PaneID) {
		if p, ok := l.Pane(id); ok {
			lines := v.render(p)
			for i := 0; i < p.Rect.H && i < len(lines); i++ {
				out[p.Rect.Y+i] = PadCell(lines[i], l.Cols)
			}
		}
	}
	place(layout.Header)
	place(layout.Footer)
	body := l.Body
	if body.H <= 0 {
		return out
	}
	rows := v.pickerBody(body.W, body.H, body.Y)
	for i := 0; i < body.H; i++ {
		s := ""
		if i < len(rows) {
			s = rows[i]
		}
		out[body.Y+i] = PadCell(s, l.Cols)
	}
	if !v.unicode() {
		copy(out[body.Y:body.Y+body.H], v.ascii(out[body.Y:body.Y+body.H], l.Cols))
	}
	return out
}

// pickerBody is the picker's lines for a body w cells wide and h rows high
// whose first row is frame row y (for the click regions).
func (v boardVisual) pickerBody(w, h, y int) []string {
	var out []string
	add := func(s string) { out = append(out, PadCell(s, w)) }
	p := v.pick
	if len(p.cands) == 0 {
		for _, line := range wrapWords(p.reason, w) {
			add(line)
		}
		return out[:min(len(out), h)]
	}

	// The first line: any "<saved> is out of limit" note, the question, and one
	// coloured entry per candidate. Each entry's cells are a click region that
	// picks-and-confirms it.
	line := p.reason + "Who plans?  "
	x := VisibleLen(line)
	for i, c := range p.cands {
		if i > 0 {
			line += " · "
			x += 3
		}
		var figure, colour string
		switch {
		case c.out:
			figure = "out of limit"
			if !c.until.IsZero() {
				figure += " until " + c.until.In(v.now.Location()).Format("15:04")
			}
			colour = "failed"
		case !c.known:
			figure = limitUnknown
		default:
			figure = fmt.Sprintf("%d%% left", c.left)
		}
		entry := ""
		if g := v.pickerGlyph(c.provider); g != "" {
			entry = g + " "
		}
		entry += c.provider + " " + figure
		if c.busy {
			entry += " busy"
		}
		n := VisibleLen(entry)
		if colour != "" {
			entry = v.fg(colour, entry)
		}
		if i == p.sel {
			entry = v.bg("sel", entry)
		}
		if len(out) < h {
			v.hit(boardHit{row: y + len(out), x0: x, x1: x + n, kind: hitPicker, id: strconv.Itoa(i)})
		}
		line += entry
		x += n
	}
	add(line)

	// The keys line names the provider Enter keeps and how to change it.
	sel := ""
	if p.sel >= 0 && p.sel < len(p.cands) {
		sel = p.cands[p.sel].provider
	}
	arrows := "↑↓"
	if !v.unicode() {
		arrows = "Up/Down"
	}
	keys := fmt.Sprintf("[Enter] keep %s · [%s] or click to change · [1-%d] pick", sel, arrows, len(p.cands))
	add(v.fg("dim", keys))
	return out[:min(len(out), h)]
}
