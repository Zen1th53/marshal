package v2

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// Geometry of the two-column screens.
const (
	TwoColumns = 120 // at or above: two columns; below: the right column stacks under the left
	RightW     = 46  // right column width in two-column mode
)

// slot is one pane waiting for its height.
type slot struct {
	want, min int
	prio      int  // higher gives way first: shrinks to min, then goes
	grow      bool // takes spare rows, up to most
	// most is the tallest a growing pane gets: what its content needs and
	// a little room. 0 is no bound (a provider area fills what is left).
	// Rows no pane takes stay blank below the panes.
	most int
	draw func(c *canvas, x, y, w, h int)
}

// stack lays slots top to bottom in h rows from y.
func stack(c *canvas, slots []slot, x, y, w, h int) {
	hs := make([]int, len(slots))
	total := 0
	for i, s := range slots {
		hs[i] = s.want
		total += s.want
	}
	order := make([]int, len(slots))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return slots[order[a]].prio > slots[order[b]].prio })
	for _, i := range order {
		if total <= h {
			break
		}
		cutBy := min(hs[i]-slots[i].min, total-h)
		if cutBy > 0 {
			hs[i] -= cutBy
			total -= cutBy
		}
		// A side pane goes whole before the panes that keep rows shrink.
		if total > h && slots[i].prio >= 2 {
			total -= hs[i]
			hs[i] = 0
		}
	}
	for _, i := range order {
		if total <= h {
			break
		}
		total -= hs[i]
		hs[i] = 0
	}
	// Spare rows go to the growing panes in order, each up to its most;
	// what none of them takes stays below.
	for i, s := range slots {
		if total >= h {
			break
		}
		if !s.grow || hs[i] <= 0 {
			continue
		}
		add := h - total
		if s.most > 0 {
			add = min(add, max(0, s.most-hs[i]))
		}
		hs[i] += add
		total += add
	}
	for i, s := range slots {
		if hs[i] <= 0 {
			continue
		}
		s.draw(c, x, y, w, hs[i])
		y += hs[i]
	}
}

// frame draws rows 0 and 1, the key bar, and returns the body rows.
type body struct{ y, h int }

func chrome(c *canvas, b vm.Board, keys []vm.Key) body {
	topBar(c, 0, b)
	if c.h > 1 {
		nowStrip(c, 1, b.NowLine)
	}
	if c.h >= 4 {
		keyBar(c, c.h-2, keys)
	}
	return body{3, max(0, c.h-6)}
}

// columns places left and right slots: side by side at TwoColumns and wider,
// otherwise the right ones under the left ones.
func columns(c *canvas, bd body, left, right []slot) {
	if c.w >= TwoColumns {
		lw := c.w - RightW - 1
		stack(c, left, 0, bd.y, lw, bd.h)
		stack(c, right, lw+1, bd.y, c.w-lw-1, bd.h)
		return
	}
	all := append(append([]slot{}, left...), right...)
	stack(c, all, 0, bd.y, c.w, bd.h)
}

func keysOr(b vm.Board, def []vm.Key) []vm.Key {
	if len(b.Keys) > 0 {
		return b.Keys
	}
	return def
}

// ---------------------------------------------------------------- plan

func stageLabel(s vm.Stage) string {
	if s.Note == "" {
		return s.Name
	}
	if s.State == "current" && !strings.Contains(s.Note, "/") {
		return s.Name + " · " + s.Note
	}
	return s.Name + " " + s.Note
}

func stages(p pane, dy int, ss []vm.Stage) {
	if len(ss) == 0 {
		p.put(2, dy, dash, sFaint)
		return
	}
	x := 2
	for i, s := range ss {
		if i > 0 {
			x = p.put(x, dy, " → ", sFaint)
		}
		switch s.State {
		case "done":
			x = p.put(x, dy, "✓ "+stageLabel(s), sOK)
		case "current":
			x = p.segs(x, dy, []seg{chip(stageLabel(s), cAcc, "")})
		default:
			x = p.put(x, dy, stageLabel(s), sFaint)
		}
	}
}

