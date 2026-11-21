package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// boardVisual paints the approved Board prototype from the read model. It owns
// presentation only; decisions and events remain with their existing owners.
type boardVisual struct {
	board  model.Board
	now    time.Time
	theme  *Theme
	frame  anim.Frame
	focus  layout.PaneID
	health map[string]string
	// vitals is the data the top-of-every-view vitals row paints.
	vitals vitalsData
	// telemetry is the help's footer line saying whether anonymous
	// telemetry is on; "" leaves the footer out.
	telemetry string
	// edition is the build named in the header; empty names none.
	edition string
	// note is the run's latest note to the operator, shown in the footer.
	note      string
	stopped   bool
	stopArmed bool
	// stall is the run's next step while nothing drives it: a returned or
	// stuck task the run waits on with no Marshal step running (after a
	// restart, say). nil when the run is moving or stopped otherwise.
	stall *runStop
	// status leads the footer instead of the run's note: the composer while
	// typing, an armed retry, or the last command's answer until the next
	// key.
	status string
	// indexing is the background project-knowledge indexing progress line,
	// shown in the footer while initial indexing runs.
	indexing string
	// hints is the hint bar: registry IDs in display order (boardHints),
	// each a button for its action.
	hints []string
	// inboxPrompt replaces the selected inbox item's action line while a
	// confirmation is in progress or the item is inspected.
	inboxPrompt string
	// host is the host watcher's latest state; nil leaves the header chip,
	// the host notice and the host key out.
	host *hostSnapshot
	// hostView paints the Host view in the body instead of the Board.
	hostView bool
	// memory is set when the Memory view can open; memView paints it in
	// the body instead of the Board.
	memory, memView bool
	// providers is set when the Providers view can open; provView paints
	// it in the body instead of the Board.
	providers, provView bool
	// verify is set when the Verify view can open; verView paints it in
	// the body.
	verify, verView bool
	// security is set when the Security view can open; secView paints it
	// in the body.
	security, secView bool
	// plan is set when the Plan view can open; planView paints it in the
	// body.
	plan, planView bool
	// viewNoRun is set when views 5-7 have no run to show: their keys
	// answer a line instead of opening them, and help leaves them out.
	viewNoRun bool
	// planning is set between the architecture's Accept and the run it
	// plans: the first-use header and card say the Marshal is planning.
	planning bool
	// mcpFailed is set once MCP has failed; until then help leaves F6 out.
	mcpFailed bool
	// arch is set when the Architecture view can open; archView paints it
	// in the body.
	arch, archView bool
	// hits, when set, collects what a click on this paint can reach.
	hits *[]boardHit
	// empty is set when no run is open: the body is the first-use card
	// (board_empty.go) and the header names the project instead of a run.
	empty bool
	// goalGiven is set once the person has given the Marshal an active,
	// undelivered goal: the home card stops asking for one and shows what
	// the Marshal is working on.
	goalGiven bool
	// goalText is the active goal's short plain-English summary (desired
	// outcome, or original request), shown on the home card while the
	// Marshal plans it before a run exists.
	goalText string
	// goalSince is when the active goal was created (or last updated when
	// CreatedAt is zero), so the home card can show how long since.
	goalSince time.Time
	// milestone is the Marshal's planning milestone for the home card, nil
	// when none is known yet.
	milestone *planningMark
	// project is the project's name, for the first-use header.
	project string
	// armed is the provider whose verify waits for Enter.
	armed string
	// notes is the background messages Activity interleaves, newest
	// first (board_notes.go).
	notes []boardNote
	// gates is what the Marshal waits for the owner to say (board_gates.go),
	// shown first in the inbox; gateArmed is the gate whose reply waits
	// for Enter.
	gates     []ownerGate
	gateArmed string
	// ask is the question the Marshal asked the owner (board_ask.go), nil
	// for none; it renders before the gates, one key or click per answer.
	ask *askCard
	// req is the requirements card the Marshal filled (board_requirements.go),
	// nil for none; it renders after the ask card, Enter confirms, e edits.
	req *reqCard
	// change is the owner's open mid-run change (board_change.go), nil for
	// none; it renders after the ask and requirements cards, one key or
	// click per action.
	change *changeCard
	// next is the "what next?" card after a run closes (board_next.go), nil
	// for none; it opens only once the watched run closed and then renders
	// first in the inbox, one key or click per action.
	next *nextCard
	// guard is the plan-only guard's alert (board_guard.go), nil for
	// none; guardArmed is set while its undo waits for Enter, guardUndo
	// what it would do; guardPrefix the project's directory in its
	// repository.
	guard       *guardAlert
	guardPrefix string
	guardArmed  bool
	guardUndo   guardUndo
	// quotaArmed is set while the all-providers-at-limit card's auto-resume
	// is armed (board_quota.go).
	quotaArmed bool
	// pick is the launch Marshal picker's candidates and selection, painted
	// in the body when the picker is open (board_picker.go).
	pick pickerView
	// marshalLeads is set when the Marshal leads acceptance (acceptance mode
	// marshal). The shipped header then shows a dim "Marshal leads" part,
	// which is the first to give way on a narrow header.
	marshalLeads bool
	// docked is set while the Marshal chat is docked beside the Board.
	docked bool
	// launchProgress is the completed startup checks line shown under the
	// opening wordmark while the launch check still runs (empty before any
	// check finishes).
	launchProgress string
}

