package tui

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// The first-use Board is what a project with no run shows: the Board's own
// header and footer around one card that says what to do next. A raw
// provider CLI opens on a prompt; the Board opens on the same thing (the
// call to action focuses the chat, and the composer below it reaches the
// chat too), plus what a raw CLI never shows: whether each provider is
// ready, and the three steps from a goal to a running plan.

// firstUseState is the empty Board's own state. installed is read once off
// the paint path; armed is the provider whose verify waits for Enter, busy
// the one being verified.
type firstUseState struct {
	mu          sync.Mutex
	installed   map[string]bool
	armed, busy string
}

// firstUseProviders are the providers the card always lists, in the
// header's order, with the executable each one runs.
var firstUseProviders = []struct{ name, binary string }{
	{"codex", "codex"}, {"claude", "claude"}, {"agy", "agy"}, {"opencode", "opencode"},
}

// lookPath is the executable lookup the card uses; tests replace it.
var (
	execLookPath = exec.LookPath
	lookPath     = execLookPath
)

// loadFirstUse reads, once, what the first-use card shows about providers:
// which are installed and the store's verification overview.
func (w *Workspace) loadFirstUse(ctx context.Context) {
	installed := map[string]bool{}
	for _, p := range firstUseProviders {
		_, err := lookPath(p.binary)
		installed[p.name] = err == nil
	}
	w.firstUse.mu.Lock()
	w.firstUse.installed = installed
	w.firstUse.mu.Unlock()
	w.prov.mu.Lock()
	read := w.prov.snap.At.IsZero()
	w.prov.mu.Unlock()
	if read && ctx.Err() == nil {
		w.refreshProviders(ctx)
	}
	w.requestRepaint()
}

// installedProviders is a copy of which providers have an executable on this
// machine, as loadFirstUse found them; nil before it has run.
func (w *Workspace) installedProviders() map[string]bool {
	w.firstUse.mu.Lock()
	defer w.firstUse.mu.Unlock()
	if w.firstUse.installed == nil {
		return nil
	}
	out := make(map[string]bool, len(w.firstUse.installed))
	for k, v := range w.firstUse.installed {
		out[k] = v
	}
	return out
}

// providerChip is one provider as the card shows it.
type providerChip struct {
	name  string
	state string // verified, unverified, limited, not installed, checking, verifying
	model string
	until time.Time
}

// firstUseChips is the card's providers: the four it always lists, then
// any other the store knows, each with its readiness.
func (w *Workspace) firstUseChips(now time.Time) []providerChip {
	m := w.prov.paint()
	w.firstUse.mu.Lock()
	installed, busy := w.firstUse.installed, w.firstUse.busy
	w.firstUse.mu.Unlock()
	known := map[string]store.ProviderStatus{}
	var extra []string
	for _, p := range m.Snap.Providers {
		name := providerWorker(p.Provider)
		known[name] = p
		if !slices.ContainsFunc(firstUseProviders, func(f struct{ name, binary string }) bool { return f.name == name }) {
			extra = append(extra, name)
		}
	}
	names := make([]string, 0, len(firstUseProviders)+len(extra))
	for _, p := range firstUseProviders {
		names = append(names, p.name)
	}
	names = append(names, extra...)
	chips := make([]providerChip, 0, len(names))
	for _, name := range names {
		c := providerChip{name: name}
		p, ok := known[name]
		switch {
		case busy == name:
			c.state = "verifying"
		case ok && providerState(p, now) == "limit":
			c.state, c.until = "limited", p.LimitUntil
		case ok && providerState(p, now) == "ready":
			c.state = "verified"
		case installed != nil && !installed[name] && !ok:
			c.state = "not installed"
		case !m.Snap.Loaded && m.Snap.Err == "":
			c.state = "checking"
		default:
			c.state = "unverified"
		}
		if ok {
			c.model = p.Model
		}
		chips = append(chips, c)
	}
	return chips
}

// nextUnverified is the first provider v verifies: installed and not yet
// verified.
func nextUnverified(chips []providerChip) string {
	for _, c := range chips {
		if c.state == "unverified" {
			return c.name
		}
	}
	return ""
}