func planPane(b vm.Board, full bool) slot {
	want := 3
	if full {
		want = 5
	}
	return slot{want: want, min: 3, prio: 1, draw: func(c *canvas, x, y, w, h int) {
		pl := b.Plan
		title := "Plan"
		if pl.Title != "" {
			title += " · " + pl.Title
		}
		right := ""
		if full {
			right = pl.Accepted
		}
		p := c.box(x, y, w, h, title, right, cAcc, cAcc, "")
		stages(p, 1, pl.Stages)
		if !full {
			return
		}
		thr := []seg{{"throughput ", sDim}}
		if len(pl.Throughput) > 0 {
			thr = append(thr, seg{spark(pl.Throughput), sAcc},
				seg{fmt.Sprintf("  %d merged/h · %d reworked · tokens %s", pl.MergedPerHr, pl.Reworked, tokens(pl.Tokens)), sDim})
		} else {
			thr = append(thr, seg{dash, sFaint})
		}
		p.segs(2, 2, thr)
		n := 45
		if w < 85 {
			n = max(10, w-40)
		}
		bar := progress(pl, n)
		eta := dash
		if pl.ETA > 0 {
			eta = dur(pl.ETA)
		}
		bar = append(bar, seg{fmt.Sprintf("   done %d · active %d · eta %s", pl.Done, pl.Active, eta), sDim})
		p.segs(2, 3, bar)
	}}
}

// ---------------------------------------------------------------- lanes

func laneGlyph(l vm.Lane) (string, st) {
	switch {
	case l.Reviewing:
		return "◎", sVio
	case l.Stale:
		return "⣾", sWarn
	}
	return "⣾", sAcc
}

func lanesPane(b vm.Board) slot {
	n := len(b.Lanes.Items)
	return slot{want: max(n, 1) + 3, min: 4, prio: 0, grow: true, most: max(n, 1) + 4, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Lanes · from worker heartbeats", b.Lanes.Limits, "", "", "")
		taskW := max(10, w-55)
		stepX, timeX, sigX := 30+taskW, w-13, w-8
		for _, hd := range []struct {
			x int
			t string
		}{{5, "LANE"}, {17, "MODEL"}, {30, "TASK"}, {stepX, "STEP"}, {timeX, "TIME"}, {sigX, "SIGNAL"}} {
			p.put(hd.x, 1, hd.t, sFaint)
		}
		if n == 0 {
			p.put(5, 2, "No lanes yet.", sFaint)
			return
		}
		for i, l := range b.Lanes.Items {
			dy := 2 + i
			var bg string
			if l.Selected {
				p.fillRow(1, dy, w-2, cSel)
				bg = cSel
			}
			g, gs := laneGlyph(l)
			gs.bg = bg
			p.put(3, dy, g, gs)
			p.put(5, dy, ljust(cut(or(l.Name, dash), 11), 12), st{bg: bg, b: l.Selected})
			p.put(17, dy, ljust(cut(or(l.Model, dash), 12), 13), st{fg: cDim, bg: bg})
			p.put(30, dy, fit(or(l.Task, dash), taskW), st{bg: bg})
			p.put(stepX, dy, ljust(or(l.Step, dash), 11), st{fg: cDim, bg: bg})
			p.put(timeX, dy, rjust(dur(l.Elapsed), 4), st{bg: bg})
			sig := sOK
			if l.Stale {
				sig = sWarn
			}
			sig.bg = bg
			p.put(sigX, dy, rjust(dur(l.LastSignal), 6), sig)
			p.hit(1, dy, w-1, HitLane, i, "")
		}
	}}
}

// ---------------------------------------------------------------- tasks

func taskState(t vm.Task) (string, st) {
	switch t.State {
	case "merged":
		return "✓ merged", sOK
	case "review":
		return "◎ in review", sVio
	case "building":
		return "› building", sAcc
	case "waiting":
		return "· " + or(t.Note, "waiting"), sFaint
	}
	return or(t.State, dash), sDim
}

func tasksPane(b vm.Board) slot {
	n := len(b.Tasks)
	return slot{want: max(n, 1) + 3, min: 4, prio: 0, draw: func(c *canvas, x, y, w, h int) {
		title := "Tasks"
		if b.Plan.Total > 0 {
			title = fmt.Sprintf("Tasks · %d of %d merged", b.Plan.Done, b.Plan.Total)
		}
		p := c.box(x, y, w, h, title, "⏎ open · r restart", "", "", "")
		p.put(2, 1, "  ID   TITLE                         FILES         WRITER  REVIEW  STATE", sFaint)
		if n == 0 {
			p.put(4, 2, "No tasks yet.", sFaint)
			return
		}
		for i, t := range b.Tasks {
			dy := 2 + i
			p.put(4, dy, or(t.ID, dash), sBold)
			p.put(9, dy, cut(or(t.Title, dash), 29), sInk)
			p.put(39, dy, cut(or(t.Files, dash), 13), sDim)
			p.put(53, dy, cut(or(t.Writer, dash), 7), sInk)
			p.put(61, dy, cut(or(t.Reviewer, dash), 7), sInk)
			s, sty := taskState(t)
			p.put(69, dy, s, sty)
			p.hit(1, dy, w-1, HitTask, i, "")
		}
	}}
}

