package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// verifyPane is the Verify view's one pane: the Board's body, between its
// header and footer.
const verifyPane layout.PaneID = "verify"

// verifySnapshot is the Verify view's data: the last overview read and
// when it was read.
type verifySnapshot struct {
	Overview store.VerifyOverview
	// Loaded is set once a read has succeeded; At is when the last read
	// finished and Err why it failed, if it did.
	Loaded bool
	At     time.Time
	Err    string
}

// verifyPaint is what one frame of the Verify view needs.
type verifyPaint struct {
	Snap verifySnapshot
	// Selected is the task whose claims show and the return key acts on.
	Selected string
	// Busy is the task being returned.
	Busy string
	// Pending is the task whose return waits for Enter.
	Pending string
}

// verifyStateColour colours a run task's state as the Board does.
var verifyStateColour = map[marshal.TaskState]string{
	"": "dim", marshal.Queued: "dim", marshal.Dispatched: "working", marshal.HandedIn: "verify",
	marshal.Accepted: "merged", marshal.Merged: "merged", marshal.Returned: "parked", marshal.Reassigned: "parked",
	marshal.Escalated: "parked", marshal.Cancelled: "failed",
}

// verifyUnsupported is the selected task's claims that are not SUPPORTED:
// what the return key sends back.
func verifyUnsupported(t store.VerifyTask) []store.VerifyClaim {
	var bad []store.VerifyClaim
	for _, c := range t.Claims {
		if c.Status != store.ClaimSupported {
			bad = append(bad, c)
		}
	}
	return bad
}

