package tui

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/redaction"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// boardVisual paints the approved Board prototype from the read model. It owns
// presentation only; decisions and events remain with their existing owners.
type boardVisual struct {
	tails                       func(string) []string
	tailWant                    int
	board                       model.Board
	now                         time.Time
	theme                       *Theme
	frame                       anim.Frame
	selectedTask, selectedInbox string
	gateInput                   string
	focus                       layout.PaneID
	health                      map[string]string
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
	// status leads the footer instead of the run's note: the composer while
	// typing, an armed retry, or the last command's answer until the next
	// key.
	status string
	// hints is the hint bar: registry IDs in display order (boardHints),
	// each a button for its action.
	hints []string
	// inboxPrompt replaces the selected inbox item's action line while a
	// confirmation is in progress or the item is inspected.
	inboxPrompt  string
	selectedLane string
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
	// goalGiven is set once the person has given the Marshal a goal (the
	// active Goal): the first-use card stops asking for one.
	goalGiven bool
	// project is the project's name, for the first-use header.
	project string
	// chips is each provider's readiness for the first-use card; armed is
	// the provider whose verify waits for Enter.
	chips []providerChip
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
	// echo is what the operator said to the Marshal from the composer,
	// newest first, with where each message is.
	echo []sayEcho
}

func (v boardVisual) code(name string, background bool) string {
	if v.theme == nil || v.theme.Mode == ThemeNoColor {
		return ""
	}
	if v.theme.Mode == ThemeMonochrome {
		if background {
			return "\x1b[7m"
		}
		return ""
	}
	truecolor := os.Getenv("COLORTERM") == "truecolor" || os.Getenv("COLORTERM") == "24bit" || os.Getenv("BOARD_TRUECOLOR") == "1"
	if c, ok := boardCodes[boardCodeKey{name, background, truecolor}]; ok {
		return c
	}
	return boardColorCode(name, background, truecolor)
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

// pulse draws a whole row bold, carrying the weight across the colour
// resets inside it.
func (v boardVisual) pulse(s string) string {
	if v.theme == nil || v.theme.Mode == ThemeNoColor {
		return s
	}
	return "\x1b[1m" + strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m\x1b[1m") + "\x1b[0m"
}

// taskIDWidth is the ID column's least width; maxTaskIDWidth is the longest
// id shown whole before it is cut.
const (
	taskIDWidth    = 7
	maxTaskIDWidth = 12
)

// truncateLeft shortens plain text to width columns by dropping its start,
// so the end, where a path's file name is, stays readable.
func truncateLeft(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if VisibleLen(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(s)
	used := 1
	i := len(runes)
	for i > 0 && used+RuneWidth(runes[i-1]) <= width {
		i--
		used += RuneWidth(runes[i])
	}
	return "…" + string(runes[i:])
}

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
	case layout.Tasks:
		return v.tasks(p)
	case layout.Lanes:
		return v.lanes(p)
	case layout.Inbox:
		return v.inbox(p)
	case layout.Activity:
		return v.activity(p)
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

// header paints the run's head line, giving way by headerSteps when it is
// too narrow.
func (v boardVisual) header(w int) string {
	line, _ := v.headerLine(w)
	return line
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

// fitHeader joins the parts with sep and takes headerSteps until the line
// fits in w cells with its leading space.
func fitHeader(parts []headerPart, w int, sep string) string {
	return joinHeader(fitHeaderParts(parts, w, sep), sep)
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

func (v boardVisual) footer(w int) string {
	line, _ := v.footerSpans(w)
	return line
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
	var s string
	if stop, ok := v.stopNotice(); ok {
		s = v.stopLine(stop) + "   "
	} else if note := strings.TrimSpace(cleanBoard(v.note)); note != "" {
		s = v.fg("waiting", "› ") + note + "   "
	}
	// A host notice follows the run's note, and only when it fits whole.
	if v.host != nil && v.host.Notice != "" {
		if part := v.fg("waiting", "▲ ") + cleanBoard(v.host.Notice) + "   "; VisibleLen(" "+s+part) <= w {
			s += part
		}
	}
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
func (v boardVisual) tasks(p layout.Pane) []string {
	if p.Density == layout.Brief {
		return v.tasksBrief(p)
	}
	w, h := p.Rect.W, p.Rect.H
	_, park, merged := v.counts()
	run := v.running()
	if v.board.Run.Done > 0 {
		merged = v.board.Run.Done
	}
	summary := fmt.Sprintf("%s · %d running · %d parked · %d merged", count(v.board.Run.Total, "task", "tasks"), run, park, merged)
	out := []string{v.title("TASKS", summary, v.focus == layout.Tasks, w)}
	if h == 1 {
		return out
	}
	compact := w < 74
	// The ID column fits the longest id, up to maxTaskIDWidth, so an id is
	// shown whole; the scope column gives up the room.
	idW := taskIDWidth
	for _, t := range v.board.Tasks {
		idW = max(idW, min(VisibleLen(t.ID), maxTaskIDWidth)+2)
	}
	if compact {
		// A narrow pane splits what the fixed columns leave between the
		// id and the scope, so neither is cut to a stub.
		idW = min(idW, max(taskIDWidth, (w-37)/2+1))
	}
	// The sent back column is there only once a task was sent back; until
	// then the scope takes its cells.
	backW := 0
	for _, t := range v.board.Tasks {
		if t.Returns > 0 {
			backW = 12
			break
		}
	}
	scopeW := max(1, w-44-backW-(idW-taskIDWidth))
	if compact {
		scopeW = max(1, w-35-backW-(idW-taskIDWidth))
	}
	backHead := ""
	if backW > 0 {
		backHead = PadCell("sent back", backW)
	}
	var head string
	if compact {
		head = "  " + PadCell("ID", idW) + PadCell("state", 12) + PadCell("worker", 13) + PadCell("scope", scopeW) + " " + backHead
	} else {
		head = "  " + PadCell("ID", idW) + PadCell("state", 13) + PadCell("worker", 13) + PadCell("scope", scopeW) + " " + PadCell("elapsed", 8) + backHead
	}
	out = append(out, v.fg("dim", PadCell(head, w)))
	visible := max(0, h-2)
	if len(v.board.Tasks) > visible {
		visible = max(0, visible-1)
	}
	start := 0
	for i, t := range v.board.Tasks {
		if t.ID == v.selectedTask && i >= visible {
			start = i - visible + 1
			break
		}
	}
	start = min(start, max(0, len(v.board.Tasks)-visible))
	for _, t := range v.board.Tasks[start:] {
		if len(out) >= h || len(out)-2 >= visible {
			break
		}
		mark := "  "
		sel := t.ID == v.selectedTask
		if sel {
			mark = v.fg("accent", "▸") + " "
			if !v.unicode() {
				mark = "> "
			}
		}
		// A merged task reads as done: every cell dim but its state.
		done := t.State == model.TaskMerged
		dim := func(s string) string {
			if done {
				return v.fg("dim", s)
			}
			return s
		}
		state := v.fg(boardStateColor(t.State), v.glyph(t.State)+" "+string(t.State))
		if v.board.Stalled(t) {
			// Nothing is behind the task: drawn still, never spinning.
			g := "◌"
			if !v.unicode() {
				g = "!"
			}
			state = v.fg("waiting", g+" stalled")
		}
		worker := t.Worker
		if worker == "" {
			worker = "—"
		}
		if !v.unicode() && worker == "—" {
			worker = "-"
		}
		worker = v.mark(providerOf(worker), "") + worker
		// A long scope keeps its end, where the file name is.
		scope := truncateLeft(cleanBoard(strings.Join(t.Scope, ", ")), scopeW)
		elapsed := "—"
		if d, ok := t.Elapsed(v.now); ok {
			elapsed = fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
		}
		rw := ""
		if t.Returns > 0 {
			times := "×"
			if !v.unicode() {
				times = "x"
			}
			rw = fmt.Sprintf("sent back %d%s", t.Returns, times)
			if t.State == model.TaskRework {
				// A task sent back for rework shows how many returns it used.
				rw = v.fg("waiting", rw)
			}
		}
		var line string
		id := dim(PadCell(Truncate(t.ID, idW-1), idW))
		if compact {
			line = mark + id + PadCell(state, 12) + dim(PadCell(worker, 13)) + PadCell(v.fg("dim", scope), scopeW) + " " + dim(PadCell(rw, backW))
		} else {
			line = mark + id + PadCell(state, 13) + dim(PadCell(worker, 13)) + PadCell(v.fg("dim", scope), scopeW) + " " + PadCell(v.fg("dim", elapsed), 8) + dim(PadCell(rw, backW))
		}
		line = PadCell(line, w)
		if v.frame.Pulsing(t.ID) {
			// The task's state changed under Pulse ago.
			line = v.pulse(line)
		}
		if sel {
			line = v.bg("sel", line)
		}
		v.hit(boardHit{row: p.Rect.Y + len(out), x0: p.Rect.X, x1: p.Rect.X + w, kind: hitTask, id: t.ID})
		out = append(out, line)
	}
	if len(v.board.Tasks) > visible && len(out) < h {
		out = append(out, PadCell(fmt.Sprintf("… %d more", len(v.board.Tasks)-visible), w))
	}
	return out
}

// tasksBrief is the tasks pane of a narrow Home (mockups 05 and 06): the
// heading counts tasks by state glyph, and each row is the id, the state
// glyph, the worker when there is room and the scope without "internal/".
func (v boardVisual) tasksBrief(p layout.Pane) []string {
	w, h := p.Rect.W, p.Rect.H
	type count struct {
		st model.TaskState
		n  int
	}
	var counts []count
	for _, st := range []model.TaskState{model.TaskWorking, model.TaskParked, model.TaskRework, "verifying", model.TaskMerged, model.TaskQueued} {
		n := 0
		for _, t := range v.board.Tasks {
			if t.State == st || (st == model.TaskWorking && t.State == model.TaskReview) {
				n++
			}
		}
		if n > 0 {
			counts = append(counts, count{st, n})
		}
	}
	var sum []string
	if v.board.Run.Total > 0 {
		sum = append(sum, fmt.Sprint(v.board.Run.Total))
	}
	for _, c := range counts {
		sum = append(sum, fmt.Sprint(c.n)+v.fg(boardStateColor(c.st), v.glyph(c.st)))
	}
	out := []string{v.title("TASKS", strings.Join(sum, v.fg("dim", " · ")), v.focus == layout.Tasks, w)}
	if h == 1 {
		return out
	}
	visible := h - 1
	if len(v.board.Tasks) > visible {
		visible--
	}
	start := 0
	for i, t := range v.board.Tasks {
		if t.ID == v.selectedTask && i >= visible {
			start = i - visible + 1
			break
		}
	}
	start = min(start, max(0, len(v.board.Tasks)-visible))
	idW := taskIDWidth
	for _, t := range v.board.Tasks {
		idW = max(idW, min(VisibleLen(t.ID), maxTaskIDWidth)+1)
	}
	withWorker := w >= 90
	for _, t := range v.board.Tasks[start:] {
		if len(out)-1 >= visible {
			break
		}
		sel := t.ID == v.selectedTask
		mark := "  "
		if sel {
			mark = v.fg("accent", "▸") + " "
			if !v.unicode() {
				mark = "> "
			}
		}
		line := mark + v.fg("accent", PadCell(Truncate(t.ID, idW-1), idW)) + v.fg(boardStateColor(t.State), v.glyph(t.State)) + " "
		if withWorker {
			worker := t.Worker
			if worker == "" {
				worker = "—"
			}
			line += PadCell(v.mark(providerOf(worker), "")+worker, 13)
		}
		scope := strings.TrimPrefix(cleanBoard(strings.Join(t.Scope, ", ")), "internal/")
		line += v.fg("dim", truncateLeft(scope, max(1, w-VisibleLen(line))))
		line = PadCell(line, w)
		if sel {
			line = v.bg("sel", line)
		}
		v.hit(boardHit{row: p.Rect.Y + len(out), x0: p.Rect.X, x1: p.Rect.X + w, kind: hitTask, id: t.ID})
		out = append(out, line)
	}
	if n := len(v.board.Tasks) - (len(out) - 1); n > 0 && len(out) < h {
		out = append(out, PadCell(v.fg("dim", fmt.Sprintf("  … %d more  (⏎ inspect)", n)), w))
	}
	return out
}

func (v boardVisual) lanes(p layout.Pane) []string {
	w, h := p.Rect.W, p.Rect.H
	by := map[string][2]int{}
	for _, l := range v.board.Lanes {
		pr := l.Provider
		if pr == "" {
			pr = strings.Split(l.Worker, "-")[0]
		}
		c := by[pr]
		if l.State != model.LaneIdle {
			c[0]++
		}
		c[1]++
		by[pr] = c
	}
	names := []string{"codex", "claude", "agy", "opencode"}
	var parts []string
	for _, n := range names {
		if c, ok := by[n]; ok {
			parts = append(parts, fmt.Sprintf("%s%s %d/%d", v.mark(n, ""), n, c[0], c[1]))
		}
	}
	busy := v.running()
	summary := "idle"
	if busy > 0 {
		summary = fmt.Sprintf("%d running", busy)
	}
	if len(parts) > 0 {
		summary += " · " + strings.Join(parts, " · ")
	}
	if busy == 0 && v.board.Run.State == marshal.Verifying {
		// No worker runs while the merged result is checked again: the
		// lanes say what the run waits for, where a narrow header has no
		// room for it.
		if word, _ := v.runWord(); strings.Contains(word, ": ") {
			summary += " · " + word
		}
	}
	out := []string{v.title("LANES", summary, v.focus == layout.Lanes, w)}
	if h == 1 && p.Density == layout.Strip && len(v.board.Lanes) > 0 {
		return []string{v.laneChips(p)}
	}
	if h == 1 {
		return out
	}
	out = append(out, v.fg("dim", PadCell("  worker       task    elapsed tokens        last action", w)))
	bars := "▁▂▃▄▅▆▇█"
	for _, l := range v.board.Lanes {
		if len(out) >= h {
			break
		}
		st := model.TaskState(l.State)
		for _, task := range v.board.Tasks {
			if task.ID == l.TaskID && task.State == "verifying" {
				st = "verifying"
				break
			}
		}
		g := v.glyph(st)
		if l.State == model.LaneIdle {
			g = "○"
			if !v.unicode() {
				g = "o"
			}
		}
		sp := l.Spark()
		var spark string
		for _, n := range sp {
			if v.unicode() {
				spark += string([]rune(bars)[n])
			} else {
				spark += string("._-=+*##"[n])
			}
		}
		el := "—"
		if !l.Started.IsZero() && l.State != model.LaneParked {
			d := v.now.Sub(l.Started)
			el = fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
		}
		task := l.TaskID
		if l.State == model.LaneIdle {
			task = "—"
		}
		act := strings.TrimSpace(strings.ReplaceAll(redaction.RedactContent(l.Last, nil), "\n", " "))
		act = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, act)
		worker := PadCell(v.mark(providerOf(l.Worker), "")+l.Worker, 13)
		if l.State == model.LaneWorking {
			// A lane at work stands out by name, as in the prototype.
			worker = v.bold(v.fg("white", PadCell(v.mark(providerOf(l.Worker), "")+l.Worker, 13)))
		}
		tok := fmt.Sprintf("%dk", l.Tokens/1000)
		line := v.fg(boardStateColor(st), g) + " " + worker + v.fg("accent", PadCell(task, 8)) + v.fg("dim", PadCell(el, 8)) + v.fg(boardStateColor(st), spark) + " " + v.fg("dim", strings.Repeat(" ", max(0, 4-VisibleLen(tok)))+tok) + "  " + v.fg(laneActColor(l), act)
		line = PadCell(line, w)
		if v.selectedLane != "" && l.Worker == v.selectedLane {
			line = v.bg("sel", line)
		}
		v.hit(boardHit{row: p.Rect.Y + len(out), x0: p.Rect.X, x1: p.Rect.X + w, kind: hitLane, id: l.Worker})
		out = append(out, line)
	}
	if len(v.board.Lanes) > len(out)-2 && len(out) == h {
		out[h-1] = PadCell(fmt.Sprintf("… %d more", len(v.board.Lanes)-h+3), w)
		v.unhit(p.Rect.Y + h - 1)
	}
	if p.Density != layout.Strip && v.tails != nil {
		// Share the rows left after lane rows between captured running lanes.
		// Allocate one line to each lane first, then a second and third line
		// in rounds so a short pane does not spend its tail space on one lane.
		tails := make([][]string, len(v.board.Lanes))
		for i := range v.board.Lanes {
			tails[i] = v.tails(v.board.Lanes[i].Pane)
		}
		// Rebuild the lane section in order, placing each tail beneath its row.
		laneRows := make([]string, 0, len(v.board.Lanes))
		// Lane rows were appended after the two heading rows above.
		laneRows = append(laneRows, out[2:]...)
		out = out[:2]
		available := max(0, h-len(out))
		shown := min(len(laneRows), available)
		minTails := func(n int) int {
			count := 0
			for _, tail := range tails[:n] {
				if len(tail) > 0 {
					count++
				}
			}
			return count
		}
		// Keep a lane and one tail row together. If they cannot all fit,
		// reduce the number of lanes shown before giving any lane zero lines.
		for shown > 0 && shown+minTails(shown) > available {
			shown--
		}
		allocated := make([]int, len(tails))
		remaining := available - shown
		for i := 0; i < shown; i++ {
			if len(tails[i]) > 0 {
				allocated[i] = 1
				remaining--
			}
		}
		for row := 1; row < laneTailRows && remaining > 0; row++ {
			for i := 0; i < shown && remaining > 0; i++ {
				if len(tails[i]) > row {
					allocated[i]++
					remaining--
				}
			}
		}
		for i := 0; i < shown; i++ {
			out = append(out, laneRows[i])
			for j := 0; j < allocated[i] && len(out) < h; j++ {
				out = append(out, PadCell("  "+redaction.RedactContent(tails[i][j], nil), w))
			}
		}
	}
	return out
}

// laneActColor is the last-action colour: a worker that reported itself
// blocked or failing over OSC 7501 says so in the waiting or failed colour,
// next to the word, never by colour alone.
func laneActColor(l model.Lane) string {
	switch l.Status {
	case "blocked":
		return "waiting"
	case "error":
		return "failed"
	}
	return "dim"
}

func (v boardVisual) inbox(p layout.Pane) []string {
	w, h := p.Rect.W, p.Rect.H
	summary := fmt.Sprintf("%d waiting · nothing blocks", len(v.board.Inbox))
	if len(v.board.Inbox) == 0 {
		summary = "empty · no decision waiting"
	}
	stop, stopped := v.stopNotice()
	// The topmost open card names the summary, as they render: the "what
	// next?" card over ask over requirements over change.
	if v.change != nil && !stopped {
		summary = "the Marshal paused the affected tasks"
		if m := len(v.board.Inbox); m > 0 {
			summary += fmt.Sprintf(" · %d more waiting", m)
		}
	}
	if v.req != nil && !stopped {
		summary = "the Marshal filled the requirements · you confirm or edit"
		if m := len(v.board.Inbox); m > 0 {
			summary += fmt.Sprintf(" · %d more waiting", m)
		}
	}
	if v.ask != nil && !stopped {
		summary = "the Marshal waits for your answer"
		if m := len(v.board.Inbox); m > 0 {
			summary += fmt.Sprintf(" · %d more waiting", m)
		}
	}
	if v.next != nil && !stopped {
		summary = "this run is done"
		if m := len(v.board.Inbox); m > 0 {
			summary += fmt.Sprintf(" · %d waiting", m)
		}
	}
	if n := waitingGates(v.gates); n > 0 && !stopped {
		summary = "the Marshal waits for your reply"
		if m := len(v.board.Inbox); m > 0 {
			summary += fmt.Sprintf(" · %d more waiting", m)
		}
	}
	if v.guard != nil && waitingGates(v.gates) == 0 && !stopped {
		summary = "project files changed outside a run"
		if v.guard.byMarshal {
			summary = "the Marshal changed project files"
		}
		if m := len(v.board.Inbox); m > 0 {
			summary += fmt.Sprintf(" · %d more waiting", m)
		}
	}
	if stopped {
		summary = "the run needs you"
		if n := len(v.board.Inbox); n > 0 {
			summary += fmt.Sprintf(" · %d waiting", n)
		}
	}
	out := []string{v.title("INBOX", summary, v.focus == layout.Inbox, w)}
	// The "what next?" card leads the inbox when a run has just closed: the
	// run's report is in the chat, and this card states its counts and the
	// one key for the next goal (board_next.go).
	if v.next != nil && len(out) < h {
		lines := v.nextEntry(*v.next, p.Rect.X, p.Rect.Y+len(out), w)
		if len(out)+len(lines) > h {
			v.unhit(p.Rect.Y + len(out) + len(lines) - 1)
		}
		out = append(out, lines...)
		out = out[:min(len(out), h)]
	}
	if stopped && h > 1 {
		// The run's stop leads the inbox: it is what holds everything
		// else up. The all-providers-at-limit pause shows its own card
		// (board_quota.go); any other stop types the command to run.
		if v.quotaCardActive() {
			out = append(out, v.quotaEntry(p.Rect.X, p.Rect.Y+len(out), w)...)
		} else {
			out = append(out, v.stopEntry(stop, p.Rect.X, p.Rect.Y+len(out), w)...)
		}
		out = out[:min(len(out), h)]
	}
	// Project files changed outside a run come next: the Marshal plans
	// and workers implement (board_guard.go).
	if v.guard != nil && len(out) < h {
		lines := v.guardEntry(p.Rect.X, p.Rect.Y+len(out), w)
		if len(out)+len(lines) > h {
			v.unhit(p.Rect.Y + len(out) + 1)
		}
		out = append(out, lines...)
		out = out[:min(len(out), h)]
	}
	// A question the Marshal asked the owner is the first inbox entry: its
	// keys come first too, so it sits on top. One key or one click sends the
	// chosen answer to the chat (board_ask.go).
	if v.ask != nil && len(out) < h {
		lines := v.askEntry(*v.ask, p.Rect.X, p.Rect.Y+len(out), w)
		if len(out)+len(lines) > h {
			v.unhit(p.Rect.Y + len(out) + len(lines) - 1)
		}
		out = append(out, lines...)
		out = out[:min(len(out), h)]
	}
	// The requirements card comes next: the a–h lines the Marshal filled,
	// Enter to confirm, e to edit (board_requirements.go).
	if v.req != nil && len(out) < h {
		lines := v.requirementsEntry(*v.req, p.Rect.X, p.Rect.Y+len(out), w, h-len(out))
		for len(out)+len(lines) > h && len(lines) > 0 {
			v.unhit(p.Rect.Y + len(out) + len(lines) - 1)
			lines = lines[:len(lines)-1]
		}
		out = append(out, lines...)
		out = out[:min(len(out), h)]
	}
	// The owner's open mid-run change comes after the ask and requirements
	// cards: apply it, or keep the current goal (board_change.go).
	if v.change != nil && len(out) < h {
		lines := v.changeEntry(*v.change, p.Rect.X, p.Rect.Y+len(out), w)
		if len(out)+len(lines) > h {
			v.unhit(p.Rect.Y + len(out) + len(lines) - 1)
		}
		out = append(out, lines...)
		out = out[:min(len(out), h)]
	}
	// What the Marshal waits for the owner to say comes next: nothing
	// moves until the owner answers it (board_gates.go).
	for _, g := range v.gates {
		if len(out) >= h {
			break
		}
		lines := v.gateEntry(g, p.Rect.X, p.Rect.Y+len(out), w)
		if len(out)+len(lines) > h {
			v.unhit(p.Rect.Y + len(out) + 1)
		}
		out = append(out, lines...)
		out = out[:min(len(out), h)]
	}
	// The selected decision sits on the selection background, its action
	// line with it, as the prototype draws it.
	selFrom, selTo := -1, -1
	for _, it := range v.board.Inbox {
		if selFrom >= 0 && selTo < 0 {
			selTo = len(out)
		}
		if len(out) >= h {
			break
		}
		r := it.Request
		hard := it.Gate == model.GateTyped
		kind := string(it.Kind)
		if hard {
			kind = "hard gate"
		}
		color := "needs"
		if hard {
			color = "failed"
		}
		join := " → "
		if hard {
			join = " · "
		}
		// The flag is the mockups' ⚑: orange for a decision, red for a
		// hard gate, "!" in the ASCII theme.
		flag := "⚑ "
		if !v.unicode() {
			flag = "! "
		}
		line := v.fg(color, flag) + v.fg(color, r.TaskID) + v.fg("dim", " "+kind+join) + v.bold(v.fg("white", cleanBoard(r.Object))) + "  " + v.fg("dim", cleanBoard(r.Reason))
		key := it.Request.Key()
		v.hit(boardHit{row: p.Rect.Y + len(out), x0: p.Rect.X, x1: p.Rect.X + w, kind: hitInbox, id: key})
		if key == v.selectedInbox {
			selFrom = len(out)
		}
		out = append(out, PadCell(line, w))
		if key == v.selectedInbox && v.inboxPrompt != "" {
			if len(out) < h {
				out = append(out, PadCell("    "+v.fg("waiting", cleanBoard(v.inboxPrompt)), w))
			}
			continue
		}
		// buttons lays out an action line and records each button, a
		// region whose click opens that action's confirmation.
		buttons := func(parts ...inboxButton) string {
			s, x := "    ", 4
			for _, b := range parts {
				s += b.text
				if b.action != 0 {
					v.hit(boardHit{row: p.Rect.Y + len(out), x0: p.Rect.X + x, x1: p.Rect.X + min(w, x+VisibleLen(b.text)), kind: hitInbox, id: key, action: b.action})
				}
				x += VisibleLen(b.text)
			}
			return s
		}
		if hard {
			if len(out) < h {
				typed := v.gateInput
				if typed == "" {
					typed = it.Canonical
				}
				out = append(out, PadCell(buttons(inboxButton{v.fg("dim", "confirm › ") + v.bold(typed), 'a'}, inboxButton{" ", 0}, inboxButton{v.bg("rule", "[d]") + " deny", 'd'}), w))
			}
		} else if key == v.selectedInbox && len(out) < h {
			out = append(out, PadCell(buttons(inboxButton{v.bg("rule", "[a]") + " allow once", 'a'}, inboxButton{"  ", 0}, inboxButton{v.bg("rule", "[A]") + " allow for run", 'A'}, inboxButton{"  ", 0}, inboxButton{v.bg("rule", "[d]") + " deny", 'd'}, inboxButton{"  ", 0}, inboxButton{v.bg("rule", "[i]") + " inspect", 'i'}), w))
		}
	}
	if selFrom >= 0 && selTo < 0 {
		selTo = len(out)
	}
	for j := selFrom; selFrom >= 0 && j < selTo; j++ {
		out[j] = v.bg("sel", out[j])
	}
	return out
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
func (v boardVisual) activity(p layout.Pane) []string {
	w, h := p.Rect.W, p.Rect.H
	out := []string{v.title("ACTIVITY", "state changes only · full log in inspector", v.focus == layout.Activity, w)}
	for _, r := range v.activityRows(w) {
		if len(out) >= h {
			break
		}
		if r.note != 0 {
			v.hit(boardHit{row: p.Rect.Y + len(out), x0: p.Rect.X, x1: p.Rect.X + w, kind: hitNote, id: strconv.Itoa(r.note)})
		}
		out = append(out, PadCell(r.line, w))
	}
	return out
}

// activityRow is one Activity line and, for a background message, the
// note a click on it shows whole.
type activityRow struct {
	at   time.Time
	line string
	note int
}

// activityRows is Activity newest first: the run's state changes, what the
// operator said to the Marshal and the background messages, interleaved by
// time. On a tie what the operator said comes first, then the message.
func (v boardVisual) activityRows(w int) []activityRow {
	var rows []activityRow
	for _, e := range v.echo {
		rows = append(rows, activityRow{at: e.At, line: v.echoLine(e, w)})
	}
	for _, n := range v.notes {
		rows = append(rows, activityRow{at: n.At, line: v.noteLine(n, w), note: n.id})
	}
	for _, a := range v.board.Activity {
		rows = append(rows, activityRow{at: a.At, line: v.activityLine(a)})
	}
	slices.SortStableFunc(rows, func(x, y activityRow) int { return y.at.Compare(x.at) })
	return rows
}

// runWordColor is the colour of a run-level change: a closed run is done,
// an approved plan starts work, an amended one waits on its new tasks.
var runWordColor = map[string]string{"closed": "merged", "approved": "working", "amended": "waiting"}

// activityLine is one Activity row: when, what changed, its new word and
// the detail. A task line reads "hello-cli merged · reason"; a run-level
// line names its subject and reads "run closed to main 017b2d0". No line
// shows a bare verb or a dangling separator.
func (v boardVisual) activityLine(a model.ActivityLine) string {
	head := v.fg("dim", a.At.In(v.now.Location()).Format("15:04:05")) + " "
	detail := strings.TrimSpace(cleanBoard(a.Detail))
	if a.Subject != "" || a.TaskID == "" {
		subject, word := a.Subject, a.Word
		if subject == "" {
			subject = "run"
		}
		if word == "" {
			// A run-level type this projection has no word for: say what
			// the event is rather than a bare verb.
			word = strings.TrimPrefix(string(a.Type), "marshal.")
		}
		color := runWordColor[word]
		if color == "" {
			color = "accent"
		}
		line := head + v.fg("accent", subject) + " " + v.fg(color, word)
		if detail != "" {
			line += " " + v.fg("white", detail)
		}
		return line
	}
	line := head + v.fg("accent", a.TaskID) + " " + v.fg(boardStateColor(a.State), string(a.State))
	if detail != "" {
		line += " " + v.fg("dim", "· "+detail)
	}
	return line
}

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

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
