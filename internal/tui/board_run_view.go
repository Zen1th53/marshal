package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/board/vm"
)

// The home screen's pages: the Run view (r), the Inbox view (i) and the
// Readiness view (c). Each reads the same view model the home and the Web
// Board read (boardV2), so the terminal shows what the browser shows
// without a second collector. A page is drawn in the home's place; Esc or 1
// goes back to the home, and every home key still works on a page.
const (
	homePageRun   = "run"
	homePageInbox = "inbox"
	homePageReady = "ready"
)

// homePageNames is each page's title, in the order the tab row lists them.
var homePageNames = []struct{ page, key, name string }{
	{"", "1", "Home"},
	{homePageRun, "r", "Run"},
	{homePageInbox, "i", "Inbox"},
	{homePageReady, "c", "Readiness"},
}

// The page key bar, long and short.
const (
	pageKeysLong  = "j k move   PgUp PgDn page   r run   i inbox   c checks   Esc home   ? keys"
	pageKeysShort = "j k  PgDn  r i c  Esc home  ? keys"
)

// pageLine is one body row of a page; id is set on a row the selection
// can rest on (a lane's name or a task's id).
type pageLine struct {
	text, id string
}

// pageState is a page's selection and scroll across paints: the selected
// row's id, its index at the last paint (where the selection falls back to
// when that row is gone), the first body row drawn and the body's height.
type pageState struct {
	sel          string
	selIdx       int
	scroll, room int
	ids          []string
}

// renderHomePage is page of b in cols x rows. It keeps the selection on
// the same id while rows come, go or move, and scrolls so the selected row
// is drawn. It reads nothing but its arguments and st.
func renderHomePage(v boardVisual, b vm.Board, page string, st *pageState, cols, rows int) []string {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	now := firstTime(b.Now, b.Top.Clock, v.now)
	var left, right []pageLine
	switch page {
	case homePageRun:
		left, right = runWorkLines(v, b, now), runSideLines(v, b, now)
	case homePageInbox:
		left = inboxPageLines(v, b, now)
	default:
		left = readyPageLines(v, b, now)
	}
	// Wide: work on the left, Activity and the Marshal on the right.
	// Narrow: one column, the side sections under the work.
	wide := cols >= 120 && len(right) > 0
	if !wide {
		left = append(left, right...)
		right = nil
	}
	st.ids = st.ids[:0]
	for _, l := range left {
		if l.id != "" {
			st.ids = append(st.ids, l.id)
		}
	}
	if len(st.ids) > 0 {
		if i := slices.Index(st.ids, st.sel); i >= 0 {
			st.selIdx = i
		} else {
			st.selIdx = min(max(st.selIdx, 0), len(st.ids)-1)
			st.sel = st.ids[st.selIdx]
		}
	} else {
		st.sel, st.selIdx = "", 0
	}

	head := []string{v.homeHeader(b.Top, cols), v.pageTabs(page, cols)}
	bottom := 1
	if rows >= 4 {
		bottom = 2
	}
	room := max(0, rows-bottom-len(head))
	if room == 0 {
		head = head[:min(len(head), max(0, rows-bottom))]
	}
	st.room = room
	maxScroll := max(0, len(left)-room)
	st.scroll = min(max(st.scroll, 0), maxScroll)
	if st.sel != "" {
		at := slices.IndexFunc(left, func(l pageLine) bool { return l.id == st.sel })
		if at < st.scroll {
			st.scroll = at
		} else if at >= st.scroll+room {
			st.scroll = at - room + 1
		}
	}
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = strings.Repeat(" ", cols)
	}
	for i, s := range head {
		lines[i] = PadCell(s, cols)
	}
	leftW := cols
	if wide {
		leftW = cols * 3 / 5
	}
	for i := 0; i < room; i++ {
		row := ""
		if j := st.scroll + i; j < len(left) {
			row = v.pageRow(left[j], st.sel, leftW)
		}
		if wide {
			r := ""
			if i < len(right) {
				r = v.pageRow(right[i], "", cols-leftW-1)
			}
			row = PadCell(row, leftW) + " " + r
		}
		lines[len(head)+i] = PadCell(row, cols)
	}
	if bottom == 2 {
		keys := pageKeysLong
		if VisibleLen(" "+keys) > cols {
			keys = pageKeysShort
		}
		if more := len(left) - st.scroll - room; more > 0 {
			keys = fmt.Sprintf("+%d below · ", more) + keys
		}
		lines[rows-2] = PadCell(v.fg("dim", " "+Truncate(keys, cols-1)), cols)
	}
	if v.status == "" {
		v.status = v.inboxPrompt
	}
	if v.status != "" {
		lines[rows-1] = PadCell(" "+Truncate(v.status, cols-1), cols)
	}
	if !v.unicode() {
		return v.ascii(lines, cols)
	}
	return lines
}