func (v boardVisual) code(name string, background bool) string {
	// No name is the terminal's own colour: "" is not in the palette, and
	// looking it up painted black (colour 0, #000000) text.
	if v.theme == nil || v.theme.Mode == ThemeNoColor || name == "" {
		return ""
	}
	if v.theme.Mode == ThemeMonochrome {
		if background {
			return "\x1b[7m"
		}
		return ""
	}
	truecolor := truecolorTerm()
	if c, ok := boardCodes[boardCodeKey{name, background, truecolor}]; ok {
		return c
	}
	return boardColorCode(name, background, truecolor)
}

// truecolorTerm reports whether the terminal takes 24-bit colour:
// COLORTERM=truecolor or 24bit, or BOARD_TRUECOLOR=1 to force it. Both
// Boards read it on each paint.
func truecolorTerm() bool {
	ct := os.Getenv("COLORTERM")
	return ct == "truecolor" || ct == "24bit" || os.Getenv("BOARD_TRUECOLOR") == "1"
}

// boardCodeKey names one escape code of the Board's palette.
type boardCodeKey struct {
	name                  string
	background, truecolor bool
}

// boardCodes is every escape code of the palette, made once: a frame asks
// for thousands of them, and formatting each anew was most of a frame's
// allocations.
var boardCodes = func() map[boardCodeKey]string {
	m := map[boardCodeKey]string{}
	for name := range boardHex {
		for _, bg := range []bool{false, true} {
			for _, tc := range []bool{false, true} {
				m[boardCodeKey{name, bg, tc}] = boardColorCode(name, bg, tc)
			}
		}
	}
	return m
}()

// boardColorCode formats the escape code that sets name as the foreground
// or background, in 24-bit colour or the 256-colour palette.
func boardColorCode(name string, background, truecolor bool) string {
	kind := 38
	if background {
		kind = 48
	}
	if truecolor {
		h := boardHex[name]
		var r, g, b int
		fmt.Sscanf(h, "%02x%02x%02x", &r, &g, &b)
		return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", kind, r, g, b)
	}
	return fmt.Sprintf("\x1b[%d;5;%dm", kind, board256[name])
}
func (v boardVisual) fg(name, s string) string {
	if s == "" {
		return s
	}
	c := v.code(name, false)
	if c == "" {
		return s
	}
	return c + s + "\x1b[0m"
}
func (v boardVisual) bg(name, s string) string {
	if s == "" {
		return s
	}
	c := v.code(name, true)
	if c == "" {
		return s
	}
	return c + strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+c) + "\x1b[0m"
}
func (v boardVisual) bold(s string) string {
	if v.theme == nil || v.theme.Mode == ThemeNoColor {
		return s
	}
	return "\x1b[1m" + s + "\x1b[0m"
}

// taskIDWidth is the ID column's least width; maxTaskIDWidth is the longest
// id shown whole before it is cut.
const (
	taskIDWidth    = 7
	maxTaskIDWidth = 12
)

