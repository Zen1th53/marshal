package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The terminal home is one simple screen: what is happening, the one thing
// needed from the owner and where the Web Board is. Everything else (plan,
// lanes, tasks, workers, activity, cards and their details) is the Web
// Board's (internal/webboard), which shows all of it with its actions.
//
//	 MARSHAL · demo · RUNNING · 3 workers · 14:05
//
//	 Now:       3 running · 9 queued (4/12 done)
//	 From you:  nothing
//	 Web:       opened in your browser (w opens it again)
//	 ⏎ chat   w web   s say to the Marshal   q quit
//	 <status or composer>
//
// Enter always opens the Marshal chat. Only a safe continue-type action
// (retry, continue, move and continue, resume, confirm the requirements)
// gets a key, y, named on the From-you line; a decision with authority
// (allow or deny, a gate, the architecture, stopping the run) says to open
// the Web Board instead.

// homeYesQuiet is how long a second y does nothing after the first: one
// action per press, no queue of repeats.
const homeYesQuiet = 2 * time.Second

// homeKeys is the key bar, long and short.
const (
	homeKeysLong  = "⏎ chat   w web   s say to the Marshal   q quit"
	homeKeysShort = "⏎ chat  w web  s say  q quit"
)

// homePrimary is the safe action y runs now, "" for none: its words for
// the From-you line and what it does.
type homePrimary struct {
	label string
	kind  string // paused, requirements, retry, verify, telemetry
	task  string // the task a retry retries, the provider a verify checks
}

// homeIn is what the home screen's From you reads besides the view model:
// the stuck task a retry would retry and the provider a verify would check.
type homeIn struct {
	retry, verify string
}

// homePrimaryOf is b's safe action: the paused card's Enter (retry, move
// and continue, resume), the requirements card's Enter (confirm them), or,
// with no card, the retry of the stuck task the Web Board would offer.
// A decision with authority has none.
func homePrimaryOf(b vm.Board, in homeIn) homePrimary {
	switch {
	case b.Decision != nil:
		if b.Decision.Kind == decisionRequirements {
			return homePrimary{label: firstSet(cardEnterLabel(b.Decision.Actions), "confirm the requirements"), kind: decisionRequirements}
		}
		return homePrimary{}
	case b.Paused != nil:
		if l := cardEnterLabel(b.Paused.Actions); l != "" {
			return homePrimary{label: l, kind: "paused"}
		}
		return homePrimary{}
	case in.retry != "":
		return homePrimary{label: "retry " + in.retry, kind: "retry", task: in.retry}
	case !b.NowLine.FromYouOK || b.NowLine.ChatAsked:
		return homePrimary{}
	case b.Ready.TelemetryAsk:
		return homePrimary{label: "keep them on · t turns them off", kind: "telemetry"}
	case in.verify != "" && b.Top.Run == "":
		return homePrimary{label: "check " + in.verify + " (one model call)", kind: "verify", task: in.verify}
	}
	return homePrimary{}
}

// homeSignIn reports whether agy waits for its sign-in: g opens its
// window (openAgySignIn), as the readiness checklist's button did.
func homeSignIn(b vm.Board) bool {
	return slices.ContainsFunc(b.Ready.Actions, func(k vm.Key) bool { return k.Key == "g" })
}

// cardEnterLabel is the label of a card's Enter action, "" for none.
func cardEnterLabel(keys []vm.Key) string {
	for _, k := range keys {
		if k.Key == "⏎" || strings.EqualFold(k.Key, "enter") {
			return strings.TrimSpace(k.Label)
		}
	}
	return ""
}