// pageTabs is row 1 of a page: the four pages with their keys, the open
// one marked.
func (v boardVisual) pageTabs(page string, cols int) string {
	var parts []string
	for _, p := range homePageNames {
		s := p.key + " " + p.name
		if p.page == page {
			s = v.bold(v.fg("accent", "["+s+"]"))
		} else {
			s = v.fg("dim", s)
		}
		parts = append(parts, s)
	}
	return Truncate(" "+strings.Join(parts, "  "), cols)
}

// pageRow is one body row, w cells wide; the selected row is marked.
func (v boardVisual) pageRow(l pageLine, sel string, w int) string {
	if w <= 0 {
		return ""
	}
	lead := " "
	if l.id != "" {
		lead = "   "
		if l.id == sel {
			lead = " " + v.fg("accent", "▸") + " "
		}
	}
	return Truncate(lead+l.text, w)
}

// section is a heading row.
func (v boardVisual) section(name string) pageLine {
	return pageLine{text: v.bold(name)}
}

// runWorkLines is the Run view's work: stages, progress, lanes and tasks.
func runWorkLines(v boardVisual, b vm.Board, now time.Time) []pageLine {
	pl := b.Plan
	out := []pageLine{v.section("STAGES")}
	if len(pl.Stages) == 0 {
		out = append(out, pageLine{text: "no plan stages yet"})
	} else {
		var st []string
		for _, s := range pl.Stages {
			mark := ""
			switch s.State {
			case "done":
				mark = "✓ "
			case "current":
				mark = "▸ "
			}
			st = append(st, strings.TrimSpace(mark+s.Name+" "+s.Note))
		}
		out = append(out, pageLine{text: strings.Join(st, " · ")})
	}
	if t := joinDot(pl.Title, pl.Accepted); t != "" {
		out = append(out, pageLine{text: t})
	}
	eta := "ETA unknown"
	if pl.ETA > 0 {
		eta = "ETA " + compactDur(pl.ETA)
	}
	out = append(out, pageLine{text: joinDot(
		fmt.Sprintf("%d/%d done", pl.Done, pl.Total),
		fmt.Sprintf("%d active", pl.Active),
		fmt.Sprintf("%d waiting", pl.Waiting),
		eta,
		fmt.Sprintf("%d merged/h", pl.MergedPerHr),
		fmt.Sprintf("%d reworked", pl.Reworked),
		compactTokens(pl.Tokens)+" tokens",
		sparkline(pl.Throughput, v.unicode()),
	)})
	if b.Waiting != nil {
		out = append(out, pageLine{text: "run waits: " + waitText(*b.Waiting, now)})
	}

	out = append(out, pageLine{}, v.section(fmt.Sprintf("LANES (%d)", len(b.Lanes.Items))))
	if b.Lanes.Limits != "" {
		out = append(out, pageLine{text: b.Lanes.Limits})
	}
	if len(b.Lanes.Items) == 0 {
		out = append(out, pageLine{text: "no worker is running"})
	}
	for _, l := range b.Lanes.Items {
		out = append(out, pageLine{id: "lane:" + l.Name, text: laneText(v, l, now)})
	}

	out = append(out, pageLine{}, v.section(fmt.Sprintf("TASKS (%d)", len(b.Tasks))))
	if len(b.Tasks) == 0 {
		out = append(out, pageLine{text: "no tasks yet"})
	}
	for _, t := range b.Tasks {
		out = append(out, pageLine{id: "task:" + t.ID, text: taskText(t, now)})
	}
	return out
}

