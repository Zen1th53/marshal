package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// securityPane is the Security view's one pane: the Board's body, between
// its header and footer.
const securityPane layout.PaneID = "security"

// securitySnapshot is the Security view's data: the last overview read and
// when it was read.
type securitySnapshot struct {
	Overview store.SecurityOverview
	// Loaded is set once a read has succeeded; At is when the last read
	// finished and Err why it failed, if it did.
	Loaded bool
	At     time.Time
	Err    string
}

// securityPaint is what one frame of the Security view needs.
type securityPaint struct {
	Snap securitySnapshot
	// Selected is the egress row the keys act on, by its sequence.
	Selected string
	// Busy is the egress row being decided.
	Busy string
	// Armed is the decision waiting for Enter on the selected row: 'A'
	// allow for the run, 'd' deny, 0 none.
	Armed rune
	// InInbox is set when the selected waiting row has a pending inbox
	// request, which a and d decide.
	InInbox bool
	// Edition is what the build says about its security.
	Edition securityEdition
}

// securityEdition is what the build's edition says about its security:
// its mode, sandbox and secrets policy.
type securityEdition struct{ Mode, Sandbox, Secrets string }

// securityDecisionColour colours an egress decision.
var securityDecisionColour = map[string]string{store.EgressAllowed: "merged", store.EgressWaiting: "waiting", store.EgressDenied: "failed"}

// securityRowID is an egress row's selection id.
func securityRowID(e store.SecurityEgress) string { return fmt.Sprint(e.Sequence) }

// securityRender paints the Security view into a w×h body, keeping the
// selected egress row in view.
func (v boardVisual) securityRender(m securityPaint, w, h int) []string {
	all, selAt := v.securityLines(m, w)
	scroll := 0
	if selAt >= 0 && selAt >= h-1 {
		scroll = selAt - h + 2
	}
	scroll = min(max(0, scroll), max(0, len(all)-h))
	out := slices.Clone(all[scroll:min(len(all), scroll+h)])
	for i := range out {
		out[i] = PadCell(out[i], w)
	}
	if !v.unicode() {
		out = hostASCII(out, w)
	}
	return out
}

// securityCols are the egress table's column widths; the reason takes
// what is left.
type securityCols struct{ decision, worker, task, host, reason int }

func securityColumns(w int) securityCols {
	c := securityCols{decision: 9, worker: 11, task: 12, host: 26}
	if w < 110 {
		c = securityCols{decision: 8, worker: 9, task: 9, host: 22}
	}
	c.reason = max(6, w-(2+c.decision+c.worker+c.task+c.host+5))
	return c
}

