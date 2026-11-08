package tui

import (
	"fmt"
	"html"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// archPane is the Architecture view's one pane: the Board's body, between
// its header and footer.
const archPane layout.PaneID = "architecture"

// archSnapshot is the Architecture view's data: the last overview read and
// when it was read.
type archSnapshot struct {
	Overview store.ArchitectureOverview
	// Loaded is set once a read has succeeded; At is when the last read
	// finished and Err why it failed, if it did.
	Loaded bool
	At     time.Time
	Err    string
}

// archPaint is what one frame of the Architecture view needs.
type archPaint struct {
	Snap archSnapshot
	// Edition is the build's edition and version; Rules the rules in
	// force in it.
	Edition string
	Rules   []string
}

// bpStep is one step of the business process.
type bpStep struct{ ID, Text string }

// businessProcess is the Marshal's business process, step by step. The
// steps and their words are the Board prototype's (board-prototype.html,
// BP); no other document in the repository names them.
var businessProcess = []bpStep{
	{"M1", "Marshal introduces itself"}, {"L1", "language chosen"}, {"U1", "owner states the goal"}, {"S1", "Marshal reads state and earlier runs"},
	{"M2", "clarify: requirements and limits recorded"}, {"U2", "owner picks mode and control level"}, {"M3", "mode and authority fixed"},
	{"M4", "plan: tasks, workers, acceptance criteria"}, {"D1", "plan approved? (/marshal approve)"}, {"M5", "runtime dispatches approved tasks"},
	{"A1", "workers do the tasks"}, {"A2", "workers hand in results"}, {"M6", "Marshal checks each result"}, {"D2", "defects? → back to A1 with reasons"},
	{"D3", "interim approval due? → owner reviews"}, {"D4", "more tasks? → M5"}, {"M7", "assemble the result and report"}, {"U5", "owner accepts the result"},
}

// archComponents is what each part may do. Static text from the Board
// prototype (COMPONENTS) until the accepted contract carries it.
var archComponents = [][2]string{
	{"Workspace / TUI", "Board, chat dock, inbox; owner input only"}, {"Marshal", "plans, reviews, never edits task files"},
	{"Runtime", "Process 05 governed lanes, approvals R2/R3"}, {"Worktrees", "one per lane; merge to integration on accept"},
	{"Egress broker", "manifest hosts only; others go to inbox"}, {"Credential broker", "short per-task leases; no auth files in lanes"},
	{"Memory / MCP", "durable only after review; recalls audited"}, {"Audit", "append-only records"}, {"Host watch", "thermal, disk, memory policy every second"},
}

// archRules is the rules in force. Static text from the Board prototype
// (RULES) until the accepted contract carries them.
var archRules = []string{"main never moves during a run", "hard gates need the typed canonical id", "architecture acceptance is owner-only", "publishing is owner-only"}

// archRulesInForce is the build's rules in force: the Marshal's
// own first.
func archRulesInForce() []string {
	if editionExtreme {
		return append([]string{"Marshal: local ULTRA, anonymous opt-out telemetry"}, archRules...)
	}
	return slices.Clone(archRules)
}

// archEdition names the build's edition and version.
func archEdition() string {
	if editionExtreme {
		return "Marshal " + BuildVersion
	}
	return "Community " + BuildVersion
}

// archStaleAfter is how long an in-flight run may go without a run event
// before the business-process step is labelled stale: the step the store
// last recorded is shown, but no longer presented as live.
const archStaleAfter = 30 * time.Minute

// bpPos is where the run is in the business process: the step, why, and
// a flag when the step is not live ("stale" or "recovered").
type bpPos struct{ Step, Why, Flag string }

// bpNow is bpAt's step and reason, the reason prefixed by its flag.
func bpNow(b model.Board, now time.Time) (string, string) {
	p := bpAt(b, now)
	if p.Flag != "" {
		return p.Step, p.Flag + ": " + p.Why
	}
	return p.Step, p.Why
}

// bpAt derives the step from the run's own state, the canonical phase the
// runtime records, not from guessing at task counts: an empty Board has no
// run yet (U1), a drafting run is planning (M4) or waiting on the plan's
// approval (D1), dispatching is M5 (M6 once nothing is in flight but work
// is in check), reviewing M6, merging and verifying M7, awaiting the owner
// or any decision in the inbox D3, and a closed run U5.
//
// A step that is not live is flagged: "stale" when the store could not be
// read or an in-flight run has had no event for archStaleAfter, and
// "recovered" when the in-flight run was restored from its stored snapshot
// and no live event has arrived since.
func bpAt(b model.Board, now time.Time) bpPos {
	n := map[model.TaskState]int{}
	for _, t := range b.Tasks {
		n[t.State]++
	}
	check := n[model.TaskReview] + n["verifying"]
	counts := fmt.Sprintf("%d working · %d queued · %d in check", n[model.TaskWorking], n[model.TaskQueued], check)
	state := b.Run.State
	var p bpPos
	switch state {
	case "":
		if b.Run.ID == "" && len(b.Tasks) == 0 {
			p = bpPos{Step: "U1", Why: "no run yet: waiting for the owner's goal"}
		} else {
			p = bpPos{Step: "S1", Why: "reading the run's state"}
		}
	case marshal.Drafting:
		if len(b.Tasks) == 0 {
			p = bpPos{Step: "M4", Why: "drafting the plan"}
		} else {
			p = bpPos{Step: "D1", Why: fmt.Sprintf("plan of %d task%s awaiting approval", len(b.Tasks), plural(len(b.Tasks), "", "s"))}
		}
	case marshal.Approved:
		p = bpPos{Step: "M5", Why: "plan approved · dispatch starting"}
	case marshal.Dispatching:
		p = bpPos{Step: "M5", Why: string(state) + " · " + counts}
		if n[model.TaskWorking]+n[model.TaskQueued] == 0 && check > 0 {
			p = bpPos{Step: "M6", Why: fmt.Sprintf("%s · %d in check", state, check)}
		}
	case marshal.Reviewing:
		p = bpPos{Step: "M6", Why: string(state) + " · " + counts}
	case marshal.Merging:
		p = bpPos{Step: "M7", Why: "merging accepted results"}
	case marshal.Verifying:
		p = bpPos{Step: "M7", Why: "verifying the assembled result"}
	case marshal.AwaitingUser:
		p = bpPos{Step: "D3", Why: "waiting on owner"}
	case marshal.Closed:
		p = bpPos{Step: "U5", Why: "run closed"}
	default:
		p = bpPos{Step: "S1", Why: "run state " + cleanBoard(string(state))}
	}
	if state != marshal.Closed && len(b.Inbox) > 0 {
		p = bpPos{Step: "D3", Why: fmt.Sprintf("waiting on owner: %d decision%s in the inbox", len(b.Inbox), plural(len(b.Inbox), "", "s"))}
	}
	inFlight := state != "" && state != marshal.Drafting && state != marshal.Closed
	switch {
	case b.Stale:
		p.Flag, p.Why = "stale", "the store could not be read; last read "+p.Why
	case inFlight && b.Seq == 0 && len(b.Activity) == 0:
		p.Flag, p.Why = "recovered", "restored from the stored run; no live event yet · "+p.Why
	case inFlight && len(b.Activity) > 0 && !now.IsZero() && now.Sub(b.Activity[0].At) >= archStaleAfter:
		p.Flag, p.Why = "stale", "no run event for "+archAge(now.Sub(b.Activity[0].At))+" · "+p.Why
	}
	return p
}

// archAge is a short duration: 45m, 3h, 2d.
func archAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

// archChip is the Board header's architecture chip: "arch — · step S"
// until the first read succeeds (or when there is nothing to read), then
// the accepted revision and the step the run is at. A step that is not
// live carries its flag ("step M5 stale"). The compact form, for a narrow
// header, keeps the revision and the step.
func (v boardVisual) archChip(compact bool) string {
	pos := bpAt(v.board, v.now)
	step := v.fg("waiting", pos.Step)
	if pos.Flag != "" {
		step += " " + v.fg("parked", pos.Flag)
	}
	rev := v.fg("dim", "—")
	if v.archLoaded {
		rev = v.fg("parked", "none")
		if v.archRev > 0 {
			rev = v.fg("merged", fmt.Sprintf("rev %d", v.archRev))
		}
	}
	if compact {
		return v.fg("dim", "arch ") + rev + " " + step
	}
	return v.fg("dim", "arch ") + rev + v.fg("dim", " · step ") + step
}

// archChipNarrow is the architecture chip of a header of 100 columns or
// fewer: the revision and the step, "rev 7 D3".
func (v boardVisual) archChipNarrow() string {
	full := v.archChip(true)
	return strings.TrimPrefix(full, v.fg("dim", "arch "))
}

// archAccepted is the ARCHITECTURE title's summary: the accepted revision,
// who accepted it and when, and the edition.
func archAccepted(o store.ArchitectureOverview, edition string, now time.Time) string {
	a := o.Accepted
	if a == nil {
		return joinNonEmpty(" · ", "no accepted architecture", edition)
	}
	what := "architecture"
	if a.Record == store.ArchitectureRecordContract {
		what = "contract"
	}
	return joinNonEmpty(" · ", fmt.Sprintf("%s rev %d", what, a.Revision), archDigest(a.Digest), "accepted by "+cleanBoard(a.By)+" "+archTime(a.At, now), edition)
}

// archAcceptedShort is archAccepted for a narrow body: who accepted it
// and when, without the words around them.
func archAcceptedShort(o store.ArchitectureOverview, edition string, now time.Time) string {
	a := o.Accepted
	if a == nil {
		return joinNonEmpty(" · ", "none accepted", edition)
	}
	what := "architecture"
	if a.Record == store.ArchitectureRecordContract {
		what = "contract"
	}
	return joinNonEmpty(" · ", fmt.Sprintf("%s rev %d", what, a.Revision), archDigest(a.Digest), cleanBoard(a.By)+" "+archTime(a.At, now), edition)
}

func archDigest(d string) string {
	d = strings.TrimPrefix(cleanBoard(d), "sha256:")
	return d[:min(len(d), 8)]
}

func archTime(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.In(now.Location()).Format("2006-01-02 15:04")
}

// archDiffAnswer is what d says: whether the accepted revision is still
// the current one, and what changed when it is not.
func archDiffAnswer(o store.ArchitectureOverview) string {
	a := o.Accepted
	switch {
	case a == nil:
		return "No accepted architecture yet: nothing to compare."
	case !o.Comparable:
		return fmt.Sprintf("rev %d is the accepted architecture; the store keeps only accepted revisions, so it cannot tell a newer draft: [o] opens it.", a.Revision)
	case o.Current <= a.Revision || len(o.Changes) == 0:
		return fmt.Sprintf("rev %d unchanged since accepted (digest %s).", a.Revision, archDigest(a.Digest))
	}
	return fmt.Sprintf("rev %d changed since rev %d was accepted: %s. It waits for \"accept architecture\".", o.Current, a.Revision, strings.Join(o.Changes, ", "))
}

// archRender paints the Architecture view into a w×h body.
func (v boardVisual) archRender(m archPaint, w, h int) []string {
	return v.archWindow(v.archLines(m, w, h), w, h)
}

// archWindow pads or cuts the view's lines to a w×h body.
func (v boardVisual) archWindow(out []string, w, h int) []string {
	out = slices.Clone(out[:min(len(out), h)])
	for i := range out {
		out[i] = PadCell(out[i], w)
	}
	for len(out) < h {
		out = append(out, PadCell("", w))
	}
	if !v.unicode() {
		out = hostASCII(boardASCIIGlyphs(out), w)
	}
	return out
}

// archLines lays the view out as the prototype does: ARCHITECTURE across
// the top, then the BUSINESS PROCESS beside COMPONENTS and RULES IN FORCE
// when the body is wide enough, else under one another. When the body is
// too short for everything, it gives way in steps (archFit) so that every
// section keeps its title and what is left out is counted.
func (v boardVisual) archLines(m archPaint, w, h int) []string {
	s := m.Snap
	o := s.Overview
	wide := w >= 100
	summary := archAccepted(o, m.Edition, v.now)
	if VisibleLen("ARCHITECTURE "+summary) > w {
		summary = archAcceptedShort(o, m.Edition, v.now)
	}
	top := []string{v.title("ARCHITECTURE", summary, false, w)}
	if !s.Loaded {
		read := "reading the store…"
		if s.Err != "" {
			read = v.fg("failed", "not readable: "+cleanBoard(s.Err))
		}
		top = append(top, v.kv("updated", read, w))
	} else {
		read := "read " + hostAgo(v.now.Sub(s.At)) + " · every 10 s while open"
		if s.Err != "" {
			read = v.fg("failed", "last read failed: "+cleanBoard(s.Err)) + v.fg("dim", " · showing "+hostAgo(v.now.Sub(s.At)))
		}
		top = append(top, v.kv("updated", read, w))
		force := v.fg("parked", "nothing accepted yet · the owner accepts a draft with \"accept architecture\"")
		if a := o.Accepted; a != nil {
			force = v.fg("merged", fmt.Sprintf("rev %d accepted · Marshal acts only inside it", a.Revision))
		}
		top = append(top, v.kv("in force", force, w))
	}
	changes := v.kv("changes", "a new revision needs \"accept architecture\" from the owner", w)
	open := v.kv("open", v.bg("rule", "[o]")+" full architecture and business process in the browser  "+v.bg("rule", "[d]")+" diff since accepted", w)
	if VisibleLen(StripANSI(open)) > w || !wide {
		open = v.kv("open", v.bg("rule", "[o]")+" in the browser  "+v.bg("rule", "[d]")+" diff since accepted", w)
	}

	cur, why := bpNow(v.board, v.now)
	f := archFit{bp: len(businessProcess), comps: len(archComponents), rules: len(m.Rules), allRules: len(m.Rules), changes: true, gaps: true, note: true}
	leftW, rightW := w, w
	if wide {
		leftW = min(56, max(48, w-2-66))
		rightW = w - leftW - 2
	}
	f.noteRows = len(archNote(leftW))
	rulesPerLine := v.archRuleLines(m.Rules, rightW)
	f.fit(h, wide, len(top)+1, len(rulesPerLine))
	out := top
	if f.changes {
		out = append(out, changes)
	}
	out = append(out, open)
	if f.gaps {
		out = append(out, "")
	}
	left := v.archProcess(cur, why, leftW, f.bp, f.note)
	right := []string{v.title("COMPONENTS", "what each part may do in this revision", false, rightW)}
	for _, c := range archComponents[:f.comps] {
		right = append(right, PadCell(" "+PadCell(c[0], 18)+" "+v.fg("dim", c[1]), rightW))
	}
	if n := len(archComponents) - f.comps; n > 0 {
		right = append(right, v.fg("dim", PadCell(fmt.Sprintf(" +%d more · [o] shows all", n), rightW)))
	}
	if f.gaps || wide {
		right = append(right, "")
	}
	right = append(right, v.title("RULES IN FORCE", "", false, rightW))
	if f.packRules {
		right = append(right, rulesPerLine...)
	} else {
		for _, r := range m.Rules[:f.rules] {
			right = append(right, " "+v.fg("merged", "●")+" "+r)
		}
		if n := len(m.Rules) - f.rules; n > 0 {
			right = append(right, v.fg("dim", PadCell(fmt.Sprintf(" +%d more · [o] shows all", n), rightW)))
		}
	}
	if !wide {
		if f.gaps {
			left = append(left, "")
		}
		return append(append(out, left...), right...)
	}
	for i := range max(len(left), len(right)) {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, PadCell(l, leftW)+"  "+PadCell(r, rightW))
	}
	return out
}