// laneText is one lane: worker, provider and model, task, step, elapsed,
// last signal and why it waits.
func laneText(v boardVisual, l vm.Lane, now time.Time) string {
	signal := "signal " + firstSet(l.Signal.Label, l.Signal.State)
	if l.LastSignal > 0 {
		signal += " " + compactDur(l.LastSignal) + " ago"
	}
	if l.Stale {
		signal += " " + v.fg("failed", "STALE")
	}
	who := strings.Trim(l.Provider+"/"+l.Model, "/")
	s := joinDot(l.Name, who, l.Task, l.Step, compactDur(l.Elapsed), signal)
	if l.Waiting != nil {
		s += " · waits: " + waitText(*l.Waiting, now)
	} else if l.Note != "" {
		s += " · " + l.Note
	}
	return s
}

// taskText is one task: id, state, title, writer and reviewer, and why it
// waits.
func taskText(t vm.Task, now time.Time) string {
	people := ""
	if t.Writer != "" || t.Reviewer != "" {
		people = strings.Trim("by "+t.Writer+" · review "+t.Reviewer, " ·")
	}
	s := joinDot(t.ID, t.State, t.Title, people)
	switch {
	case t.Waiting != nil:
		s += " · waits: " + waitText(*t.Waiting, now)
	case t.Note != "":
		s += " · " + t.Note
	}
	return s
}

// waitText is why something waits: the reason, its code, what blocks it
// and until when.
func waitText(w vm.Waiting, now time.Time) string {
	s := firstSet(w.Label, w.Code)
	var more []string
	if w.Code != "" && w.Label != "" {
		more = append(more, w.Code)
	}
	if w.BlockerID != "" {
		more = append(more, "on "+w.BlockerID)
	}
	if !w.RetryAt.IsZero() {
		more = append(more, "until "+w.RetryAt.Format("15:04")+" (in "+compactDur(w.RetryAt.Sub(now))+")")
	}
	if len(more) > 0 {
		s += " (" + strings.Join(more, ", ") + ")"
	}
	return s
}

// runSideLines is the Run view's other half: Activity, the Marshal, its
// steps and requests, and the chat's health.
func runSideLines(v boardVisual, b vm.Board, now time.Time) []pageLine {
	out := []pageLine{v.section("ACTIVITY")}
	if len(b.Activity) == 0 {
		out = append(out, pageLine{text: "nothing yet"})
	}
	for _, e := range b.Activity[max(0, len(b.Activity)-6):] {
		out = append(out, pageLine{text: eventText(e)})
	}
	out = append(out, pageLine{}, v.section("MARSHAL"))
	out = append(out, pageLine{text: marshalText(b.Marshal)})
	if b.Marshal.Doing != "" {
		out = append(out, pageLine{text: "doing: " + b.Marshal.Doing})
	}
	st := b.Steps
	steps := "steps"
	if st.Model != "" {
		steps += " · planning on " + st.Model
	}
	if st.Quiet > 0 {
		steps += " · quiet " + compactDur(st.Quiet)
	}
	out = append(out, pageLine{text: steps})
	for _, s := range st.Items[max(0, len(st.Items)-4):] {
		out = append(out, pageLine{text: joinDot(clock(s.At), s.Kind, s.Text)})
	}
	if len(b.Requests) > 0 {
		out = append(out, pageLine{text: "requests"})
	}
	for _, r := range b.Requests[max(0, len(b.Requests)-4):] {
		ans := r.Answer
		if r.Latency > 0 {
			ans += " in " + compactDur(r.Latency)
		}
		out = append(out, pageLine{text: joinDot(clock(r.Sent), r.What, "→ "+ans)})
	}
	out = append(out, pageLine{text: chatText(b.Chat, now)})
	return out
}