// homeNow is the Now line: one sentence of what is happening.
func homeNow(b vm.Board) string {
	if b.LastRun {
		return b.NowLine.Work + " · tell the Marshal a new goal"
	}
	if b.Top.Run != "" {
		s := firstSet(b.NowLine.Work, "nothing running")
		if pl := b.Plan; pl.Total > 0 {
			s += fmt.Sprintf(" (%d/%d done)", pl.Done, pl.Total)
		}
		if b.Paused != nil {
			s = "paused · " + s
		}
		return s
	}
	doing := b.NowLine.Marshal
	if _, after, ok := strings.Cut(doing, " · "); ok {
		doing = after
	} else {
		doing = "waiting for a goal"
	}
	// Some states already read as a whole sentence ("the architecture is
	// ready for you to accept in the browser"); do not prefix those.
	if strings.HasPrefix(doing, "the ") || strings.Contains(doing, " is ") || strings.Contains(doing, " are ") {
		return doing
	}
	return "the Marshal is " + doing
}

// homeFromYou is the From-you line and whether it needs the owner.
func homeFromYou(b vm.Board, p homePrimary) (string, bool) {
	n := b.NowLine
	what := firstSet(n.FromYou, "nothing")
	if n.FromYouOK {
		// Nothing on a card: the line says what the key is for.
		switch p.kind {
		case "telemetry":
			return "anonymous usage counts are on (docs/telemetry.md) — y " + p.label, true
		case "retry":
			return p.task + " is stuck — y " + p.label, true
		case "verify":
			return p.task + " is not checked yet — y " + p.label, true
		case "":
			if homeSignIn(b) {
				return "agy is not signed in — g opens its sign-in window", true
			}
		}
	}
	switch {
	case n.ChatAsked:
		return what + " — ⏎ open chat", true
	case p.label != "":
		return what + " — y " + p.label, true
	case !n.FromYouOK:
		return what + " — open the web to decide (w)", true
	}
	return what, false
}

// homeWeb is the Web line. It never holds the Web Board's private link:
// that is shown only on demand (/marshal web, or w with no display).
func homeWeb(web vm.Web) string {
	switch {
	case web.NoDisplay:
		return "no display here: /marshal web shows the link to open"
	case web.Opened:
		return "opened in your browser (w opens it again)"
	}
	return "open with w"
}

// homeLine is one labelled row: " Label:     value", the value cut to fit.
func (v boardVisual) homeLine(label, value string, cols int, tone string) string {
	return v.homeLines(label, value, cols, tone, 1)[0]
}

// homeLines is a labelled value wrapped onto at most most rows under its
// first, the last cut to fit: the From-you line keeps its key in view.
func (v boardVisual) homeLines(label, value string, cols int, tone string, most int) []string {
	head := " " + label
	head += strings.Repeat(" ", max(1, 12-VisibleLen(head)))
	room := cols - VisibleLen(head)
	if room <= 0 {
		return []string{Truncate(head, cols)}
	}
	parts := wrapWords(value, room)
	if len(parts) == 0 {
		parts = []string{""}
	}
	if len(parts) > most {
		parts = append(parts[:most-1], strings.Join(parts[most-1:], " "))
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		p = Truncate(p, room)
		if tone != "" {
			p = v.fg(tone, p)
		}
		lead := strings.Repeat(" ", VisibleLen(head))
		if i == 0 {
			lead = v.fg("dim", head)
		}
		out[i] = lead + p
	}
	return out
}

// toneColor is a view-model tone as the theme's colour name.
func toneColor(t vm.Tone) string {
	switch t {
	case vm.ToneOK:
		return "merged"
	case vm.ToneWarn:
		return "waiting"
	case vm.ToneBad:
		return "failed"
	case vm.ToneInfo, vm.ToneVio:
		return "accent"
	case vm.ToneMuted:
		return "dim"
	}
	return ""
}

// homeHeader is row 0: " MARSHAL · project · STATE · N workers · clock".
func (v boardVisual) homeHeader(t vm.TopBar, cols int) string {
	sep := v.fg("dim", " · ")
	parts := []string{v.bold(v.fg("accent", "MARSHAL"))}
	if t.Project != "" {
		parts = append(parts, t.Project)
	}
	if t.State != "" {
		parts = append(parts, v.bold(v.fg(toneColor(t.StateTone), t.State)))
	}
	parts = append(parts, countOf(t.Workers, "worker"))
	if !t.Clock.IsZero() {
		parts = append(parts, t.Clock.Format("15:04"))
	}
	line := " " + strings.Join(parts, sep)
	for len(parts) > 2 && VisibleLen(line) > cols {
		parts = parts[:len(parts)-1]
		line = " " + strings.Join(parts, sep)
	}
	return Truncate(line, cols)
}