// ---------------------------------------------------------------- right column

func pctKnown(p int, m vm.Marshal) bool { return p >= 0 && m.Provider != "" }

func marshalPane(b vm.Board) slot {
	return slot{want: 7, min: 4, prio: 2, draw: func(c *canvas, x, y, w, h int) {
		m := b.Marshal
		p := c.box(x, y, w, h, "Marshal", "m switch", cAcc, cAcc, "")
		model := "  " + or(m.Model, dash)
		if m.Effort != "" {
			model += " · effort " + m.Effort
		}
		p.segs(2, 1, []seg{{glyph(m.Provider) + " " + or(m.Provider, dash), sBold}, {model, sInk}})
		if m.InUse {
			p.segs(w-1-8, 1, []seg{{" in use ", st{fg: cOK, bg: cTag, b: true}}})
		}
		p.segs(2, 2, []seg{{"doing   ", sDim}, {or(m.Doing, dash), sInk}})
		ctx := []seg{{"context ", sDim}}
		if pctKnown(m.ContextPct, m) && m.ContextWindow > 0 {
			ctx = append(ctx, gauge(m.ContextPct, cAcc, 10)...)
			ctx = append(ctx, seg{fmt.Sprintf("  %d%% · %s of %s", m.ContextPct, tokens(int64(m.ContextUsed)), tokens(int64(m.ContextWindow))), sDim})
		} else {
			ctx = append(ctx, gauge(-1, cAcc, 10)...)
			ctx = append(ctx, seg{"  " + dash, sDim})
		}
		p.segs(2, 3, ctx)
		lim := []seg{{"limit   ", sDim}}
		if pctKnown(m.Limit5hPct, m) {
			col := cOK
			if m.Limit5hPct < 20 {
				col = cWarn
			}
			wk := dash
			if m.WeeklyPct >= 0 {
				wk = strconv.Itoa(m.WeeklyPct) + "%"
			}
			lim = append(lim, gauge(m.Limit5hPct, col, 10)...)
			lim = append(lim, seg{fmt.Sprintf("  5h %d%% · weekly %s", m.Limit5hPct, wk), sDim})
		} else {
			lim = append(lim, gauge(-1, cOK, 10)...)
			lim = append(lim, seg{"  " + dash, sDim})
		}
		p.segs(2, 4, lim)
		chat := seg{"not bound", sWarn}
		if m.ChatBound {
			in := ""
			if m.InputKnown {
				in = " · input waiting"
				if m.InputClear {
					in = " · input clear"
				}
			}
			chat = seg{"bound to this run" + in, sOK}
		}
		p.segs(2, 5, []seg{{"chat    ", sDim}, chat})
	}}
}

func workerGlyph(wk vm.Worker) string { return or(wk.Glyph, glyph(wk.Provider)) }

func workersPane(b vm.Board) slot {
	n := len(b.Workers)
	return slot{want: max(n, 1) + 2, min: 3, prio: 2, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Workers · providers", "v re-check", "", "", "")
		if n == 0 {
			p.put(2, 1, dash, sFaint)
			return
		}
		for i, wk := range b.Workers {
			dy := 1 + i
			col := tone(wk.TagTone)
			p.put(2, dy, ljust(workerGlyph(wk)+" "+or(wk.Provider, dash), 10), sInk)
			p.segs(12, dy, gauge(wk.LeftPct, col, 10))
			p.segs(24, dy, []seg{{" " + ljust(or(wk.Tag, dash), 7) + " ", st{fg: col, bg: cTag, b: true}}, {" " + or(wk.Detail, ""), sDim}})
		}
	}}
}

func inboxPane(b vm.Board) slot {
	want := 4
	if len(b.Inbox.Lines) > 0 {
		want = len(b.Inbox.Lines) + 2
		if b.Inbox.Next != "" {
			want++
		}
	}
	return slot{want: want, min: 3, prio: 3, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Inbox", "i opens", "", "", "")
		dy := 1
		if b.Inbox.Count == 0 && len(b.Inbox.Lines) == 0 {
			p.put(2, dy, "Nothing waits for you.", st{fg: cOK, b: true})
			dy++
		}
		for i, l := range b.Inbox.Lines {
			p.put(2, dy, l, sInk)
			p.hit(1, dy, w-1, HitInbox, i, "")
			dy++
		}
		if b.Inbox.Next != "" {
			p.put(2, dy, "Next: "+b.Inbox.Next, sDim)
		}
	}}
}