// eventText is one Activity line.
func eventText(e vm.Event) string {
	s := joinDot(clock(e.At), e.Kind, e.Text)
	if e.Count > 1 {
		s += fmt.Sprintf(" ×%d", e.Count)
	}
	return s
}

// marshalText is the Marshal's identity, context and quota.
func marshalText(m vm.Marshal) string {
	if m.Provider == "" {
		return "no Marshal chat yet"
	}
	ctx := "context unknown"
	if m.ContextPct >= 0 {
		ctx = fmt.Sprintf("context %d%%", m.ContextPct)
		if m.ContextWindow > 0 {
			ctx += fmt.Sprintf(" (%s/%s)", compactTokens(int64(m.ContextUsed)), compactTokens(int64(m.ContextWindow)))
		}
	}
	return joinDot(m.Provider, m.Model, m.Effort, ctx, pctLeft("5h", m.Limit5hPct), pctLeft("weekly", m.WeeklyPct))
}

func pctLeft(window string, pct int) string {
	if pct < 0 {
		return window + " unknown"
	}
	return fmt.Sprintf("%s %d%% left", window, pct)
}

// chatText is the chat's health: bound, stage, answers, restarts and
// rebinds.
func chatText(c vm.Chat, now time.Time) string {
	bound := "chat not bound"
	if c.Bound {
		bound = "chat bound"
	}
	input := ""
	if c.InputKnown {
		input = "input waiting"
		if c.InputClear {
			input = "input clear"
		}
	}
	rebound := ""
	if !c.ReboundAt.IsZero() {
		rebound = "rebound " + clock(c.ReboundAt)
	}
	return joinDot(bound, c.Stage, fmt.Sprintf("%d answered", c.Answered), fmt.Sprintf("%d restarts", c.Restarts), rebound, fmt.Sprintf("%d resent", c.Resent), input)
}

// inboxPageLines is everything waiting for the owner: the From-you line,
// the decision and paused cards, every queued inbox item, the approvals
// with their age and expiry, and what each waiting task waits for.
func inboxPageLines(v boardVisual, b vm.Board, now time.Time) []pageLine {
	out := []pageLine{v.section("FROM YOU"), {text: firstSet(b.NowLine.FromYou, "nothing")}}
	if d := b.Decision; d != nil {
		out = append(out, pageLine{text: "decision · " + joinDot(d.Kind, d.Title, d.Summary)})
	}
	if p := b.Paused; p != nil {
		resets := ""
		if p.Resets != "" {
			resets = "resets " + p.Resets
		}
		out = append(out, pageLine{text: "paused · " + joinDot(p.Kind, p.Title, resets, p.Cause)})
	}
	out = append(out, pageLine{}, v.section(fmt.Sprintf("QUEUED (%d)", max(b.Inbox.Count, len(b.Inbox.Lines)))))
	if len(b.Inbox.Lines) == 0 {
		out = append(out, pageLine{text: "Nothing waits for you."})
	}
	for i, l := range b.Inbox.Lines {
		out = append(out, pageLine{id: fmt.Sprintf("inbox:%d:%s", i, l), text: l})
	}
	if b.Inbox.Next != "" {
		out = append(out, pageLine{text: "next: " + b.Inbox.Next})
	}
	if len(b.Approvals) > 0 {
		out = append(out, pageLine{}, v.section(fmt.Sprintf("APPROVALS (%d)", len(b.Approvals))))
	}
	for _, a := range b.Approvals {
		age := ""
		if !a.CreatedAt.IsZero() {
			age = "waiting " + compactDur(now.Sub(a.CreatedAt))
		}
		exp := ""
		if !a.ExpiresAt.IsZero() {
			exp = "expires " + a.ExpiresAt.Format("15:04") + " (in " + compactDur(a.ExpiresAt.Sub(now)) + ")"
		}
		var keys []string
		for _, k := range a.Actions {
			keys = append(keys, k.Key+" "+k.Label)
		}
		s := joinDot(a.ID, a.TaskID, "awaiting "+a.Awaiting, a.Status, age, exp, a.Reason, strings.Join(keys, ", "))
		if a.Waiting != nil {
			s += " · " + waitText(*a.Waiting, now)
		}
		out = append(out, pageLine{id: "approval:" + a.ID, text: s})
	}
	var waits []pageLine
	for _, t := range b.Tasks {
		if t.Waiting != nil {
			waits = append(waits, pageLine{id: "task:" + t.ID, text: t.ID + " waits: " + waitText(*t.Waiting, now)})
		}
	}
	if b.Waiting != nil {
		waits = append([]pageLine{{text: "run waits: " + waitText(*b.Waiting, now)}}, waits...)
	}
	if len(waits) > 0 {
		out = append(out, pageLine{}, v.section("WAITING"))
		out = append(out, waits...)
	}
	out = append(out, pageLine{}, pageLine{text: v.fg("dim", "Decide these here (o) or on the Web Board (w).")})
	return out
}