// paintHome draws the home screen in cols x rows: the header, Now, From
// you, Web, the key bar and, on the last row, the status or composer line.
// It draws down to any size, dropping the blank row and then the lower
// rows first; nothing is ever half drawn.
//
// It is called without w.mu held: the collectors read the workspace state
// through its own locked accessors (GetUIState, marshalPanel).
func (w *Workspace) paintHome(v boardVisual, cols, rows int) []string {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	b := w.boardV2()
	// The docked chat's tmux title is the MARSHAL CHAT strip.
	w.noteDockStrip(b)
	// The Web Board offers r only while it would retry a task; it is noted
	// here, on the loop (webboard_act.go).
	w.noteWebRetry()
	if b.Top.Clock.IsZero() {
		b.Top.Clock = v.now
	}
	return renderHome(v, b, homePrimaryOf(b, w.homeIn()), cols, rows)
}

// renderHome is the home screen of b in cols x rows, p its safe action. It
// reads nothing but its arguments.
func renderHome(v boardVisual, b vm.Board, p homePrimary, cols, rows int) []string {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	from, needs := homeFromYou(b, p)
	fromTone := ""
	if needs {
		fromTone = "waiting"
	}
	content := []string{v.homeHeader(b.Top, cols), "", v.homeLine("Now:", homeNow(b), cols, "")}
	content = append(content, v.homeLines("From you:", from, cols, fromTone, 3)...)
	content = append(content, v.homeLine("Web:", homeWeb(b.Web), cols, ""))
	keys := homeKeysLong
	if !v.unicode() {
		keys = strings.ReplaceAll(keys, "⏎", "Enter")
	}
	if VisibleLen(" "+keys) > cols {
		keys = homeKeysShort
		if !v.unicode() {
			keys = strings.ReplaceAll(keys, "⏎", "Enter")
		}
	}
	bottom := 1 // the status row
	if rows >= 4 {
		bottom = 2 // and the key bar
	}
	if room := rows - bottom; len(content) > room {
		content = append(content[:1], content[2:]...)
		content = content[:max(0, min(len(content), room))]
	}
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = strings.Repeat(" ", cols)
	}
	for i, s := range content {
		lines[i] = PadCell(s, cols)
	}
	if bottom == 2 {
		lines[rows-2] = PadCell(v.fg("dim", " "+Truncate(keys, cols-1)), cols)
	}
	if v.status == "" {
		v.status = v.inboxPrompt
	}
	if v.status != "" {
		lines[rows-1] = PadCell(" "+Truncate(v.status, cols-1), cols)
	} else if lead := strings.TrimSpace(v.footerLead(cols)); lead != "" {
		lines[rows-1] = PadCell(" "+Truncate(lead, cols-1), cols)
	}
	if !v.unicode() {
		return v.ascii(lines, cols)
	}
	return lines
}