func activityPane(b vm.Board, title string, compact bool) slot {
	n := len(b.Activity)
	return slot{want: max(n, 1) + 2, min: 3, prio: 5, grow: !compact, most: max(n, 1) + 3, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, title, "", "", "", "")
		if n == 0 {
			p.put(2, 1, "Nothing yet.", sFaint)
			return
		}
		for i, e := range b.Activity {
			dy := 1 + i
			text := e.Text
			ts := sInk
			if e.Tone == vm.ToneMuted && !compact {
				ts = sFaint
			}
			count := ""
			if e.Count > 1 {
				count = "×" + strconv.Itoa(e.Count)
			}
			if compact && count != "" {
				text += " " + count
			}
			p.segs(2, dy, []seg{{clock(e.At), sFaint}, {"  " + fit(e.Kind, 6), st{fg: tone(e.Tone)}}, {"  " + text, ts}})
			if count != "" && !compact {
				p.put(w-2-width(count), dy, count, sFaint)
			}
		}
	}}
}

// ---------------------------------------------------------------- screens

var runningKeys = keys("s", "say", "⏎", "open", "a", "architecture", "r", "restart task", "m", "model", "l", "limits", "p", "pause all", "F11", "chat", "space", "actions", "?", "help")

func screenRunning(c *canvas, b vm.Board) {
	bd := chrome(c, b, keysOr(b, runningKeys))
	columns(c, bd,
		[]slot{planPane(b, true), lanesPane(b), tasksPane(b)},
		[]slot{marshalPane(b), workersPane(b), inboxPane(b), activityPane(b, "Activity · grouped", false)})
}

var readyKeys = keys("⏎", "say what to build", "v", "check again", "m", "Marshal model", "?", "help")

func markGlyph(m string) (string, st) {
	switch m {
	case "ok":
		return "✓", sOK
	case "warn":
		return "!", sWarn
	case "bad":
		return "✗", sBad
	}
	return "", sInk
}

func readinessPane(b vm.Board) slot {
	r := b.Ready
	rows := 0
	for _, ch := range r.Checks {
		rows++
		if ch.Hint != "" {
			rows++
		}
	}
	return slot{want: max(rows, 1) + 7, min: 5, prio: 0, grow: true, most: max(rows, 1) + 8, draw: func(c *canvas, x, y, w, h int) {
		col := cOK
		for _, ch := range r.Checks {
			if ch.Mark != "ok" {
				col = cWarn
			}
		}
		title := "Readiness"
		if r.Total > 0 {
			title = fmt.Sprintf("Readiness · %d of %d ready", r.Ready, r.Total)
		}
		right := ""
		if !r.Checked.IsZero() {
			right = "re-checked " + clock(r.Checked)
		}
		p := c.box(x, y, w, h, title, right, col, col, "")
		if len(r.Checks) == 0 {
			p.put(3, 2, "Checking "+dash, sFaint)
		}
		dy := 2
		for i, ch := range r.Checks {
			p.hit(1, dy, w-1, HitCheck, i, "")
			g, gs := markGlyph(ch.Mark)
			gs.b = true
			p.put(3, dy, g, gs)
			p.put(6, dy, ch.Label, sDim)
			p.put(17, dy, ch.Text, sInk)
			if ch.Tag != "" {
				_, ts := markGlyph(ch.Mark)
				p.put(w-3-width(ch.Tag)-2, dy, " "+ch.Tag+" ", st{fg: ts.fg, bg: cTag, b: true})
			}
			dy++
			if ch.Hint != "" {
				p.put(17, dy, ch.Hint, sDim)
				dy++
			}
		}
		x2 := 3
		primary := false
		for i, a := range r.Actions {
			if i > 0 {
				x2 += 2
			}
			label := a.Key + "  " + a.Label
			bx := x2
			switch {
			case a.Key == "⏎":
				primary = true
				x2 = p.segs(x2, h-4, []seg{chip(label, cAcc, "")})
			case !primary:
				x2 = p.segs(x2, h-4, []seg{chip(label, cWarn, cWarnInk)})
			default:
				x2 = p.segs(x2, h-4, []seg{{" " + label + " ", st{fg: cDim, bg: cButton}}})
			}
			p.hit(bx, h-4, x2, HitReady, i, a.Key)
		}
	}}
}

func usagePane(b vm.Board) slot {
	return slot{want: 8, min: 6, prio: 3, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Usage counts", "", "", "", "")
		p.put(2, 1, "Anonymous install and version counts", sInk)
		p.put(2, 2, "go to marshal.blackhat.uz.", sInk)
		p.put(2, 3, "No code, prompts or file names.", sDim)
		sec := st{fg: cDim, bg: cButton}
		var parts []seg
		if b.Ready.TelemetryOn {
			parts = []seg{chip("y keep on", cAcc, ""), {" ", sInk}, {" t turn off ", sec}}
		} else {
			parts = []seg{chip("y turn on", cAcc, ""), {" ", sInk}, {" t keep off ", sec}}
		}
		parts = append(parts, seg{" ", sInk}, seg{" ? what is sent ", sec})
		p.segs(2, 5, parts)
	}}
}