func (v boardVisual) unicode() bool { return v.theme == nil || v.theme.Unicode }
func (v boardVisual) glyph(st model.TaskState) string {
	if !v.unicode() {
		switch st {
		case model.TaskWorking, model.TaskReview:
			return "*"
		case model.TaskParked:
			return "#"
		case model.TaskRework:
			return "~"
		case model.TaskMerged, model.TaskAccepted:
			return "v"
		case model.TaskQueued:
			return "."
		default:
			return "x"
		}
	}
	switch st {
	case model.TaskWorking, model.TaskReview:
		return v.frame.Spinner(true)
	case "verifying":
		return "◎"
	case model.TaskParked:
		return "▮"
	case model.TaskRework:
		return "↻"
	case model.TaskMerged, model.TaskAccepted:
		return "✓"
	case model.TaskQueued:
		return "·"
	default:
		return "×"
	}
}
func boardStateColor(st model.TaskState) string {
	switch st {
	case model.TaskWorking, model.TaskReview:
		return "working"
	case "verifying":
		return "verify"
	case model.TaskParked, model.TaskRework, "returned":
		return "waiting"
	case model.TaskMerged, model.TaskAccepted:
		return "merged"
	case model.TaskQueued:
		return "dim"
	default:
		return "failed"
	}
}
func (v boardVisual) title(name, summary string, focused bool, w int) string {
	head := name
	if focused {
		head = "> " + head
	}
	return PadCell(v.bold(v.fg(func() string {
		if focused {
			return "accent"
		}
		return "white"
	}(), head))+" "+v.fg("dim", summary), w)
}
func (v boardVisual) render(p layout.Pane) []string {
	lines := v.renderLines(p)
	if v.unicode() {
		return lines
	}
	return v.ascii(lines, p.Rect.W)
}

// ascii is lines in the monochrome ASCII theme, each w cells wide.
func (v boardVisual) ascii(lines []string, w int) []string {
	lines = slices.Clone(lines)
	repl := strings.NewReplacer("°C", "C", "▲", "^", "·", ".", "→", "->", "›", ">", "▰", "#", "▱", "-", "◎", "*", "↻", "~", "▮", "#", "✓", "v", "○", "o", "⏎", "Enter", "▸", ">")
	for i, s := range lines {
		s = repl.Replace(s)
		s = strings.Map(func(r rune) rune {
			if r > 127 {
				return '?'
			}
			return r
		}, s)
		lines[i] = strings.ReplaceAll(PadCell(s, w), "…", ".")
	}
	return lines
}
func (v boardVisual) renderLines(p layout.Pane) []string {
	switch p.ID {
	case layout.Vitals:
		line, spans := v.vitalsLine(p.Rect.W)
		for _, sp := range spans {
			id := ""
			switch {
			case strings.HasPrefix(sp.name, "limit:"):
				if !v.providers {
					continue
				}
				id = "health"
			case sp.name == "host":
				id = "host"
			case sp.name == "lanes":
				id = "lanes"
			default:
				continue
			}
			v.hit(boardHit{row: p.Rect.Y, x0: p.Rect.X + sp.x0, x1: p.Rect.X + min(sp.x1, p.Rect.W), kind: hitChip, id: id})
		}
		return []string{line}
	case layout.Header:
		line, spans := v.headerLine(p.Rect.W)
		if v.empty {
			line, spans = v.firstUseHeaderLine(p.Rect.W)
		}
		for _, sp := range spans {
			if boardChipClickable(sp.name) && (sp.name != "health" || v.providers) {
				v.hit(boardHit{row: p.Rect.Y, x0: p.Rect.X + sp.x0, x1: p.Rect.X + min(sp.x1, p.Rect.W), kind: hitChip, id: sp.name})
			}
		}
		return []string{line}
	case layout.Footer:
		line, spans := v.footerSpans(p.Rect.W)
		for _, sp := range spans {
			v.hit(boardHit{row: p.Rect.Y, x0: p.Rect.X + sp.x0, x1: p.Rect.X + min(sp.x1, p.Rect.W), kind: hitAction, id: sp.id})
		}
		return []string{line}
	case layout.Notice:
		return []string{PadCell("Board needs more space", p.Rect.W)}
	}
	return nil
}