// armFirstUseVerify arms the verify of provider: Enter runs it, any other
// key cancels. The probe calls the model, so it is never one key alone.
func (w *Workspace) armFirstUseVerify(provider string) {
	if provider == "" {
		w.memoryAnswer("Every installed provider is verified or busy; 4 opens Providers.", false)
		return
	}
	for _, c := range w.firstUseChips(time.Now()) {
		if c.name != provider {
			continue
		}
		switch c.state {
		case "not installed":
			w.memoryAnswer(provider+" is not installed: put its CLI on PATH, then press v.", false)
			return
		case "limited":
			w.memoryAnswer(fmt.Sprintf("%s is in a limit window until %s; verify would fail.", provider, providerUntil(c.until, time.Now())), false)
			return
		case "verifying":
			return
		}
	}
	w.firstUse.mu.Lock()
	w.firstUse.armed = provider
	w.firstUse.mu.Unlock()
}

// firstUseArmed is the provider whose verify waits for Enter.
func (w *Workspace) firstUseArmed() string {
	w.firstUse.mu.Lock()
	defer w.firstUse.mu.Unlock()
	return w.firstUse.armed
}

// runFirstUseVerify verifies the armed provider through /harness verify,
// the path the Providers view takes, off the input loop.
func (w *Workspace) runFirstUseVerify() {
	w.firstUse.mu.Lock()
	provider := w.firstUse.armed
	w.firstUse.armed = ""
	if provider == "" || w.firstUse.busy != "" {
		w.firstUse.mu.Unlock()
		return
	}
	w.firstUse.busy = provider
	w.firstUse.mu.Unlock()
	src := w.providerSrc()
	done := func() {
		w.firstUse.mu.Lock()
		w.firstUse.busy = ""
		w.firstUse.mu.Unlock()
	}
	if src == nil {
		done()
		w.memoryAnswer("Cannot verify "+provider+" here: this session has no store.", true)
		return
	}
	model := ""
	for _, p := range w.prov.paint().Snap.Providers {
		if providerWorker(p.Provider) == provider {
			model = p.Model
		}
	}
	// The chip says "verifying" while the probe runs; the result is the
	// chip's new state and one Activity line, never an overlay.
	if !w.startBackground(context.Background(), func(ctx context.Context) {
		text, err := src.VerifyHarness(ctx, provider, model)
		done()
		if err != nil {
			w.addNote("Verify "+provider+" failed: "+err.Error(), true)
		} else {
			w.addNote(firstNonEmpty(strings.TrimSpace(text), "Verified "+provider+"."), false)
		}
		w.refreshProviders(ctx)
		w.requestRepaint()
	}) {
		done()
	}
}

// handleFirstUseKey is the first-use card's keys: v arms the verify of the
// next unverified provider and Enter runs an armed verify, any other key
// cancels it; Enter with nothing armed is the call to action, as m is.
func (w *Workspace) handleFirstUseKey(ctx context.Context, event KeyEvent) bool {
	if armed := w.firstUseArmed(); armed != "" {
		w.firstUse.mu.Lock()
		w.firstUse.armed = ""
		w.firstUse.mu.Unlock()
		if event.Type == KeyEnter {
			w.firstUse.mu.Lock()
			w.firstUse.armed = armed
			w.firstUse.mu.Unlock()
			w.runFirstUseVerify()
		} else {
			w.memoryAnswer("Verify of "+armed+" cancelled", false)
		}
		return true
	}
	switch {
	case event.Type == KeyRune && event.Rune == 'v':
		w.armFirstUseVerify(nextUnverified(w.firstUseChips(time.Now())))
		return true
	case event.Type == KeyEnter:
		w.focusChatFromBoard(ctx)
		return true
	}
	return false
}

// focusChatFromBoard is the card's call to action: the keyboard goes to the
// Marshal chat beside the Board, native or tmux; with no chat yet it says
// how to start one.
func (w *Workspace) focusChatFromBoard(ctx context.Context) {
	if w.nativeChatShown() {
		w.focusNativeChat(true)
		return
	}
	if err := w.focusMarshalChat(ctx); err != nil {
		w.setOutput(err.Error(), true)
	}
}

// firstUseStateColor is the theme colour of a provider's readiness.
var firstUseStateColor = map[string]string{"verified": "merged", "unverified": "waiting", "limited": "failed", "not installed": "dim", "checking": "dim", "verifying": "working"}

func (v boardVisual) firstUseGlyph(state string) string {
	if !v.unicode() {
		return map[string]string{"verified": "*", "limited": "x", "not installed": "o", "checking": "o", "verifying": "~"}[state] + map[bool]string{true: "~"}[firstUseStateColor[state] == "waiting"]
	}
	switch state {
	case "verified":
		return "●"
	case "limited":
		return "×"
	case "not installed", "checking":
		return "○"
	case "verifying":
		return v.frame.Spinner(true)
	}
	return "◐"
}