// verifyRender paints the Verify view into a w×h body, keeping the
// selected task in view.
func (v boardVisual) verifyRender(m verifyPaint, w, h int) []string {
	all, selAt := v.verifyLines(m, w)
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

// verifyCols are the task table's column widths; the reviewer takes what
// is left.
type verifyCols struct{ task, state, worker, claims, supported, reviewer, other int }

func verifyColumns(w int) verifyCols {
	c := verifyCols{task: 9, state: 11, worker: 11, claims: 6, supported: 9, other: 14}
	if w < 110 {
		c = verifyCols{task: 7, state: 10, worker: 8, claims: 6, supported: 9, other: 14}
	}
	fixed := 2 + c.task + c.state + c.worker + c.claims + c.supported + c.other + 7
	c.reviewer = min(24, max(6, w-fixed))
	return c
}

// verifyLines lays the view out as the prototype does: the VERIFY title,
// one row per task, the selected task's claims and keys, then the run's
// verifier and architecture review. It also returns the line the selected
// task is on, or -1.
func (v boardVisual) verifyLines(m verifyPaint, w int) ([]string, int) {
	s := m.Snap
	summary := "a task merges only when its claims are SUPPORTED by evidence a different provider checked"
	if VisibleLen("VERIFY "+summary) > w {
		summary = "claims merge only when SUPPORTED by evidence"
	}
	out := []string{v.title("VERIFY", summary, false, w)}
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
	out = append(out, v.kv("updated", read, w), "")
	c := verifyColumns(w)
	cell := func(s string, n int) string { return PadCell(s, n) }
	row := func(dot, task, state, worker, claims, supported, reviewer, other string) string {
		return strings.Join([]string{cell(dot, 1), cell(task, c.task), cell(state, c.state), cell(worker, c.worker), verifyRight(claims, c.claims), verifyRight(supported, c.supported), cell(reviewer, c.reviewer), cell(other, c.other)}, " ")
	}
	out = append(out, v.fg("dim", PadCell(row("", "task", "state", "worker", "claims", "supported", "reviewer", "provider"), w)))
	o := s.Overview
	if len(o.Tasks) == 0 {
		out = append(out, v.fg("dim", PadCell("  the run has no tasks yet", w)))
		return append(out, v.verifyRun(o, w)...), -1
	}
	sel := -1
	var selected *store.VerifyTask
	for i, t := range o.Tasks {
		col := verifyStateColour[t.State]
		if col == "" {
			col = "dim"
		}
		claims, supported := v.fg("dim", "—"), v.fg("dim", "—")
		if t.Attempt > 0 {
			n, ok := len(t.Claims), t.Supported()
			claims = fmt.Sprint(n)
			supported = v.fg("merged", fmt.Sprintf("%d/%d", ok, n))
			if ok < n {
				supported = v.fg("waiting", fmt.Sprintf("%d/%d", ok, n))
			}
		}
		reviewer, other := v.fg("dim", "—"), v.fg("dim", "—")
		if t.Reviewer != "" {
			reviewer = cleanBoard(t.Reviewer)
		}
		switch {
		case t.OtherProvider:
			other = v.fg("merged", "other provider")
		case t.ReviewerProvider != "":
			other = v.fg("waiting", "same provider")
		}
		state := string(t.State)
		if m.Busy == t.ID {
			state, col = "returning", "parked"
		}
		line := PadCell(row(v.fg(col, "●"), cleanBoard(t.ID), v.fg(col, state), cleanBoard(t.Worker), claims, supported, reviewer, other), w)
		if t.ID == m.Selected {
			sel = len(out)
			selected = &o.Tasks[i]
			line = v.bg("sel", line)
		}
		out = append(out, line)
	}
	if selected != nil {
		out = append(out, v.verifyClaims(*selected, m, w)...)
	}
	return append(out, v.verifyRun(o, w)...), sel
}

// verifyClaims is the selected task's claims, each with its status,
// evidence and checker, and the return key.
func (v boardVisual) verifyClaims(t store.VerifyTask, m verifyPaint, w int) []string {
	summary := cleanBoard(t.Title)
	if t.Attempt > 0 {
		summary = joinNonEmpty(" · ", summary, fmt.Sprintf("hand-in %d", t.Attempt))
	}
	out := []string{"", v.title("CLAIMS · "+cleanBoard(t.ID), summary, false, w)}
	if t.Attempt == 0 {
		return append(out, v.fg("dim", PadCell("  no hand-in yet; claims show once "+cleanBoard(firstNonEmpty(t.Worker, "the worker"))+" hands in", w)))
	}
	if len(t.Claims) == 0 {
		out = append(out, v.fg("dim", PadCell("  the hand-in makes no claims and the task has no criteria", w)))
	}
	for _, c := range t.Claims {
		status := v.fg("merged", PadCell(c.Status, 11))
		if c.Status != store.ClaimSupported {
			status = v.fg("failed", PadCell(c.Status, 11))
		}
		text := cleanBoard(c.Text)
		if c.Criterion {
			text = "criterion: " + text
		}
		out = append(out, PadCell("  "+status+" "+text+v.fg("dim", " · "+cleanBoard(c.Evidence)+" · checked by "+cleanBoard(c.CheckedBy)), w))
	}
	keys := v.bg("rule", "[x]") + " return unsupported claims for rework"
	bad := len(verifyUnsupported(t))
	switch {
	case m.Busy != "":
		keys = v.fg("waiting", "returning "+cleanBoard(m.Busy)+"…")
	case m.Pending == t.ID:
		keys = v.fg("waiting", fmt.Sprintf("Enter to return %s with %d unsupported claim(s) for rework · Esc cancels", cleanBoard(t.ID), bad))
	case bad == 0:
		keys = v.fg("dim", "[x] return · every claim is SUPPORTED")
	case t.State == marshal.Escalated:
		keys = v.fg("dim", "[x] return · escalated: use /marshal retry "+cleanBoard(t.ID))
	case t.State != marshal.HandedIn:
		keys = v.fg("dim", "[x] return · only a task awaiting your decision")
	}
	return append(out, "", PadCell("  "+keys, w))
}

// verifyRun is the run-level verification: the independent verifier's
// result, the verification session and the architecture review.
func (v boardVisual) verifyRun(o store.VerifyOverview, w int) []string {
	out := []string{"", v.title("RUN", "independent verifier and architecture review", false, w)}
	verifier := v.fg("dim", "not recorded yet")
	if f := o.Verifier; f != nil {
		col := "merged"
		if f.Verdict != "pass" || f.Error != "" {
			col = "failed"
		}
		who := joinNonEmpty(" ", cleanBoard(f.Reviewer), "("+cleanBoard(f.Provider)+")")
		verifier = joinNonEmpty(" · ", v.fg(col, firstNonEmpty(cleanBoard(f.Verdict), "no verdict")), who)
		if f.SameProvider {
			verifier += v.fg("waiting", " · same provider as a worker")
		}
		if f.Error != "" {
			verifier += v.fg("failed", " · "+cleanBoard(f.Error))
		} else if len(f.Findings) > 0 {
			verifier += v.fg("dim", " · "+cleanBoard(f.Findings[0]))
		}
		if !f.At.IsZero() {
			verifier += v.fg("dim", " · "+hostAgo(v.now.Sub(f.At)))
		}
	}
	out = append(out, v.kv("verifier", verifier, w))
	session := v.fg("dim", "none yet")
	if ss := o.Session; ss != nil {
		col := "waiting"
		switch ss.State {
		case "VERIFIED_COMPLETE":
			col = "merged"
		case "VERIFICATION_FAILED", "BLOCKED", "CANCELLED":
			col = "failed"
		}
		session = v.fg(col, cleanBoard(ss.State)) + v.fg("dim", " · "+cleanBoard(ss.ID))
	}
	out = append(out, v.kv("verification", session, w))
	arch := v.fg("dim", "no review recorded")
	if a := o.Architecture; a != nil {
		col := "waiting"
		switch a.Outcome {
		case "COMPATIBLE", "NOT_APPLICABLE":
			col = "merged"
		case "INCOMPATIBLE":
			col = "failed"
		}
		arch = joinNonEmpty(" · ", v.fg(col, cleanBoard(a.Outcome)), fmt.Sprintf("%d pass, %d fail, %d unknown", a.Pass, a.Fail, a.Unknown), cleanBoard(a.Provider))
		if !a.At.IsZero() {
			arch += v.fg("dim", " · "+hostAgo(v.now.Sub(a.At)))
		}
	}
	return append(out, v.kv("architecture", arch, w))
}

// verifyRight right-aligns s in a cell n wide.
func verifyRight(s string, n int) string {
	if VisibleLen(s) >= n {
		return PadCell(s, n)
	}
	return strings.Repeat(" ", n-VisibleLen(s)) + s
}