func defaultsPane(b vm.Board) slot {
	n := len(b.Ready.Defaults)
	return slot{want: max(n, 1) + 4, min: 3, prio: 2, grow: true, most: max(n, 1) + 4, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Defaults · this project", "", "", "", "")
		if n == 0 {
			p.put(2, 1, dash, sFaint)
		}
		for i, kv := range b.Ready.Defaults {
			p.put(2, 1+i, ljust(kv[0], 10), sDim)
			p.put(12, 1+i, kv[1], sInk)
		}
	}}
}

func screenReady(c *canvas, b vm.Board) {
	bd := chrome(c, b, keysOr(b, readyKeys))
	right := []slot{}
	if b.Ready.TelemetryAsk {
		right = append(right, usagePane(b))
	}
	right = append(right, defaultsPane(b))
	columns(c, bd, []slot{readinessPane(b)}, right)
}

var planningKeys = keys("F11", "Marshal chat", "s", "say", "a", "architecture", "f", "faster model", "space", "actions", "?", "help")

func stepsPane(b vm.Board) slot {
	n := len(b.Steps.Items)
	return slot{want: max(n, 1) + 7, min: 4, prio: 0, grow: true, most: max(n, 1) + 7, draw: func(c *canvas, x, y, w, h int) {
		right := ""
		if b.Steps.Quiet > 0 || len(b.Steps.Items) > 0 {
			q := "0m"
			if b.Steps.Quiet >= 60e9 {
				q = dur(b.Steps.Quiet)
			}
			right = "quiet " + q
		}
		p := c.box(x, y, w, h, "Marshal steps · from its tool calls", right, "", "", "")
		if n == 0 {
			p.put(2, 1, "No steps yet.", sFaint)
		}
		for i, s := range b.Steps.Items {
			if 1+i >= h-4 {
				break
			}
			p.segs(2, 1+i, []seg{{clock(s.At), sFaint}, {"  " + ljust(s.Kind, 7), sAcc}, {"  " + s.Text, sInk}})
		}
		p.segs(2, h-3, []seg{{"after 10 quiet minutes:  ", sDim}, {" f ", st{fg: cInk, bg: cKey, b: true}}, {"  continue this step on a faster model", sDim}})
	}}
}

func requestsPane(b vm.Board) slot {
	n := len(b.Requests)
	return slot{want: max(n, 1) + 4, min: 3, prio: 3, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Requests to the Marshal", "", "", "", "")
		p.put(2, 1, "SENT   REQUEST                   ANSWER", sFaint)
		if n == 0 {
			p.put(2, 2, dash, sFaint)
		}
		for i, r := range b.Requests {
			ans := or(r.Answer, dash)
			if r.Latency > 0 {
				ans += " " + dur(r.Latency)
			}
			p.segs(2, 2+i, []seg{{clock(r.Sent), sFaint}, {"  " + ljust(cut(r.What, 23), 24), sInk}, {ans, st{fg: tone(r.Tone)}}})
			if r.Tone == vm.ToneNone {
				p.put(2+5+2+24, 2+i, ans, sDim)
			}
		}
	}}
}

func chatPane(b vm.Board) slot {
	return slot{want: 9, min: 3, prio: 2, grow: true, most: 9, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Chat · Marshal", "", "", "", "")
		ch, m := b.Chat, b.Marshal
		binding := seg{"not bound", sWarn}
		if ch.Bound {
			binding = seg{"this run · live", sOK}
		}
		restarts := strconv.Itoa(ch.Restarts)
		if !ch.ReboundAt.IsZero() {
			restarts += " · rebound " + clock(ch.ReboundAt)
		}
		input := seg{"", sDim} // not looked at yet: nothing
		if ch.InputKnown {
			input = seg{"waiting", sWarn}
			if ch.InputClear {
				input = seg{"clear", sOK}
			}
		}
		model := dash
		if m.Provider != "" {
			model = m.Provider + " " + modelName(m.Model)
		}
		ctx := dash
		if pctKnown(m.ContextPct, m) && m.ContextWindow > 0 {
			ctx = strconv.Itoa(m.ContextPct) + "% used"
		}
		rows := []struct {
			k string
			v seg
		}{{"binding", binding}, {"restarts", seg{restarts, sInk}}, {"re-sent", seg{fmt.Sprintf("%d open requests", ch.Resent), sInk}},
			{"input", input}, {"model", seg{model, sInk}}, {"context", seg{ctx, sDim}}}
		for i, r := range rows {
			p.segs(2, 1+i, []seg{{ljust(r.k, 10), sDim}, r.v})
		}
	}}
}