// headerPart is one segment of the Board header: its name in
// headerSteps, its text and the shorter text a narrow header uses.
type headerPart struct {
	name, text, short string
}

// headerSteps is how a header too narrow for every part gives way, one
// step at a time until it fits: a part is shortened ("-") or left out. The
// provider limits, host vitals and running count moved to the vitals row;
// the run's own identity, state and inbox stay longest, after the brand.
// The "Marshal leads" mode part gives way first; the state's reason (the
// final check's "re-running the checks on the merged result") goes before
// any part is left out.
var headerSteps = []string{"mode", "-run", "clock", "-budget", "-done", "-brand", "-state", "budget", "done", "-arch", "inbox", "run", "state", "arch", "brand"}

// headerSpan is where one header part landed on the line, in cells.
type headerSpan struct {
	name   string
	x0, x1 int
}

// spansOf places parts joined by sep from cell start.
func spansOf(parts []headerPart, sep string, start int) []headerSpan {
	out := make([]headerSpan, 0, len(parts))
	x := start
	for i, p := range parts {
		if i > 0 {
			x += VisibleLen(sep)
		}
		n := VisibleLen(p.text)
		out = append(out, headerSpan{p.name, x, x + n})
		x += n
	}
	return out
}

// headerLine is header with where each part landed, for the mouse.
func (v boardVisual) headerLine(w int) (string, []headerSpan) {
	b := v.board
	title := firstLine(b.Run.Title)
	if title == "" {
		title = "current run"
	} else {
		title = fmt.Sprintf("%q", title)
	}
	brand := v.bold(v.fg("white", "Marshal"))
	full := brand
	if v.edition != "" && v.edition != "Marshal" {
		full += " " + v.fg("verify", v.edition)
	}
	run := v.fg("accent", Truncate(title, 40))
	shortRun := v.fg("accent", Truncate(title, 16))
	parts := []headerPart{
		{"brand", full, brand},
	}
	if v.marshalLeads {
		parts = append(parts, headerPart{"mode", v.fg("dim", "Marshal leads"), v.fg("dim", "leads")})
	}
	parts = append(parts, []headerPart{
		{"run", run, shortRun},
		{"state", v.stateChip(), v.stateChipShort()},
		{"done", v.fg("waiting", fmt.Sprint(b.Run.Done)) + v.fg("dim", fmt.Sprintf("/%d done", b.Run.Total)), v.fg("waiting", fmt.Sprint(b.Run.Done)) + v.fg("dim", fmt.Sprintf("/%d", b.Run.Total))},
	}...)
	if b.Run.TokenLimit > 0 && b.Run.Tokens.Known {
		pct := int(b.Run.Tokens.Value * 100 / b.Run.TokenLimit)
		if pct > 100 {
			pct = 100
		}
		n := (pct + 6) / 13
		bar := v.fg("waiting", strings.Repeat("▰", n)) + v.fg("dim", strings.Repeat("▱", 8-n))
		parts = append(parts, headerPart{"budget", v.fg("dim", "budget ") + bar + fmt.Sprintf(" %d%%", pct), bar})
	} else {
		parts = append(parts, headerPart{"budget", v.fg("dim", "budget —"), v.fg("dim", "—")})
	}
	inboxColor := "merged"
	if v.inboxCount() > 0 {
		inboxColor = "failed"
	}
	parts = append(parts, headerPart{"inbox", v.fg("dim", "inbox ") + v.fg(inboxColor, fmt.Sprint(v.inboxCount())), ""})
	parts = append(parts, headerPart{"clock", v.fg("dim", v.now.Format("15:04:05")), ""})
	if w <= 100 {
		return v.narrowHeader(w, brand, full, run, shortRun, inboxColor)
	}
	kept := fitHeaderParts(parts, w, v.fg("faint", " · "))
	return v.bg("band", PadCell(" "+joinHeader(kept, v.fg("faint", " · ")), w)), spansOf(kept, " · ", 1)
}

// narrowHeaderSteps is how a header of 100 columns or fewer gives way, as
// mockups 05 and 06 draw it: the edition, the inbox label and the state's
// reason go first, then a warm host chip, the budget bar, the lanes and the done count; the run
// and its state stay longest.
var narrowHeaderSteps = []string{"-brand", "-inbox", "-state", "budget", "done", "inbox", "state", "run", "brand"}