// readyPageLines is the readiness checklist, the providers with their
// limits and caps, and the host.
func readyPageLines(v boardVisual, b vm.Board, now time.Time) []pageLine {
	r := b.Ready
	checked := "not checked yet"
	if !r.Checked.IsZero() {
		checked = "checked " + clock(r.Checked)
	}
	tele := "telemetry off"
	if r.TelemetryOn {
		tele = "telemetry on"
	}
	out := []pageLine{v.section("READINESS"), {text: joinDot(r.SessionLabel, fmt.Sprintf("%d/%d ready", r.Ready, r.Total), checked, tele)}}
	for _, c := range r.Checks {
		mark := map[string]string{"ok": "✓", "warn": "▲", "bad": "×"}[c.Mark]
		s := firstSet(mark, c.Mark) + " " + joinDot(c.Label, c.Text, c.Tag)
		out = append(out, pageLine{id: "check:" + c.Label, text: s})
		if c.Hint != "" {
			out = append(out, pageLine{text: "    " + c.Hint})
		}
	}
	if len(r.Actions) > 0 {
		var keys []string
		for _, k := range r.Actions {
			keys = append(keys, k.Key+" "+k.Label)
		}
		out = append(out, pageLine{text: "actions: " + strings.Join(keys, " · ")})
	}
	if len(r.Defaults) > 0 {
		out = append(out, pageLine{}, v.section("DEFAULTS"))
		for _, d := range r.Defaults {
			out = append(out, pageLine{text: d[0] + ": " + d[1]})
		}
	}
	out = append(out, pageLine{}, v.section("PROVIDERS"))
	if b.Lanes.Limits != "" {
		out = append(out, pageLine{text: "caps: " + b.Lanes.Limits})
	}
	for _, w := range b.Workers {
		left := ""
		if w.LeftPct >= 0 {
			left = fmt.Sprintf("%d%% left", w.LeftPct)
		}
		out = append(out, pageLine{text: joinDot(w.Provider, w.Tag, w.Detail, left, w.Reason)})
	}
	for _, l := range b.Limits {
		var rows []string
		for _, row := range l.Rows {
			pct := "unknown"
			if row.PercentLeft >= 0 {
				pct = fmt.Sprintf("%d%% left", row.PercentLeft)
			}
			s := strings.TrimSpace(firstSet(row.Window, "window") + " " + pct)
			if !row.ResetsAt.IsZero() {
				s += " resets " + row.ResetsAt.Format("Mon 15:04")
			}
			rows = append(rows, s)
		}
		out = append(out, pageLine{text: "limits " + joinDot(strings.Trim(l.Provider+" "+l.Model, " "), string(l.State), l.Reason, strings.Join(rows, " · "))})
	}
	out = append(out, pageLine{}, v.section("HOST"), pageLine{text: hostText(b.System)})
	return out
}