// firstUseHeaderLine is the header with no run: the brand and edition, the
// project, NO RUN YET, the inbox, provider health and the clock.
func (v boardVisual) firstUseHeaderLine(w int) (string, []headerSpan) {
	brand := v.bold(v.fg("white", "Marshal"))
	full := brand
	if v.edition != "" && v.edition != "Marshal" {
		full += " " + v.fg("verify", v.edition)
	}
	project := cleanBoard(v.project)
	parts := []headerPart{
		{"brand", full, brand},
	}
	if v.marshalLeads {
		parts = append(parts, headerPart{"mode", v.fg("dim", "Marshal leads"), v.fg("dim", "leads")})
	}
	parts = append(parts,
		headerPart{"project", v.fg("dim", "project ") + v.fg("accent", project), v.fg("accent", project)},
		headerPart{"state", v.bg("sel", v.fg("waiting", " NO RUN YET ")), v.fg("waiting", "NO RUN")},
	)
	if v.planning {
		parts[len(parts)-1] = headerPart{"state", v.bg("sel", v.fg("working", " PLANNING ")), v.fg("working", "PLANNING")}
	}
	if v.ownerWaits() {
		// The Marshal waits for the owner before any run exists: the
		// project contract to accept, or files changed to keep or undo.
		parts[len(parts)-1] = headerPart{"state", v.bold(v.bg("sel", v.fg("needs", " NEEDS YOU "))), v.bold(v.fg("needs", "NEEDS YOU"))}
	}
	inboxColor := "merged"
	if v.inboxCount() > 0 {
		inboxColor = "failed"
	}
	parts = append(parts, headerPart{"inbox", v.fg("dim", "inbox ") + v.fg(inboxColor, fmt.Sprint(v.inboxCount())), v.fg(inboxColor, "⚑"+fmt.Sprint(v.inboxCount()))})
	parts = append(parts, headerPart{"clock", v.fg("dim", v.now.Format("15:04:05")), ""})
	sep := v.fg("faint", " · ")
	// The edition and the project stay longest: they are what this
	// header is for while no run is open. Provider readiness and host vitals
	// are on the vitals row above. The "Marshal leads" mode part gives way
	// first.
	steps := []string{"mode", "clock", "-inbox", "-project", "-state", "inbox", "-brand", "project", "state", "brand"}
	kept := fitHeaderPartsBy(parts, w, sep, steps)
	return v.bg("band", PadCell(" "+joinHeader(kept, sep), w)), spansOf(kept, " · ", 1)
}

// firstUseCTA is the call to action's label.
const firstUseCTA = "Tell the Marshal what to build"