// narrowHeader is the header at 100 columns or fewer (owner decision
// 2026-11-06, mockups 05 and 06): the run, its state, the done count and the
// budget bar stay; the provider limits and host vitals are on the vitals row.
func (v boardVisual) narrowHeader(w int, brand, full, run, shortRun, inboxColor string) (string, []headerSpan) {
	b := v.board
	parts := []headerPart{
		{"brand", full, brand},
		{"run", run, shortRun},
		{"state", v.stateChip(), v.stateChipShort()},
		{"done", v.fg("waiting", fmt.Sprint(b.Run.Done)) + v.fg("dim", fmt.Sprintf("/%d", b.Run.Total)), ""},
	}
	if b.Run.TokenLimit > 0 && b.Run.Tokens.Known {
		pct := min(int(b.Run.Tokens.Value*100/b.Run.TokenLimit), 100)
		n := (pct + 6) / 13
		parts = append(parts, headerPart{"budget", v.fg("waiting", strings.Repeat("▰", n)) + v.fg("dim", strings.Repeat("▱", 8-n)), ""})
	}
	parts = append(parts, headerPart{"inbox", v.fg("dim", "inbox ") + v.fg(inboxColor, fmt.Sprint(v.inboxCount())), v.fg(inboxColor, "⚑"+fmt.Sprint(v.inboxCount()))})
	if w <= 80 {
		parts[len(parts)-1].text = parts[len(parts)-1].short
	}
	sep := v.fg("faint", " · ")
	kept := fitHeaderPartsBy(parts, w, sep, narrowHeaderSteps)
	return v.bg("band", PadCell(" "+joinHeader(kept, sep), w)), spansOf(kept, " · ", 1)
}

func joinHeader(parts []headerPart, sep string) string {
	s := make([]string, len(parts))
	for i, p := range parts {
		s[i] = p.text
	}
	return strings.Join(s, sep)
}

// fitHeaderParts is the parts fitHeader keeps, as they are shown.
func fitHeaderParts(parts []headerPart, w int, sep string) []headerPart {
	return fitHeaderPartsBy(parts, w, sep, headerSteps)
}

// fitHeaderPartsBy is fitHeaderParts giving way by steps.
func fitHeaderPartsBy(parts []headerPart, w int, sep string, steps []string) []headerPart {
	parts = slices.Clone(parts)
	for _, step := range steps {
		if VisibleLen(" "+joinHeader(parts, sep)) <= w {
			break
		}
		name, shorten := strings.CutPrefix(step, "-")
		i := slices.IndexFunc(parts, func(p headerPart) bool { return p.name == name })
		switch {
		case i < 0:
		case !shorten:
			parts = slices.Delete(parts, i, i+1)
		case parts[i].short != "":
			parts[i].text = parts[i].short
		}
	}
	return parts
}

// footerSpan is where one hint of the footer landed, for the mouse: the
// registry action a click on it runs.
type footerSpan struct {
	x0, x1 int
	id     string
}

