package tui

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
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
	for i, p := range firstUseProviders {
		_, err := lookPath(p.binary)
		installed[p.name] = err == nil
		w.noteLaunchCount("providers", i+1, len(firstUseProviders))
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
	state string // verified, in use, unverified, limited, not installed, checking, verifying
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
	// A live Marshal session proves its provider works: it is in use, never
	// "unverified" (board v2 U5).
	chat := w.v2LiveChat()
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
		case chat.Live && chat.Provider == name:
			c.state = "in use"
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

// verifyNext is the provider v verifies next, "" for none: what the
// readiness checklist shows as "not checked yet", in the checklist's order
// (v2NextUnchecked).
func (w *Workspace) verifyNext() string {
	return v2NextUnchecked(w.v2MarshalInput(time.Now()))
}

// v2NextUnchecked is the first provider the v2 readiness checklist shows as
// "not checked yet": the Marshal's provider, which it lists first, then each
// installed worker provider in v2ProviderOrder.
func v2NextUnchecked(in v2MarshalIn) string {
	if prov := in.marshalProvider(); prov != "" && in.provider(prov).State == "unchecked" {
		return prov
	}
	for _, name := range v2ProviderOrder {
		if p := in.provider(name); p.Installed && p.State == "unchecked" {
			return name
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
		w.recordVerifyNote(provider, text, err)
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
		w.armFirstUseVerify(w.verifyNext())
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
var firstUseStateColor = map[string]string{"verified": "merged", "in use": "merged", "unverified": "waiting", "limited": "failed", "not installed": "dim", "checking": "dim", "verifying": "working"}

// firstUseHeaderLine is the header with no run: the brand and edition, the
// project, NO RUN YET (or PLANNING once a goal exists or architecture is
// accepted, or NEEDS YOU when a decision waits), the inbox, and the clock.
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
	if v.planning || v.goalGiven {
		// Once a goal exists (requirements, design, architecture) or the
		// architecture is accepted, the Marshal is actively planning rather
		// than sitting idle before a goal.
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

// workingGoalPrefix and workingGoalFallback are the home card's lead line once
// an active goal exists before a run starts (requirements, design, architecture).
const (
	workingGoalPrefix   = "The Marshal is working on: "
	workingGoalFallback = "The Marshal is working on your goal"
)

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

// goalElapsed formats how long the Marshal has been working on the active goal
// in compact plain units ("45s", "12m", "1h20m"), matching host_view.go.
func goalElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	switch {
	case s >= 3600:
		return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
	case s >= 60:
		return fmt.Sprintf("%dm", s/60)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