// paintFirstUse paints the first-use card in the Board's body: decisions
// already waiting first, then the call to action, the three steps, the
// providers and what the operator said to the Marshal. Each block keeps to
// the width it is given; rows that do not fit are left off the bottom.
func paintFirstUse(l layout.Layout, th *Theme, v boardVisual) []string {
	out := make([]string, l.Rows)
	place := func(id layout.PaneID) {
		if p, ok := l.Pane(id); ok {
			lines := v.render(p)
			for i := 0; i < p.Rect.H && i < len(lines); i++ {
				out[p.Rect.Y+i] = PadCell(lines[i], l.Cols)
			}
		}
	}
	place(layout.Vitals)
	place(layout.Header)
	place(layout.Footer)
	body := l.Body
	if body.H <= 0 {
		return out
	}
	// A tall body gives the card some air above it, as a page would.
	measure := v
	measure.hits = nil
	top := 0
	if n := len(measure.firstUseBody(body.W, body.H, body.Y)); body.H-n > 8 {
		top = min(3, (body.H-n)/6)
	}
	rows := append(make([]string, top), v.firstUseBody(body.W, body.H-top, body.Y+top)...)
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

// firstUseBody is the card's lines for a body w cells wide and h rows high
// whose first row is frame row y (for the click regions).
func (v boardVisual) firstUseBody(w, h, y int) []string {
	var out []string
	add := func(s string) { out = append(out, PadCell(s, w)) }
	// hit records a click region on the line about to be added, when that
	// line is inside the body.
	hit := func(b boardHit) {
		if len(out) < h {
			b.row = y + len(out)
			v.hit(b)
		}
	}
	gap := func() {
		if len(out) > 0 && len(out) < h {
			add("")
		}
	}
	pad := "  "
	if w < 60 {
		pad = " "
	}
	// A decision already waiting comes before anything else.
	if n := len(v.board.Inbox) + len(v.gates) + v.cardCount(); n > 0 {
		// The requirements card is tall: count the rows it draws at this
		// width (its text wraps), not the 2+3*n estimate the shorter
		// cards use.
		est := 2 + 3*n
		if v.req != nil {
			est = 1 + v.reqRows(*v.req, w, h/2-1) + 3*(n-1)
		}
		rows := min(h/2, est)
		p := layout.Pane{ID: layout.Inbox, Rect: layout.Rect{X: 0, Y: y, W: w, H: rows}, Density: layout.Compact}
		if w >= layout.FullDensity {
			p.Density = layout.Full
		}
		for _, line := range v.inbox(p) {
			add(line)
		}
		gap()
	} else {
		add("")
	}

	// The call to action: a button that focuses the chat. Once a goal is
	// given, or the Marshal already filled its requirements, there is
	// nothing left to ask for: the button goes and the chat keys stay.
	given := v.goalGiven || v.req != nil || v.planning
	if v.planning {
		add(pad + v.fg("working", v.frame.Spinner(true)) + " " + v.bold(v.fg("white", planningCard)))
	}
	if !given {
		arrow, enter := "▸", "⏎"
		if !v.unicode() {
			arrow, enter = ">", "Enter"
		}
		label := " " + arrow + " " + firstUseCTA + "   " + enter + " "
		if VisibleLen(pad+label) > w {
			label = " " + arrow + " " + firstUseCTA + " "
		}
		button := v.bold(v.bg("accent", v.fg("bg", label)))
		if v.code("accent", true) == "" {
			button = "[" + strings.TrimSpace(label) + "]"
		}
		hit(boardHit{x0: len(pad), x1: len(pad) + VisibleLen(label), kind: hitAction, id: "marshal.chat"})
		add(pad + button)
	}
	how := "Enter or F11: type it in the Marshal chat  ·  s: say it from here"
	switch {
	case v.planning:
		how = "The tasks appear here when the plan is ready  ·  F11: the Marshal chat  ·  s: say something from here"
	case given:
		how = "The Marshal has your goal  ·  F11: the Marshal chat  ·  s: say something from here"
	}
	for _, line := range wrapWords(how, w-len(pad)-1) {
		add(pad + " " + v.fg("dim", line))
	}
	gap()

	// Providers: readiness of each, and v to verify the next one.
	summary := "v verifies the next unverified · click one to verify it"
	if armed := v.armed; armed != "" {
		summary = v.fg("waiting", "Enter verifies "+armed+" (one model call) · any other key cancels")
	}
	add(pad + v.title("PROVIDERS", summary, false, w-len(pad)))
	line, x := pad, len(pad)
	flush := func() {
		if strings.TrimSpace(StripANSI(line)) != "" {
			add(line)
		}
		line, x = pad, len(pad)
	}
	for _, c := range v.chips {
		col := firstUseStateColor[c.state]
		word := c.state
		if c.state == "limited" && !c.until.IsZero() {
			word = "limited until " + c.until.In(v.now.Location()).Format("15:04")
		}
		text := v.fg(col, v.firstUseGlyph(c.state)) + " " + v.mark(c.name, "") + v.bold(v.fg("white", c.name)) + " " + v.fg(col, word)
		n := VisibleLen(text)
		if x > len(pad) && x+3+n > w {
			flush()
		}
		if x > len(pad) {
			line += "   "
			x += 3
		}
		if c.state == "unverified" {
			hit(boardHit{x0: x, x1: x + n, kind: hitVerify, id: c.name})
		}
		line += text
		x += n
	}
	flush()

	// What the operator said to the Marshal from here, and what happened
	// in the background (a verify result, a re-delivery), one line each.
	if len(v.echo) > 0 || len(v.notes) > 0 {
		gap()
		summary := "what you said to the Marshal"
		if len(v.notes) > 0 {
			summary = "what you said and what happened · click a note for all of it"
		}
		add(pad + v.title("ACTIVITY", summary, false, w-len(pad)))
		for _, r := range v.activityRows(w - len(pad)) {
			if r.note != 0 {
				hit(boardHit{x0: len(pad), x1: w, kind: hitNote, id: strconv.Itoa(r.note)})
			}
			add(pad + r.line)
		}
	}
	return out[:min(len(out), h)]
}

// wrapWords breaks s into lines of at most w cells at spaces.
func wrapWords(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case VisibleLen(line)+1+VisibleLen(word) <= w:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// trunc cuts s to w cells with an ellipsis ("." in the ASCII theme).
func (v boardVisual) trunc(s string, w int) string {
	if VisibleLen(s) <= w {
		return s
	}
	ellipsis := "…"
	if !v.unicode() {
		ellipsis = "."
	}
	r := []rune(s)
	for len(r) > 0 && VisibleLen(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + ellipsis
}