// footerSpans is footer with the cells each hint takes. The hints are
// v.hints, each written as its key and its registry Hint, and every one is a
// button for its action. The run's note or stop notice leads; a status line
// (typing, an armed retry, a command's answer) leads instead, with the lead
// of a confirmation, picker or composer right-aligned after it, or help after
// an answer, while they fit beside it.
func (v boardVisual) footerSpans(w int) (string, []footerSpan) {
	type item = hintItem
	var items []item
	for _, id := range v.hints {
		if k, ok := boardAction(id); ok && k.Hint != "" {
			items = append(items, item{id, v.hintKey(k.Keys), k.Hint})
		}
	}
	chip := func(it item) string {
		return v.bg("rule", v.fg("white", it.key)) + " " + v.fg("dim", it.hint)
	}
	width := func(it item) int { return VisibleLen(it.key) + 1 + VisibleLen(it.hint) }
	if v.status != "" {
		// The status wins: the items beside it drop from the right until
		// they fit at least three cells after it.
		var side []item
		if len(items) > 0 && hintHoldsKeyboard(items[0].id) {
			side = items
		} else if i := slices.IndexFunc(items, func(it item) bool { return it.id == "help" }); i >= 0 {
			side = items[i : i+1]
		}
		for len(side) > 0 {
			n := 0
			for i, it := range side {
				n += width(it)
				if i > 0 {
					n += 2
				}
			}
			if 1+VisibleLen(v.status)+3+n+1 <= w {
				break
			}
			side = side[:len(side)-1]
		}
		if len(side) == 0 {
			return PadCell(" "+v.status, w), nil
		}
		n := 0
		for i, it := range side {
			n += width(it)
			if i > 0 {
				n += 2
			}
		}
		x := w - 1 - n
		s := " " + v.status + strings.Repeat(" ", max(0, x-1-VisibleLen(v.status)))
		var spans []footerSpan
		for i, it := range side {
			if i > 0 {
				s += "  "
				x += 2
			}
			spans = append(spans, footerSpan{x0: x, x1: x + width(it), id: it.id})
			s += chip(it)
			x += width(it)
		}
		return PadCell(s, w), spans
	}
	// The run's note leads the footer, which is full width, so an id it
	// carries for the operator to type is shown whole; the hints follow
	// while they fit.
	// A stopped run's notice takes the note's place: why it stopped and
	// the exact command that moves it on.
	s := v.footerLead(w)
	items = fitHints(items, VisibleLen(" "+s), w)
	var spans []footerSpan
	for _, it := range items {
		if VisibleLen(" "+s)+width(it)+2 > w {
			break
		}
		x := 1 + VisibleLen(s)
		spans = append(spans, footerSpan{x0: x, x1: x + width(it), id: it.id})
		s += chip(it) + "  "
	}
	return PadCell(" "+s, w), spans
}

// footerLead is what leads the footer before its hints when there is no
// status: a stopped run's notice (why it stopped and the command that moves
// it on), else the run's note, else the knowledge index's progress, then a
// host notice when it fits whole in w. "" when there is none.
func (v boardVisual) footerLead(w int) string {
	var s string
	if stop, ok := v.stopNotice(); ok {
		s = v.stopLine(stop) + "   "
	} else if note := strings.TrimSpace(cleanBoard(v.note)); note != "" {
		s = v.fg("waiting", "› ") + v.quietNote(note) + "   "
	} else if indexing := strings.TrimSpace(cleanBoard(v.indexing)); indexing != "" {
		s = v.fg("working", v.frame.Spinner(true)+" ") + v.fg("dim", indexing) + "   "
	}
	if v.host != nil && v.host.Notice != "" {
		if part := v.fg("waiting", "▲ ") + cleanBoard(v.host.Notice) + "   "; VisibleLen(" "+s+part) <= w {
			s += part
		}
	}
	return s
}

func (v boardVisual) counts() (running, parked, merged int) {
	for _, t := range v.board.Tasks {
		switch t.State {
		case model.TaskWorking, model.TaskReview, "verifying":
			running++
		case model.TaskParked:
			parked++
		case model.TaskMerged:
			merged++
		}
	}
	return
}

// inboxButton is one piece of an inbox action line: a button standing for
// the key action, or the space between buttons (action 0).
type inboxButton struct {
	text   string
	action rune
}

func cleanBoard(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
}

// activityRow is one Activity line and, for a background message, the
// note a click on it shows whole.
type activityRow struct {
	at   time.Time
	line string
	note int
}

// runWordColor is the colour of a run-level change: a closed run is done,
// an approved plan starts work, an amended one waits on its new tasks.
var runWordColor = map[string]string{"closed": "merged", "approved": "working", "amended": "waiting"}

// inboxCount is what the header's inbox chip counts: every entry the inbox
// lists that waits for the person — pending requests, the owner gates still
// waiting for a reply and each open card (guard, ask, requirements, change,
// "what next?"). It is the one count the header and the layout read.
func (v boardVisual) inboxCount() int {
	return len(v.board.Inbox) + waitingGates(v.gates) + v.cardCount()
}

// cardCount is the open inbox cards: guard, ask, requirements, change and
// "what next?".
func (v boardVisual) cardCount() int {
	return v.guardCount() + v.askCount() + v.reqCount() + v.changeCount() + v.nextCount()
}