func screenPlanning(c *canvas, b vm.Board) {
	bd := chrome(c, b, keysOr(b, planningKeys))
	columns(c, bd, []slot{planPane(b, false), stepsPane(b)}, []slot{requestsPane(b), chatPane(b)})
}

// base is the screen a card sits over.
func base(c *canvas, b vm.Board) {
	switch {
	case len(b.Lanes.Items) > 0:
		screenRunning(c, b)
	case len(b.Steps.Items) > 0 || len(b.Plan.Stages) > 0:
		screenPlanning(c, b)
	default:
		screenReady(c, b)
	}
}

func screenDecision(c *canvas, b vm.Board) {
	base(c, b)
	d := b.Decision
	if d == nil {
		return
	}
	c.dim()
	topBar(c, 0, b)
	if c.h > 1 {
		nowStrip(c, 1, b.NowLine)
	}
	nodeRows := (len(d.Nodes) + 3) / 4
	h := 5*nodeRows + 7
	w := min(104, c.w-4)
	h = min(h, c.h-4)
	x0, y0 := (c.w-w)/2, 3
	p := c.box(x0, y0, w, h, or(d.Title, "Your turn"), "esc later", cWarn, cWarn, cModal)
	M := cModal
	sum := []seg{{or(d.Summary, dash), st{bg: M}}}
	if d.ChangedNote != "" {
		sum = append(sum, seg{" · ", st{bg: M}}, seg{d.ChangedNote, st{fg: cWarn, bg: M}})
	}
	p.segs(2, 1, sum)
	col := func(i int) int { return 3 + 25*(i%4) }
	row := func(i int) int { return 3 + 5*(i/4) }
	for i, n := range d.Nodes {
		if i >= 8 {
			break
		}
		bc, hz, vt := cRule, "─", "│"
		if n.Changed {
			bc, hz, vt = cWarn, "┄", "┆"
		}
		nx, ny := col(i), row(i)
		p.put(nx, ny, "╭"+strings.Repeat(hz, 20)+"╮", st{fg: bc, bg: M})
		for k, t := range []seg{{cut(n.Name, 19), st{bg: M, b: true}}, {cut(n.Sub, 19), st{fg: cFaint, bg: M}}} {
			p.put(nx, ny+1+k, vt, st{fg: bc, bg: M})
			p.put(nx+2, ny+1+k, t.t, t.s)
			p.put(nx+21, ny+1+k, vt, st{fg: bc, bg: M})
		}
		p.put(nx, ny+3, "╰"+strings.Repeat(hz, 20)+"╯", st{fg: bc, bg: M})
	}
	for _, l := range d.Links {
		a, z := min(l.From, l.To), max(l.From, l.To)
		if a < 0 || z >= len(d.Nodes) || z >= 8 {
			continue
		}
		ls := st{fg: cDim, bg: M}
		if l.Changed {
			ls.fg = cWarn
		}
		switch {
		case z == a+1 && a/4 == z/4:
			arrow := "──▶"
			if l.Both {
				arrow = "◀─▶"
			}
			if l.Changed {
				arrow = "┄┄▶"
			}
			p.put(col(a)+22, row(a)+1, arrow, ls)
		case z == a+4:
			v := "│"
			if l.Changed {
				v = "┆"
			}
			p.put(col(a)+10, row(a)+4, v, ls)
		}
	}
	ly := 2 + 5*nodeRows
	if d.NoDisplay && d.Link != "" {
		p.segs(2, ly, []seg{{"No display here, so open this on the machine:  ", st{fg: cDim, bg: M}}, {d.Link, st{fg: cAcc, bg: M}}})
	} else if d.Link != "" {
		p.segs(2, ly, []seg{{"Link:  ", st{fg: cDim, bg: M}}, {d.Link, st{fg: cAcc, bg: M}}})
	}
	if d.SelfApproval != "" {
		p.put(2, ly+1, "⚠ "+d.SelfApproval, st{fg: cWarn, bg: M})
	}
	x := 2
	for i, a := range d.Actions {
		label := a.Key + "  " + a.Label
		if i > 0 {
			x += 2
		}
		bx := x
		if i == 0 {
			x = p.segs(x, ly+3, []seg{chip(label, cAcc, "")})
		} else {
			x = p.segs(x, ly+3, []seg{button(label)})
		}
		p.hit(bx, ly+3, x, HitAction, i, a.Key)
	}
}