// archRuleLines packs the rules in force side by side, as many to a line
// as fit in w cells.
func (v boardVisual) archRuleLines(rules []string, w int) []string {
	var out []string
	line := ""
	for _, r := range rules {
		item := v.fg("merged", "●") + " " + r
		switch {
		case line == "":
			line = " " + item
		case VisibleLen(line+"   "+item) <= w:
			line += "   " + item
		default:
			out = append(out, line)
			line = " " + item
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// archFit is how much of each section the Architecture view shows.
type archFit struct {
	// bp is the business process's step rows; comps and rules how many
	// components and rules are listed, of allRules; noteRows is the ULTRA
	// note's lines.
	bp, comps, rules, allRules, noteRows int
	// changes keeps the "changes" line, gaps the blank lines between
	// sections, note the ULTRA note under the process; packRules puts
	// several rules on a line.
	changes, gaps, note, packRules bool
}

// rows is how many body rows the view takes: top rows of summary above
// the sections, and packed lines when the rules are packed.
func (f *archFit) rows(wide bool, top, packed int) int {
	more := func(n, of int) int { return boolInt(n < of) }
	head := top + boolInt(f.changes) + boolInt(f.gaps)
	left := 1 + f.bp + boolInt(f.note)*f.noteRows
	rules := f.rules + more(f.rules, f.allRules)
	if f.packRules {
		rules = packed
	}
	right := 1 + f.comps + more(f.comps, len(archComponents)) + boolInt(f.gaps || wide) + 1 + rules
	if wide {
		return head + max(left, right)
	}
	return head + left + boolInt(f.gaps) + right
}

// fit shrinks the view into h rows. It gives way in this order: the blank
// lines, the "changes" line and the ULTRA note; the rules packed side by
// side; the process down to five steps around the current one, then to
// three; then the components and the rules, each to what fits with a
// "+N more" line; last the process down to its current step.
func (f *archFit) fit(h int, wide bool, top, packed int) {
	over := func() bool { return f.rows(wide, top, packed) > h }
	for _, step := range []func(){
		func() { f.gaps = false },
		func() { f.changes = false },
		func() { f.note = false },
		func() { f.packRules = !wide && packed < f.rules },
	} {
		if !over() {
			return
		}
		step()
	}
	for _, floor := range []int{5, 3} {
		for f.bp > floor && over() {
			f.bp--
		}
	}
	for f.comps > 0 && over() {
		f.comps--
	}
	if f.packRules && over() {
		f.packRules = false
	}
	for f.rules > 0 && over() {
		f.rules--
	}
	for f.bp > 1 && over() {
		f.bp--
	}
}

// archProcess is the BUSINESS PROCESS section: each step done, current or
// to come, in at most rows step rows that keep the current step in view,
// and the ULTRA note under them when note is set.
func (v boardVisual) archProcess(cur, why string, w, rows int, note bool) []string {
	out := []string{v.title("BUSINESS PROCESS", "now at "+cur+" · "+cleanBoard(why), false, w)}
	ix := slices.IndexFunc(businessProcess, func(s bpStep) bool { return s.ID == cur })
	first := 0
	if rows < len(businessProcess) {
		first = min(max(0, ix-rows/2), len(businessProcess)-rows)
	}
	for i := first; i < min(len(businessProcess), first+rows); i++ {
		s := businessProcess[i]
		mark, col, text := v.fg("dim", "·"), "dim", v.fg("dim", s.Text)
		switch {
		case i < ix:
			mark, col, text = v.fg("merged", "✓"), "accent", s.Text
		case i == ix:
			mark, col, text = v.fg("waiting", "▸"), "accent", s.Text
		}
		line := PadCell(" "+mark+" "+v.fg(col, s.ID)+" "+text, w)
		if i == ix {
			line = v.bg("sel", line)
		}
		out = append(out, line)
	}
	if note {
		for _, l := range archNote(w) {
			out = append(out, v.fg("dim", PadCell(" "+l, w)))
		}
	}
	return out
}

// archNote is the ULTRA note under the process, wrapped to a column w
// cells wide with its leading space.
func archNote(w int) []string {
	return wrap("under ULTRA, independent tasks loop M5–M6 in parallel", max(1, w-1))
}

// archPage is the static page o opens: the accepted architecture, the
// business process with the current step marked, the components and the
// rules in force.
func archPage(o store.ArchitectureOverview, edition string, rules []string, cur, why string, now time.Time) []byte {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>MARSHAL architecture</title>
<style>body{background:#0b0e14;color:#c9d1e3;font:14px/1.5 ui-monospace,monospace;margin:24px}h1,h2{color:#fff;font-size:15px;margin:20px 0 6px}.dim{color:#6b7489}.ok{color:#7fd69a}.now{background:#1d2433;color:#e6c35c}.id{color:#8fb3ff}li{list-style:none}td{padding:0 16px 0 0;vertical-align:top}</style></head><body>`)
	b.WriteString("<h1>ARCHITECTURE</h1><p>" + e(archAccepted(o, edition, now)) + "</p>")
	b.WriteString(`<p class="dim">` + e(archDiffAnswer(o)) + "</p>")
	b.WriteString("<h2>BUSINESS PROCESS</h2><p class=\"dim\">now at " + e(cur) + " · " + e(why) + "</p><ul>")
	ix := slices.IndexFunc(businessProcess, func(s bpStep) bool { return s.ID == cur })
	for i, s := range businessProcess {
		class, mark := "dim", "·"
		switch {
		case i < ix:
			class, mark = "", "✓"
		case i == ix:
			class, mark = "now", "▸"
		}
		b.WriteString(`<li class="` + class + `">` + mark + ` <span class="id">` + e(s.ID) + "</span> " + e(s.Text) + "</li>")
	}
	b.WriteString(`</ul><p class="dim">under ULTRA, independent tasks loop M5–M6 in parallel</p><h2>COMPONENTS</h2><table>`)
	for _, c := range archComponents {
		b.WriteString("<tr><td>" + e(c[0]) + `</td><td class="dim">` + e(c[1]) + "</td></tr>")
	}
	b.WriteString("</table><h2>RULES IN FORCE</h2><ul>")
	for _, r := range rules {
		b.WriteString(`<li><span class="ok">●</span> ` + e(r) + "</li>")
	}
	b.WriteString(`</ul><p class="dim">written ` + e(now.Format("2006-01-02 15:04:05")) + " · read-only</p></body></html>\n")
	return []byte(b.String())
}