// hostText is the host's state with the keys that reach more: 2 the Host
// view, F10 the update, F6 what MCP failed on.
func hostText(s vm.System) string {
	parts := []string{fmt.Sprintf("cpu %d%% · memory %d%% · disk %d%% · %d°C (2)", s.CPUPct, s.MemPct, s.DiskPct, s.TempC)}
	if s.ThermalGuard {
		g := "thermal guard"
		if s.ThermalCap > 0 {
			g += fmt.Sprintf(" caps lanes at %d", s.ThermalCap)
		}
		parts = append(parts, g)
	}
	if s.LowDisk {
		parts = append(parts, "low disk")
	}
	if s.UpdateAvailable != "" {
		parts = append(parts, "update "+s.UpdateAvailable+" (F10)")
	}
	if s.MCPFailed {
		parts = append(parts, "MCP failed (F6)")
	}
	return strings.Join(parts, " · ")
}

// hostAbnormal is the home's host line, "" while the host, the update and
// MCP need nothing.
func hostAbnormal(s vm.System) string {
	if s.Notice == "" && !s.ThermalGuard && !s.LowDisk && s.UpdateAvailable == "" && !s.MCPFailed {
		return ""
	}
	notice := strings.TrimPrefix(s.Notice, "host: ")
	parts := []string{notice}
	if s.ThermalGuard && !strings.Contains(notice, "thermal") {
		parts = append(parts, "thermal guard")
	}
	if s.ThermalCap > 0 {
		parts = append(parts, fmt.Sprintf("lanes capped at %d", s.ThermalCap))
	}
	if s.LowDisk && !strings.Contains(notice, "disk") {
		parts = append(parts, fmt.Sprintf("low disk %d%%", s.DiskPct))
	}
	if s.UpdateAvailable != "" {
		parts = append(parts, "update "+s.UpdateAvailable+" (F10)")
	}
	if s.MCPFailed {
		parts = append(parts, "MCP failed (F6)")
	}
	return joinDot(append(parts, "2 host view")...)
}

// homeWork is the home's Work line: active work against the cap and what
// waits.
func homeWork(b vm.Board) string {
	m := b.Mode
	active := len(b.Lanes.Items)
	if m.Active != nil {
		active = *m.Active
	}
	s := fmt.Sprintf("%d active", active)
	if m.EffectiveCap != nil {
		s += fmt.Sprintf(" / cap %d", *m.EffectiveCap)
	}
	if b.Plan.Waiting > 0 {
		s += fmt.Sprintf(" · %d waiting", b.Plan.Waiting)
	}
	return s
}

// homeCompact is up to the items that fit in room cells, then "+N more".
func homeCompact(items []string, room int) string {
	s := ""
	for i, it := range items {
		next := it
		if s != "" {
			next = s + " · " + it
		}
		rest := ""
		if left := len(items) - i - 1; left > 0 {
			rest = fmt.Sprintf(" · +%d more", left)
		}
		if s != "" && VisibleLen(next+rest) > room {
			return s + fmt.Sprintf(" · +%d more", len(items)-i)
		}
		s = next
	}
	return s
}

// homeLanes is the home's compact lane line.
func homeLanes(b vm.Board) []string {
	var out []string
	for _, l := range b.Lanes.Items {
		// The task's id only ("T03", "review T02"): the Run view has its title.
		task := strings.Fields(l.Task)
		if len(task) > 2 || len(task) == 2 && task[0] != "review" {
			task = task[:1+min(1, len(task)-1)*boolInt(task[0] == "review")]
		}
		s := strings.Join(append(append([]string{l.Name}, task...), strings.Fields(compactDur(l.Elapsed))...), " ")
		if l.Stale {
			s += " STALE"
		}
		out = append(out, s)
	}
	return out
}