var pausedKeys = keys("⏎", "move", "w", "wait", "d", "details", "x", "stop run", "space", "actions")

func pausedPane(b vm.Board) slot {
	pa := b.Paused
	n := len(pa.Tasks)
	will := strings.Split(pa.Will, "\n")
	willY := 4 + n + 1
	actY := willY + max(2, len(will)) + 1
	return slot{want: actY + 3, min: 5, prio: 0, grow: true, most: actY + 3, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, or(pa.Title, "Paused"), pa.Resets, cBad, cBad, "")
		p.put(2, 1, or(pa.Cause, dash), sInk)
		p.put(2, 3, "  TASK                         PROGRESS                    KEPT", sFaint)
		for i, t := range pa.Tasks {
			p.segs(4, 4+i, []seg{{fit(t.Title, 27) + " ", sInk}, {fit(t.Progress, 27) + " ", sDim}, {t.Kept, sOK}})
		}
		if pa.Will != "" {
			p.segs(2, willY, []seg{{"Enter will: ", sDim}, {will[0], sInk}})
			for i, l := range will[1:] {
				p.put(2, willY+1+i, "            "+l, sDim)
			}
		}
		x2 := 2
		for i, a := range pa.Actions {
			label := a.Key + "  " + a.Label
			if i > 0 {
				x2 += 2
			}
			bx := x2
			if i == 0 {
				x2 = p.segs(x2, actY, []seg{chip(label, cAcc, "")})
			} else {
				x2 = p.segs(x2, actY, []seg{button(label)})
			}
			p.hit(bx, actY, x2, HitAction, i, a.Key)
		}
	}}
}

func providersPane(b vm.Board) slot {
	n := len(b.Workers)
	want := max(n, 1) + 2
	if b.Paused != nil && b.Paused.Room != "" {
		want += 3
	}
	return slot{want: want, min: 3, prio: 2, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Providers", "", "", "", "")
		if n == 0 {
			p.put(2, 1, dash, sFaint)
		}
		for i, wk := range b.Workers {
			dy := 1 + i
			col := tone(wk.TagTone)
			p.put(2, dy, ljust(workerGlyph(wk)+" "+wk.Provider, 12), sInk)
			if wk.LeftPct > 0 {
				p.segs(14, dy, gauge(wk.LeftPct, col, 10))
			} else {
				p.put(14, dy, strings.Repeat("░", 10), st{fg: cBad})
			}
			p.segs(26, dy, []seg{{" " + ljust(wk.Tag, 6) + " ", st{fg: col, bg: cTag, b: true}}, {"  " + wk.Detail, sInk}})
		}
		if b.Paused != nil && b.Paused.Room != "" {
			p.put(2, n+2, b.Paused.Room, sDim)
		}
	}}
}

func runPane(b vm.Board) slot {
	return slot{want: 5, min: 3, prio: 3, grow: true, most: 5, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Run", "", "", "", "")
		bar := progress(b.Plan, 24)
		merged := dash
		if b.Plan.Total > 0 {
			merged = fmt.Sprintf("%d of %d merged", b.Plan.Done, b.Plan.Total)
		}
		p.segs(2, 1, append(bar, seg{"  " + merged, sDim}))
		if b.Paused != nil && b.Paused.Run != "" {
			p.put(2, 2, b.Paused.Run, sDim)
		}
	}}
}

func screenPaused(c *canvas, b vm.Board) {
	if b.Paused == nil {
		screenRunning(c, b)
		return
	}
	bd := chrome(c, b, keysOr(b, pausedKeys))
	columns(c, bd, []slot{pausedPane(b)}, []slot{providersPane(b), runPane(b)})
}

// ---------------------------------------------------------------- chat

var chatKeys = keys("F11", "Board", "Alt+←", "split with Board", "m", "change Marshal model", "s", "say from Board", "i", "inbox", "?", "help")

// paneIn copies the provider's own lines into the box's inside, as they are.
func paneIn(c *canvas, x, y, w, h int, lines []string) {
	for i, l := range lines {
		if i >= h {
			break
		}
		c.putClip(x, y+i, l, sInk, x+w)
	}
}

func screenChat(c *canvas, b vm.Board, lines []string) {
	topBar(c, 0, b)
	if c.h > 1 {
		chatStrip(c, 0, 1, c.w, b, false)
	}
	if c.h >= 4 {
		keyBar(c, c.h-2, keysOr(b, chatKeys))
	}
	h := max(0, c.h-5)
	if h < 3 {
		return
	}
	c.box(0, 2, c.w, h, cliName(b.Marshal.Provider)+" · Marshal's own session", "the provider draws this area", cRule, "", "")
	paneIn(c, 1, 3, c.w-2, h-2, lines)
}