// handleHomeKey is a key on the home screen; it reports whether it took it.
// Enter opens the Marshal chat, always; y runs the one safe action named on
// the From-you line; w opens the Web Board; s writes to the Marshal; q
// quits; the digits and b and h open the views. Any other key goes on to
// the palette, the shortcuts and the composer: no key acts on a card the
// screen does not draw.
func (w *Workspace) handleHomeKey(event KeyEvent) bool {
	ctx := context.Background()
	if event.Type == KeyEnter {
		w.focusChatFromBoard(ctx)
		return true
	}
	if event.Type != KeyRune {
		return false
	}
	// The requirements card keeps its line edit in the terminal: e, then
	// the line's letter, puts the line in the composer (the Web Board can
	// only confirm them all).
	if c := w.currentReq(); c != nil && c.editing {
		return w.handleRequirementsKey(event)
	}
	switch event.Rune {
	case 'y':
		w.homeYes(ctx)
		return true
	case 't', 'g', 'e':
		// Each acts only while its prompt is on the From-you line.
		b := w.boardV2()
		if !homeKeyShown(b, homePrimaryOf(b, w.homeIn()), event.Rune) {
			return false
		}
		switch event.Rune {
		case 't':
			w.chooseTelemetry(ctx, false)
		case 'g':
			w.openAgySignIn(ctx)
		case 'e':
			return w.handleRequirementsKey(event)
		}
		return true
	case 'w':
		w.openWebBoardKey()
		return true
	case 's':
		w.openSay()
		return true
	case 'q':
		w.requestExit()
		return true
	case 'a':
		// The project architecture in the browser; it never accepts one.
		response, err := w.marshalArchitecture(ctx, "", "")
		w.recordCommandResult("/marshal architecture", response, err)
		return true
	}
	return w.openViewKey(event)
}

// homeIn is the stuck task y would retry (retryTarget) and, with no run,
// the provider y would check: the one whose check the agy sign-in armed,
// or the readiness checklist's next "not checked yet".
func (w *Workspace) homeIn() homeIn {
	var in homeIn
	if id, why := retryTarget(w); why == "" {
		in.retry = id
	}
	if w.marshalPanel() == nil {
		in.verify = firstSet(w.firstUseArmed(), v2NextUnchecked(w.v2MarshalInput(time.Now())))
	}
	return in
}

// homeKeyShown reports whether a home key's prompt is on the From-you line
// now: t only with the usage counts, g only with agy's sign-in, e only with
// the requirements. A key whose prompt is not drawn types into the composer.
func homeKeyShown(b vm.Board, p homePrimary, r rune) bool {
	switch r {
	case 't':
		return p.kind == "telemetry"
	case 'g':
		return p.kind == "" && b.NowLine.FromYouOK && homeSignIn(b)
	case 'e':
		return p.kind == decisionRequirements
	}
	return false
}

// homeYesReady reports whether y has a safe action to run now.
func (w *Workspace) homeYesReady() bool {
	if !w.boardVisible() || w.anyBodyView() {
		return false
	}
	return homePrimaryOf(w.boardV2(), w.homeIn()).kind != ""
}

// homeYes is y: the safe action the From-you line names, once. A second y
// within homeYesQuiet does nothing more; with no safe action y says where
// the decision is made.
func (w *Workspace) homeYes(ctx context.Context) {
	now := time.Now()
	ui := &w.boardUI
	if !ui.homeYesAt.IsZero() && now.Sub(ui.homeYesAt) < homeYesQuiet {
		return
	}
	b := w.boardV2()
	p := homePrimaryOf(b, w.homeIn())
	switch p.kind {
	case "":
		switch {
		case b.NowLine.ChatAsked:
			w.setOutput("The Marshal asked you something in its chat: Enter opens it.", false)
		case !b.NowLine.FromYouOK:
			w.setOutput("Decide this on the Web Board: w opens it.", false)
		default:
			w.setOutput("Nothing waits for you.", false)
		}
		return
	case "retry":
		ui.homeYesAt = now
		w.retryAndContinue(ctx, p.task)
	case "telemetry":
		ui.homeYesAt = now
		w.chooseTelemetry(ctx, true)
	case "verify":
		ui.homeYesAt = now
		w.firstUse.mu.Lock()
		w.firstUse.armed = p.task
		w.firstUse.mu.Unlock()
		w.runFirstUseVerify()
		w.setOutput("Checking "+p.task+": one model call; Activity says how it went.", false)
	default:
		ui.homeYesAt = now
		if !w.handleV2CardKey(KeyEvent{Type: KeyEnter}) {
			w.setOutput("That changed on the Board; look again.", false)
		}
	}
}