// homeTasks is the home's compact task line: tasks not merged yet.
func homeTasks(b vm.Board) []string {
	var out []string
	for _, t := range b.Tasks {
		if t.State == "merged" {
			continue
		}
		s := t.ID + " " + t.State
		if t.Waiting != nil {
			s += " (" + firstSet(t.Waiting.Code, t.Waiting.Label) + ")"
		} else if t.Note != "" {
			s += " (" + t.Note + ")"
		}
		if t.Provider != "" {
			s += " · " + t.Provider
		}
		out = append(out, s)
	}
	return out
}

// homeMarshal is the home's Marshal line: who it is, its context and its
// quota left, in short.
func homeMarshal(m vm.Marshal) string {
	ctx := ""
	if m.ContextPct >= 0 {
		ctx = fmt.Sprintf("context %d%%", m.ContextPct)
	}
	quota := func(w string, p int) string {
		if p < 0 {
			return ""
		}
		return fmt.Sprintf("%s %d%%", w, p)
	}
	return joinDot(strings.TrimSpace(m.Provider+" "+m.Model+" "+m.Effort), ctx, quota("5h", m.Limit5hPct), quota("weekly", m.WeeklyPct))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func firstTime(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Now()
}

func clock(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("15:04")
}

// compactDur is "12s", "4m", "1h05m", "" for none.
func compactDur(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

// compactTokens is "820", "34k", "1.2M".
func compactTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}

// sparkline is the throughput as bars, ASCII ones without Unicode.
func sparkline(xs []int, unicode bool) string {
	if len(xs) == 0 {
		return ""
	}
	bars := []rune("▁▂▃▄▅▆▇█")
	if !unicode {
		bars = []rune("_.-=+*#@")
	}
	top := slices.Max(xs)
	var sb strings.Builder
	for _, x := range xs {
		i := 0
		if top > 0 {
			i = min(7, max(0, x*7/top))
		}
		sb.WriteRune(bars[i])
	}
	return sb.String()
}

// handleHomePageKey is a key while a page is open: Esc and 1 go back to the
// home, j k and the arrows move the selection, PgUp PgDn a page, and r i c
// switch pages. Any other key goes on to the home's keys.
func (w *Workspace) handleHomePageKey(event KeyEvent) bool {
	ui := &w.boardUI
	if ui.homePage == "" {
		return false
	}
	st := &ui.homePageState
	move := func(n int) {
		if len(st.ids) == 0 {
			st.scroll = max(0, st.scroll+n)
			return
		}
		st.selIdx = min(max(st.selIdx+n, 0), len(st.ids)-1)
		st.sel = st.ids[st.selIdx]
	}
	switch {
	case event.Type == KeyEsc, event.Type == KeyRune && event.Rune == '1':
		ui.homePage = ""
	case event.Type == KeyDown, event.Type == KeyRune && event.Rune == 'j':
		move(1)
	case event.Type == KeyUp, event.Type == KeyRune && event.Rune == 'k':
		move(-1)
	case event.Type == KeyPgDn:
		move(max(1, st.room))
	case event.Type == KeyPgUp:
		move(-max(1, st.room))
	default:
		return false
	}
	return true
}

// openHomePage opens page over the home, or the home again when it is
// open already. A new page starts at its top.
func (w *Workspace) openHomePage(page string) {
	ui := &w.boardUI
	if ui.homePage == page {
		ui.homePage = ""
		return
	}
	w.dropDecisionFocus()
	ui.homePage = page
	ui.homePageState = pageState{}
}

// homePageKey is the page a home key opens, "" for none.
func homePageKey(r rune) string {
	switch r {
	case 'r':
		return homePageRun
	case 'i':
		return homePageInbox
	case 'c':
		return homePageReady
	}
	return ""
}