var splitKeys = keys("Alt+←", "chat", "Alt+→", "Board", "F11", "full chat", "m", "Marshal model", "space", "actions")

// SplitChatW is the chat's width in a split of cols columns.
func SplitChatW(cols int) int { return cols / 2 }

func screenSplit(c *canvas, b vm.Board, lines []string) {
	topBar(c, 0, b)
	if c.h >= 4 {
		keyBar(c, c.h-2, keysOr(b, splitKeys))
	}
	lw := SplitChatW(c.w)
	h := max(0, c.h-5)
	if c.h > 1 {
		chatStrip(c, 0, 1, lw, b, true)
	}
	if h >= 3 {
		c.box(0, 2, lw, h, cliName(b.Marshal.Provider), "", cRule, "", "")
		paneIn(c, 1, 3, lw-2, h-2, lines)
	}
	side(c, b, lw+1, c.w-lw-1, 1, h+1)
}

// side is the compact Board: a NOW line and the panes, x..x+w, from row y.
func side(c *canvas, b vm.Board, x, w, y, h int) {
	if h <= 0 {
		return
	}
	n := b.NowLine
	c.fill(x, y, w, 1, cBg)
	bg, fg := cYouOK, cOK
	if !n.FromYouOK {
		bg, fg = cYouW, cWarn
	}
	c.segsClip(x, y, []seg{{" NOW ", st{fg: cAcc, bg: cNow, b: true}}, {" " + or(n.Work, dash) + " ", sInk}, {" from you " + or(n.FromYou, dash) + " ", st{fg: fg, bg: bg, b: true}}}, x+w)
	stack(c, []slot{compactPlan(b), compactLanes(b), compactWorkers(b), activityPane(b, "Activity", true)}, x, y+1, w, h-1)
}

func compactPlan(b vm.Board) slot {
	return slot{want: 4, min: 3, prio: 1, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Plan", "", cAcc, cAcc, "")
		ss := b.Plan.Stages
		// Keep the last two done stages and everything after them.
		first := 0
		for i, s := range ss {
			if s.State == "done" {
				first = i
			}
		}
		first = max(0, first-1)
		stages(p, 1, ss[first:])
		n := 32
		if w < 50 {
			n = max(10, w-20)
		}
		eta := dash
		if b.Plan.ETA > 0 {
			eta = dur(b.Plan.ETA)
		}
		p.segs(2, 2, append(progress(b.Plan, n), seg{"  eta " + eta, sDim}))
	}}
}

func compactLanes(b vm.Board) slot {
	n := len(b.Lanes.Items)
	return slot{want: max(n, 1) + 3, min: 3, prio: 0, grow: true, most: max(n, 1) + 4, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Lanes · heartbeats", "", "", "", "")
		if n == 0 {
			p.put(2, 1, "No lanes yet.", sFaint)
		}
		for i, l := range b.Lanes.Items {
			g, gs := laneGlyph(l)
			p.put(2, 1+i, g, gs)
			p.put(4, 1+i, ljust(cut(l.Name, 9), 10), sInk)
			p.put(14, 1+i, fit(l.Task, min(28, max(0, w-24))), sInk)
			sig := dur(l.LastSignal)
			ss := sOK
			if l.Stale {
				ss = sWarn
			}
			p.put(w-2-width(sig), 1+i, sig, ss)
			p.hit(1, 1+i, w-1, HitLane, i, "")
		}
	}}
}

func compactWorkers(b vm.Board) slot {
	n := len(b.Workers)
	return slot{want: max(n, 1) + 2, min: 3, prio: 2, draw: func(c *canvas, x, y, w, h int) {
		p := c.box(x, y, w, h, "Workers · providers", "", "", "", "")
		if n == 0 {
			p.put(2, 1, dash, sFaint)
		}
		for i, wk := range b.Workers {
			p.put(2, 1+i, ljust(workerGlyph(wk)+" "+wk.Provider, 10), sInk)
			p.segs(12, 1+i, gauge(wk.LeftPct, tone(wk.TagTone), 10))
			v := wk.Tag
			if wk.Detail != "" {
				v += " · " + wk.Detail
			}
			p.put(24, 1+i, v, sDim)
		}
	}}
}

// keys pairs key, label, key, label ...
func keys(kv ...string) []vm.Key {
	out := make([]vm.Key, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, vm.Key{Key: kv[i], Label: kv[i+1]})
	}
	return out
}