// securityLines lays the view out as the prototype does: the SECURITY
// summary, the run's EGRESS decisions with the selected row's keys, the
// CREDENTIAL LEASES and the AUDIT records. It also returns the line the
// selected egress row is on, or -1.
func (v boardVisual) securityLines(m securityPaint, w int) ([]string, int) {
	s := m.Snap
	ed := m.Edition
	out := []string{v.title("SECURITY", ed.Mode+" · egress only through the broker", false, w)}
	if !s.Loaded {
		read := "reading the store…"
		if s.Err != "" {
			read = v.fg("failed", "not readable: "+cleanBoard(s.Err))
		}
		return append(out, v.kv("updated", read, w)), -1
	}
	read := "read " + hostAgo(v.now.Sub(s.At)) + " · every 10 s while open"
	if s.Err != "" {
		read = v.fg("failed", "last read failed: "+cleanBoard(s.Err)) + v.fg("dim", " · showing "+hostAgo(v.now.Sub(s.At)))
	}
	o := s.Overview
	out = append(out, v.kv("updated", read, w))
	sandbox := v.fg("merged", ed.Sandbox)
	if u := o.Unconfined; u != nil {
		sandbox = v.fg("parked", "last lane unconfined: "+joinNonEmpty(" ", cleanBoard(u.Worker), cleanBoard(u.Task))) + v.fg("dim", " · "+hostAgo(v.now.Sub(u.At))+" · "+ed.Sandbox)
	}
	out = append(out, v.kv("sandbox", sandbox, w), v.kv("secrets", ed.Secrets, w))
	trips := v.fg("merged", "0 trips")
	if o.HoneypotTrips > 0 {
		trips = v.fg("failed", fmt.Sprintf("%d trip%s · task stopped, do not merge", o.HoneypotTrips, map[bool]string{true: "", false: "s"}[o.HoneypotTrips == 1]))
	}
	if o.HoneypotPartial {
		trips += v.fg("dim", " · in the newest audit records")
	}
	out = append(out, v.kv("honeypot", trips, w), v.kv("audit", "append-only records · not hash chained", w))

	out = append(out, "", v.title("EGRESS", "every network call a worker makes in this run", false, w))
	c := securityColumns(w)
	row := func(dot, decision, worker, task, host, reason string) string {
		return strings.Join([]string{PadCell(dot, 1), PadCell(decision, c.decision), PadCell(worker, c.worker), PadCell(task, c.task), PadCell(host, c.host), PadCell(reason, c.reason)}, " ")
	}
	out = append(out, v.fg("dim", PadCell(row("", "decision", "worker", "task", "host", "reason"), w)))
	sel := -1
	var selected *store.SecurityEgress
	if len(o.Egress) == 0 {
		out = append(out, v.fg("dim", PadCell("  no network calls recorded for this run yet", w)))
	}
	for i, e := range o.Egress {
		col := securityDecisionColour[e.Decision]
		if col == "" {
			col = "dim"
		}
		decision := e.Decision
		if m.Busy == securityRowID(e) {
			decision, col = "deciding", "working"
		}
		line := PadCell(row(v.fg(col, "●"), v.fg(col, decision), cleanBoard(e.Worker), v.fg("accent", cleanBoard(e.Task)), cleanBoard(e.Endpoint), v.fg("dim", cleanBoard(e.Reason))), w)
		if securityRowID(e) == m.Selected {
			sel, selected = len(out), &o.Egress[i]
			line = v.bg("sel", line)
		}
		out = append(out, line)
	}
	if selected != nil && selected.Decision == store.EgressWaiting {
		keys := v.bg("rule", "[a]") + " allow for run  " + v.bg("rule", "[d]") + " deny"
		switch {
		case m.Busy != "":
			keys = v.fg("working", "deciding "+cleanBoard(selected.Endpoint)+"…")
		case m.Armed == 'A' && m.InInbox:
			keys = v.fg("waiting", "Enter to allow "+cleanBoard(selected.Endpoint)+" for the run · Esc cancels")
		case m.Armed == 'd' && m.InInbox:
			keys = v.fg("waiting", "Enter to deny "+cleanBoard(selected.Endpoint)+" · Esc cancels")
		case !m.InInbox:
			keys = v.fg("dim", "[a] allow · [d] deny · no inbox request for this call: decide it in the inbox")
		}
		out = append(out, PadCell("  "+keys, w))
	}

	out = append(out, "", v.title("CREDENTIAL LEASES", "short, per task, revoked when the lane ends", false, w))
	if len(o.Leases) == 0 {
		out = append(out, v.fg("dim", PadCell("  no active leases", w)))
	}
	for _, l := range o.Leases {
		left := max(0, int(l.Expires.Sub(v.now).Seconds()))
		what := joinNonEmpty(" · ", cleanBoard(l.Purpose), cleanBoard(l.Secret), fmt.Sprintf("ends in %d:%02d", left/60, left%60))
		out = append(out, PadCell("  "+PadCell(cleanBoard(l.Worker), 10)+" "+v.fg("accent", PadCell(cleanBoard(l.Task), 12))+" "+v.fg("dim", what), w))
	}

	out = append(out, "", v.title("AUDIT", "newest records", false, w))
	if len(o.Audit) == 0 {
		out = append(out, v.fg("dim", PadCell("  no audit records yet", w)))
	}
	for _, a := range o.Audit {
		col := "fg"
		if a.Type == "HONEYPOT_HIT" {
			col = "failed"
		}
		out = append(out, PadCell("  "+v.fg("dim", PadCell(hostAgo(v.now.Sub(a.At)), 9))+" "+v.fg(col, cleanBoard(a.Type))+v.fg("dim", " · "+cleanBoard(firstNonEmpty(a.Subject, "—"))), w))
	}
	return out, sel
}
