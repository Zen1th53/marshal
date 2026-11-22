package tui

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// flowIn is everything the flow collector reads for one frame. The
// builders below are pure over it, so each is tested without a terminal.
type flowIn struct {
	now              time.Time
	version, project string
	hasRun           bool
	panel            *MarshalPanel
	run              marshal.Run
	// v is the Board as the classic paint sees it: the run's Board with the
	// live inbox, the owner gates and the open cards. Its own rules (the
	// run word, the inbox count, the stop) are reused so both Boards agree.
	v                   boardVisual
	planning, goalGiven bool
	goalSince           time.Time
	goalText            string // the goal the Marshal works on, before a run
	milestone           *planningMark
	archRev             int64
	archAccepted        bool
	notes               []foldedNote
	steps               []marshalStep
	lastCall            time.Time
	stepsProvider       string
	stepsModel          string
	chatProvider        string
	chatModel           string
	marshalProvider     string
	marshalModel        string // as the top bar names it ("Opus 5.5")
	chatBlocked         int
	chatWaiting         int
	// chatAsked is set when the Marshal's last chat turn asked the owner
	// something and the chat's input sits idle (chatAskedIn).
	chatAsked    bool
	limits       map[string]int
	globalCap    int
	selectedLane string
	// draftRejected is the Marshal's plan draft the runtime refused and no
	// imported draft has replaced yet; nil for none.
	draftRejected *draftRejectMark
}

// fixingPlan reports whether the refused draft is what the Board shows: for
// the run it was refused on, while that run has no stored plan to run. A
// superseding draft refused during a run leaves that run on the Board (the
// refusal is in Activity), so an abandoned replan never hides live work.
func (in flowIn) fixingPlan() bool {
	r := in.draftRejected
	if r == nil {
		return false
	}
	if !in.hasRun {
		return true
	}
	if r.runID != "" && in.panel != nil && in.panel.RunID != "" && r.runID != in.panel.RunID {
		return false
	}
	state := runState(in.run, in.v.board)
	return state == "" || state == marshal.Drafting
}

// stageNames are the Plan pipeline's stages as the Board names them.
var stageNames = []string{"goal", "reqs", "design", "arch", "tasks", "build", "review", "merge"}

const (
	stageArch  = 3
	stageTasks = 4
	stageBuild = 5
	stageRev   = 6
	stageMerge = 7
)

// staleSignal is how long a lane may go without a heartbeat before Board v2
// calls it stale. Workers beat every 30 s, so this is two missed beats; the
// classic Board calls a lane lost later, at boardmodel.WorkerBeatGrace.
const staleSignal = 60 * time.Second

// taskCounts are a run's tasks by what they wait on.
type taskCounts struct {
	working, review, queued, parked, merged, cancelled, total int
}

func countTasks(tasks []boardmodel.TaskRow) taskCounts {
	c := taskCounts{total: len(tasks)}
	for _, t := range tasks {
		switch t.State {
		case boardmodel.TaskWorking:
			c.working++
		case boardmodel.TaskReview, boardmodel.TaskAccepted:
			c.review++
		case boardmodel.TaskQueued, boardmodel.TaskRework:
			c.queued++
		case boardmodel.TaskParked:
			c.parked++
		case boardmodel.TaskMerged:
			c.merged++
		case boardmodel.TaskCancelled:
			c.cancelled++
		}
	}
	return c
}

// flowTop is the top bar.
func flowTop(in flowIn) vm.TopBar {
	t := vm.TopBar{Version: in.version, Project: in.project, Clock: in.now, Inbox: in.v.inboxCount()}
	if in.hasRun {
		b := in.v.board
		t.Run = firstSet(firstLine(b.Run.Title), b.Run.ID)
		for _, l := range b.Lanes {
			if liveLane(l) {
				t.Workers++
			}
		}
	}
	t.State, t.StateTone = flowState(in)
	return t
}

// flowState is the top bar's state word and its tone. With a run it is the
// classic header's run word (board_stop.go runWord) in capitals.
func flowState(in flowIn) (string, vm.Tone) {
	if in.fixingPlan() {
		if in.draftRejected.overdue(in.now) {
			return "YOUR TURN", vm.ToneWarn
		}
		return "FIXING PLAN", vm.ToneWarn
	}
	if !in.hasRun {
		switch {
		case in.v.ownerWaits() || in.milestone != nil && in.milestone.OwnerTurn:
			return "YOUR TURN", vm.ToneWarn
		case in.planning || in.milestone != nil || in.goalGiven:
			return "PLANNING", vm.ToneInfo
		}
		return "READY", vm.ToneMuted
	}
	b := in.v.board
	if in.v.quotaCardActive() {
		c := countTasks(b.Tasks)
		if n := c.total - c.merged - c.cancelled; n > 0 {
			return fmt.Sprintf("%d PAUSED", n), vm.ToneWarn
		}
		return "PAUSED", vm.ToneWarn
	}
	phase, _, _ := in.v.runWord()
	switch phase {
	case phaseUserPaused:
		return "PAUSED", vm.ToneWarn
	case phaseStopped:
		return "PAUSED", vm.ToneBad
	case phaseWaiting:
		return "YOUR TURN", vm.ToneWarn
	case phaseReviewing:
		return "REVIEWING", vm.ToneVio
	case phaseDone:
		return "DONE", vm.ToneOK
	case phaseMerging:
		return "MERGING", vm.ToneInfo
	case phaseFinalCheck:
		return "FINAL CHECK", vm.ToneInfo
	case phasePlanning:
		return "PLANNING", vm.ToneInfo
	case phaseStarting:
		return "STARTING", vm.ToneMuted
	case phaseNotRunning:
		return "NOT RUNNING", vm.ToneWarn
	}
	return "RUNNING", vm.ToneOK
}

// flowNowLine is the NOW row. FromYou is never empty.
func flowNowLine(in flowIn) vm.NowLine {
	n := vm.NowLine{Marshal: firstSet(marshalModelLabel(in.marshalProvider, firstSet(in.marshalModel, in.chatModel)), "Marshal")}
	if doing := flowDoing(in); doing != "" {
		n.Marshal += " · " + doing
	}
	n.Work = flowWork(in)
	n.FromYou, n.FromYouOK = fromYou(in)
	n.ChatAsked = n.FromYou == vm.ChatAskedLine
	return n
}

// flowDoing is what the Marshal does now, in a few words.
func flowDoing(in flowIn) string {
	var doing string
	if in.fixingPlan() {
		r := in.draftRejected
		doing = "fixing the plan the runtime refused"
		if in.now.After(r.since) {
			doing += " · " + goalElapsed(in.now.Sub(r.since))
		}
		if in.chatWaiting > 0 {
			doing = joinDot(doing, countOf(in.chatWaiting, "request")+" to decide")
		}
		return doing
	}
	if !in.hasRun {
		switch m := in.milestone; {
		case m != nil:
			doing = m.Now
			if !m.Since.IsZero() && in.now.After(m.Since) {
				doing += " · " + goalElapsed(in.now.Sub(m.Since))
			}
		case in.planning:
			doing = "planning the tasks"
		case in.goalGiven:
			doing = "working on your goal"
		default:
			doing = "waiting for a goal"
		}
		return doing
	}
	b := in.v.board
	switch runState(in.run, b) {
	case marshal.Drafting:
		doing = "drafting the plan"
	case marshal.Approved, marshal.Dispatching:
		switch {
		case in.v.onlyReviewing():
			doing = "reviewing hand-ins"
		case b.Run.Running > 0 || in.v.liveLanes() > 0:
			doing = "watching the work"
		default:
			doing = "starting tasks"
		}
	case marshal.Reviewing:
		doing = "reviewing hand-ins"
	case marshal.Merging:
		doing = "merging"
	case marshal.Verifying:
		doing = "final check"
	case marshal.AwaitingUser:
		doing = "waiting for you"
	case marshal.Closed:
		doing = "done"
	}
	if in.chatWaiting > 0 {
		doing = joinDot(doing, countOf(in.chatWaiting, "request")+" to decide")
	}
	return doing
}

// runState is the run's state, from the Board's head when the snapshot has
// none.
func runState(run marshal.Run, b boardmodel.Board) marshal.RunState {
	if run.State != "" {
		return run.State
	}
	return b.Run.State
}

// flowWork is what the workers do: "8 running · 4 queued".
func flowWork(in flowIn) string {
	if !in.hasRun {
		return "none yet"
	}
	c := countTasks(in.v.board.Tasks)
	var parts []string
	if c.working > 0 {
		parts = append(parts, fmt.Sprintf("%d running", c.working))
	}
	if c.review > 0 {
		parts = append(parts, fmt.Sprintf("%d in review", c.review))
	}
	if c.queued > 0 {
		parts = append(parts, fmt.Sprintf("%d queued", c.queued))
	}
	if c.parked > 0 {
		parts = append(parts, fmt.Sprintf("%d stuck", c.parked))
	}
	if len(parts) > 0 {
		return strings.Join(parts, " · ")
	}
	if c.total > 0 && c.merged == c.total-c.cancelled {
		return fmt.Sprintf("all %d merged", c.merged)
	}
	return "none running"
}

// ownerAsk is one thing that waits for the owner. inbox marks the ones the
// inbox counts.
type ownerAsk struct {
	line  string
	inbox bool
	ref   string // what a click on its inbox line reaches: v2RefInbox or v2RefGate and the id
}

// The refs of the inbox lines a click reaches (boardv2_mouse.go).
const (
	v2RefInbox = "inbox:"
	v2RefGate  = "gate:"
)

// ownerAsks is everything that waits for the owner now, in the order the
// owner should answer it: decisions in the inbox, the gates and cards, then
// the stops and the chat.
func ownerAsks(in flowIn) []ownerAsk {
	v := in.v
	var out []ownerAsk
	ref := ""
	add := func(inbox bool, line string) {
		if line = strings.TrimSpace(cleanBoard(line)); line != "" {
			out = append(out, ownerAsk{line: Truncate(line, 72), inbox: inbox, ref: ref})
		}
		ref = ""
	}
	for _, it := range v.board.Inbox {
		ref = v2RefInbox + it.Request.Key()
		add(true, inboxAskLine(it))
	}
	for _, g := range v.gates {
		if !g.sent {
			ref = v2RefGate + g.key
			add(true, g.kind+" "+g.title)
		}
	}
	// The owner cards, in the decision card's order (ownerCardOrder).
	for _, kind := range (ownerCardsOpen{guard: v.guard != nil, next: v.next != nil, ask: v.ask != nil, req: v.req != nil, change: v.change != nil}).kinds() {
		switch kind {
		case decisionGuard:
			add(true, "changed files: keep or undo them")
		case decisionNext:
			add(true, "the run is done: choose what is next")
		case decisionAsk:
			add(true, "the Marshal asks: "+firstLine(v.ask.Question))
		case decisionRequirements:
			add(true, "check the requirements")
		case decisionChange:
			add(true, "confirm the plan change")
		}
	}
	if v.quotaCardActive() {
		add(false, "limits reached: choose how to continue")
	} else if s, ok := v.stopNotice(); ok {
		if s.waiting {
			add(false, "run paused: "+s.reason)
		} else {
			add(false, "run stopped: "+firstSet(s.reason, "choose how to recover"))
		}
	}
	if v.board.Run.UserPaused && in.hasRun {
		add(false, "you paused the run: resume when ready")
	}
	if in.chatBlocked > 0 {
		add(false, "send or clear your unsent text in the Marshal chat")
	}
	if m := in.milestone; !in.hasRun && m != nil && m.OwnerTurn && len(out) == 0 {
		add(false, m.Now)
	}
	if r := in.draftRejected; in.fixingPlan() && r.overdue(in.now) {
		// The Marshal was told the reason and has not sent a fix in time:
		// the owner looks at the chat (Activity has the reason).
		add(false, "plan refused "+goalElapsed(in.now.Sub(r.since))+" ago, no fix yet: open the Marshal chat")
	}
	return out
}

// inboxAskLine is one permission request in a few words.
func inboxAskLine(it boardmodel.InboxItem) string {
	r := it.Request
	object := firstSet(firstLine(r.Object), it.Canonical)
	var line string
	switch it.Kind {
	case boardmodel.InboxEgress:
		line = "allow network access to " + object
	case boardmodel.InboxTool:
		line = "allow the Marshal's tool " + object
	case boardmodel.InboxRead:
		line = "allow reading " + object
	case boardmodel.InboxCredential:
		line = "allow credential " + object
	case boardmodel.InboxMemory:
		line = "keep memory " + object
	default:
		line = "confirm " + object
	}
	if r.TaskID != "" {
		line = r.TaskID + ": " + line
	}
	return line
}

// fromYou is the NOW row's "from you": the first thing that waits for the
// owner (and how many more), or "nothing" with the next expected ask.
func fromYou(in flowIn) (string, bool) {
	asks := ownerAsks(in)
	if len(asks) == 0 && in.chatAsked {
		// Nothing on a card waits, but the Marshal's chat does: its last
		// turn asked the owner something.
		return vm.ChatAskedLine, false
	}
	if len(asks) == 0 {
		if next := nextAsk(in); next != "" {
			return "nothing · next ask: " + next, true
		}
		return "nothing", true
	}
	line := asks[0].line
	if len(asks) > 1 {
		line += fmt.Sprintf(" · +%d more", len(asks)-1)
	}
	return line, false
}

// nextAsk is the next thing the owner will be asked, "" when none is
// expected: the architecture and the plan while planning, task acceptance
// when the owner accepts tasks, and the final review before merge unless
// the owner already authorized the close.
func nextAsk(in flowIn) string {
	if !in.hasRun {
		switch m := in.milestone; {
		case m != nil && m.Step < stageArch:
			return "architecture review"
		case m != nil && m.Step == stageArch && !m.OwnerTurn && !m.Applying:
			return "architecture review"
		case m != nil, in.planning:
			return "plan approval"
		case in.goalGiven:
			return "architecture review"
		}
		return ""
	}
	switch runState(in.run, in.v.board) {
	case marshal.Drafting, "":
		// No stored plan yet (a panel before the first import, for example
		// after a refused draft): the plan is what the owner approves next.
		return "plan approval"
	case marshal.Closed:
		return ""
	}
	c := countTasks(in.v.board.Tasks)
	if in.run.Settings.AcceptanceMode == marshal.AcceptUser && c.merged+c.cancelled < c.total {
		return "task acceptance"
	}
	if !in.run.ValidCloseAuthorization() {
		return "final review before merge"
	}
	return ""
}

// flowInbox is the Inbox pane.
func flowInbox(in flowIn) vm.Inbox {
	ib := vm.Inbox{Count: in.v.inboxCount(), Next: nextAsk(in)}
	for _, a := range ownerAsks(in) {
		if a.inbox {
			ib.Lines = append(ib.Lines, a.line)
			ib.Refs = append(ib.Refs, a.ref)
		}
	}
	return ib
}

// flowPlan is the Plan pane: the stage pipeline, counts, ETA and rates.
func flowPlan(in flowIn) vm.Plan {
	var p vm.Plan
	if !in.hasRun && !in.goalGiven && !in.planning && in.milestone == nil {
		return p
	}
	b := in.v.board
	c := countTasks(b.Tasks)
	p.Stages = flowStages(in, c)
	if !in.hasRun {
		// Before a run the Plan pane names the goal being planned.
		if p.Title = in.goalText; p.Title == "" && in.goalGiven {
			p.Title = "your goal"
		}
		return p
	}
	p.Done, p.Active, p.Waiting, p.Total = c.merged, c.working+c.review, c.queued+c.parked, c.total
	if c.total > 0 {
		p.Title = countOf(c.total, "task")
	}
	if !b.Run.Started.IsZero() {
		p.Accepted = "accepted"
		if in.run.PlanVersion > 0 {
			p.Accepted += fmt.Sprintf(" rev %d", in.run.PlanVersion)
		}
		p.Accepted += " · " + b.Run.Started.Local().Format("15:04")
	}
	p.ETA = flowETA(b, c, in.now)
	p.Throughput, p.MergedPerHr = throughput(b.Events, b.Run.ID, in.now)
	for _, t := range b.Tasks {
		p.Reworked += t.Returns
	}
	if b.Run.Tokens.Known {
		p.Tokens = b.Run.Tokens.Value
	}
	return p
}

// flowStages maps the run (or the planning milestone before one exists)
// onto goal → reqs → design → arch → tasks → build → review → merge.
func flowStages(in flowIn, c taskCounts) []vm.Stage {
	cur, note := -1, ""
	allDone := false
	if !in.hasRun {
		switch m := in.milestone; {
		case m != nil && m.Step >= 0 && m.Step < len(app.PlanningMilestoneSteps):
			cur = m.Step
			if !m.Since.IsZero() && in.now.After(m.Since) {
				note = goalElapsed(in.now.Sub(m.Since))
			}
		case in.planning:
			cur = stageTasks
		case in.goalGiven:
			cur = 1
			if !in.goalSince.IsZero() && in.now.After(in.goalSince) {
				note = goalElapsed(in.now.Sub(in.goalSince))
			}
		}
	} else {
		state := runState(in.run, in.v.board)
		if state == marshal.AwaitingUser && in.run.Pause != nil && in.run.Pause.ResumeState != "" {
			state = in.run.Pause.ResumeState
		}
		switch state {
		case marshal.Drafting:
			cur = stageTasks
		case marshal.Reviewing:
			cur, note = stageRev, reviewNote(c)
		case marshal.Merging:
			cur = stageMerge
		case marshal.Verifying:
			cur, note = stageMerge, "final check"
		case marshal.Closed:
			allDone = true
		default:
			switch {
			case c.total > 0 && c.merged+c.cancelled == c.total:
				cur = stageMerge
			case c.working+c.queued+c.parked == 0 && c.review > 0:
				cur, note = stageRev, reviewNote(c)
			default:
				cur, note = stageBuild, fmt.Sprintf("%d/%d", c.merged, c.total)
			}
		}
	}
	out := make([]vm.Stage, len(stageNames))
	for i, name := range stageNames {
		s := vm.Stage{Name: name, State: "todo"}
		switch {
		case allDone || i < cur:
			s.State = "done"
		case i == cur:
			s.State, s.Note = "current", note
		}
		if i == stageArch && in.archRev > 0 && (in.archAccepted || s.State != "todo") {
			s.Note = strings.TrimSpace(fmt.Sprintf("rev %d %s", in.archRev, s.Note))
		}
		if i == stageBuild && s.State == "done" && c.total > 0 {
			s.Note = fmt.Sprintf("%d/%d", c.merged, c.total)
		}
		out[i] = s
	}
	return out
}

func reviewNote(c taskCounts) string {
	if c.review == 0 {
		return ""
	}
	return fmt.Sprintf("%d in review", c.review)
}

// flowETA is the average time a merged task took, times the tasks left,
// over the lanes that work them; 0 when no task has merged yet.
func flowETA(b boardmodel.Board, c taskCounts, now time.Time) time.Duration {
	var sum time.Duration
	n := 0
	for _, t := range b.Tasks {
		if t.State != boardmodel.TaskMerged {
			continue
		}
		if d, ok := t.Elapsed(now); ok && d > 0 {
			sum += d
			n++
		}
	}
	left := c.total - c.merged - c.cancelled
	if n == 0 || left <= 0 {
		return 0
	}
	lanes := 0
	for _, l := range b.Lanes {
		if liveLane(l) {
			lanes++
		}
	}
	if lanes == 0 {
		lanes = b.Run.LaneCap
	}
	lanes = max(1, min(lanes, left))
	return (sum / time.Duration(n)) * time.Duration(left) / time.Duration(lanes)
}

// throughput is the run's merges per 5 minutes over the last hour, oldest
// first, and their total.
func throughput(evs []events.Event, runID string, now time.Time) ([]int, int) {
	const buckets, width = 12, 5 * time.Minute
	out := make([]int, buckets)
	total := 0
	for _, e := range evs {
		if e.Type != events.EventTypeMarshalTaskMerged || (runID != "" && e.RunID != runID) {
			continue
		}
		age := now.Sub(e.At)
		if age < 0 || age >= buckets*width {
			continue
		}
		out[buckets-1-int(age/width)]++
		total++
	}
	return out, total
}

// flowLanes is the Lanes pane, one row per live worker invocation.
func flowLanes(in flowIn) vm.Lanes {
	var out vm.Lanes
	out.Limits = limitsText(in.limits, in.globalCap, in.v.board.Lanes)
	if !in.hasRun {
		return out
	}
	b := in.v.board
	for _, l := range b.Lanes {
		row := vm.Lane{Name: l.Worker, Provider: l.Provider, Selected: in.selectedLane != "" && l.Worker == in.selectedLane}
		if t, ok := taskRow(b, l.TaskID); ok {
			row.Task = strings.TrimSpace(t.ID + " " + firstLine(t.Title))
		} else {
			row.Task = l.TaskID
		}
		row.Model = taskModel(in.run, l.TaskID)
		switch {
		case l.State == boardmodel.LaneReview:
			row.Step, row.Reviewing = "reviewing", true
			row.Task = "review " + row.Task
		case l.State == boardmodel.LaneLost:
			row.Step = "no worker"
		case l.State == boardmodel.LaneRework:
			row.Step = "rework"
		case l.State == boardmodel.LaneParked:
			row.Step = "stuck"
		case l.State == boardmodel.LaneIdle:
			row.Step = "idle"
		case l.Status != "" && l.Status != "working":
			row.Step = l.Status
		default:
			row.Step = "working"
		}
		if !l.Started.IsZero() && in.now.After(l.Started) {
			row.Elapsed = in.now.Sub(l.Started)
		}
		if l.Heartbeat && !l.Beat.IsZero() {
			row.LastSignal = max(0, in.now.Sub(l.Beat))
			row.Stale = row.LastSignal > staleSignal
		}
		out.Items = append(out.Items, row)
	}
	return out
}

// taskModel is the model a run's task was routed to, "" when not recorded.
func taskModel(run marshal.Run, taskID string) string {
	for _, t := range run.Tasks {
		if t.PlanTaskID == taskID && t.WorkerSelection != nil {
			return firstSet(t.WorkerSelection.SelectedModel, t.WorkerSelection.RequestedModel)
		}
	}
	return ""
}

// limitsText is "limit claude 4 · agy 4 · total 12": the providers with a
// lane on the Board (every provider with a limit when none has), then the
// total.
func limitsText(limits map[string]int, total int, lanes []boardmodel.Lane) string {
	shown := map[string]bool{}
	for _, l := range lanes {
		shown[providerName(l.Provider)] = true
	}
	var names []string
	for p, n := range limits {
		if n > 0 && (len(shown) == 0 || shown[p]) {
			names = append(names, p)
		}
	}
	slices.Sort(names)
	var parts []string
	for _, p := range names {
		parts = append(parts, fmt.Sprintf("%s %d", p, limits[p]))
	}
	if total > 0 {
		parts = append(parts, fmt.Sprintf("total %d", total))
	}
	if len(parts) == 0 {
		return ""
	}
	return "limit " + strings.Join(parts, " · ")
}

// flowTasks is the Tasks pane, in task ID order.
func flowTasks(in flowIn) []vm.Task {
	if !in.hasRun {
		return nil
	}
	b := in.v.board
	deps := map[string][]string{}
	for _, t := range in.run.Tasks {
		deps[t.PlanTaskID] = t.DependsOn
	}
	state := map[string]boardmodel.TaskState{}
	for _, t := range b.Tasks {
		state[t.ID] = t.State
	}
	reviewers := taskReviewers(b.Events)
	rows := slices.Clone(b.Tasks)
	slices.SortStableFunc(rows, func(x, y boardmodel.TaskRow) int { return cmp.Compare(x.ID, y.ID) })
	out := make([]vm.Task, 0, len(rows))
	for _, t := range rows {
		row := vm.Task{ID: t.ID, Title: firstLine(t.Title), Files: filesCell(t.Scope), Writer: providerName(t.Worker)}
		if t.Worker == "" {
			row.Writer = ""
		}
		row.Reviewer = reviewers[t.ID]
		switch t.State {
		case boardmodel.TaskMerged:
			row.State = "merged"
		case boardmodel.TaskReview:
			row.State = "review"
		case boardmodel.TaskAccepted:
			row.State, row.Note = "review", "merging"
		case boardmodel.TaskWorking:
			row.State = "building"
		case boardmodel.TaskRework:
			row.State, row.Note = "building", fmt.Sprintf("rework %d", max(1, t.Returns))
		case boardmodel.TaskParked:
			row.State, row.Note = "waiting", "stuck: needs you"
		case boardmodel.TaskCancelled:
			row.State = "cancelled"
		default:
			row.State, row.Note = "waiting", waitsOn(deps[t.ID], state)
		}
		if (row.State == "review" || row.State == "merged") && row.Reviewer == "" {
			row.Reviewer = "marshal"
		}
		out = append(out, row)
	}
	return out
}

// waitsOn is a queued task's note: the tasks it still waits on, "queued"
// when it waits on none.
func waitsOn(deps []string, state map[string]boardmodel.TaskState) string {
	var open []string
	for _, d := range deps {
		if s, ok := state[d]; ok && s != boardmodel.TaskMerged && s != boardmodel.TaskCancelled {
			open = append(open, d)
		}
	}
	switch len(open) {
	case 0:
		return "queued"
	case 1:
		return "waits " + open[0]
	}
	return fmt.Sprintf("waits %s +%d", open[0], len(open)-1)
}

// filesCell is a task's files in one short cell: "review.go +2".
func filesCell(files []string) string {
	if len(files) == 0 {
		return ""
	}
	cell := filepath.Base(files[0])
	if len(files) > 1 {
		cell += fmt.Sprintf(" +%d", len(files)-1)
	}
	return cell
}

// taskReviewers is each task's reviewer from its latest review: the
// cross-review provider the acceptance or return recorded.
func taskReviewers(evs []events.Event) map[string]string {
	out := map[string]string{}
	for _, e := range evs {
		if e.TaskID == "" {
			continue
		}
		switch e.Type {
		case events.EventTypeMarshalTaskAccepted, events.EventTypeMarshalTaskReturned:
			if p, _ := e.Data["cross_review_provider"].(string); strings.TrimSpace(p) != "" {
				out[e.TaskID] = providerName(p)
			}
		}
	}
	return out
}

// flowActivity is the Activity pane: the run's state changes and the
// background notes, newest first, equal events within foldWindow folded.
func flowActivity(in flowIn) []vm.Event {
	var evs []vm.Event
	for _, l := range in.v.board.Activity {
		if e, ok := activityEvent(l); ok {
			evs = append(evs, e)
		}
	}
	for _, n := range in.notes {
		kind, tone := noteKind(n)
		evs = append(evs, vm.Event{At: n.First, Last: n.Last, Kind: kind, Text: noteSummary(n.Text), Tone: tone, Count: n.Count})
	}
	return foldEvents(evs, foldWindow, 32)
}

// activityEvent is one run state change as an Activity row.
func activityEvent(l boardmodel.ActivityLine) (vm.Event, bool) {
	e := vm.Event{At: l.At, Last: l.At, Count: 1}
	subject := firstSet(l.TaskID, l.Subject)
	switch l.Type {
	case events.EventTypeMarshalTaskMerged:
		e.Kind, e.Tone, e.Text = "merge", vm.ToneOK, subject+" merged"
	case events.EventTypeMarshalRunClosed:
		e.Kind, e.Tone, e.Text = "merge", vm.ToneOK, "run closed"
	case events.EventTypeMarshalTaskAccepted:
		e.Kind, e.Tone, e.Text = "review", vm.ToneVio, subject+" accepted"
	case events.EventTypeMarshalTaskHandedIn:
		e.Kind, e.Tone, e.Text = "review", vm.ToneVio, subject+" handed in for review"
	case events.EventTypeMarshalTaskReturned:
		e.Kind, e.Tone, e.Text = "review", vm.ToneWarn, subject+" returned"
	case events.EventTypeMarshalTaskReassigned:
		e.Kind, e.Tone, e.Text = "review", vm.ToneWarn, subject+" moved to another worker"
	case events.EventTypeMarshalTaskDispatched:
		e.Kind, e.Tone, e.Text = "plan", vm.ToneInfo, subject+" started"
	case events.EventTypeMarshalEscalated:
		e.Kind, e.Tone, e.Text = "error", vm.ToneBad, subject+" stuck"
	case events.EventTypeMarshalTaskCancelled:
		e.Kind, e.Tone, e.Text = "note", vm.ToneMuted, subject+" cancelled"
	case events.EventTypeMarshalPlanApproved:
		e.Kind, e.Tone, e.Text = "plan", vm.ToneInfo, "plan approved"
	case events.EventTypeMarshalPlanAmended:
		e.Kind, e.Tone, e.Text = "plan", vm.ToneInfo, "plan amended"
	default:
		return vm.Event{}, false
	}
	if d := firstLine(l.Detail); d != "" {
		e.Text += " · " + d
	}
	e.Text = Truncate(e.Text, 80)
	return e, true
}

// noteKind sorts a background note into an Activity kind.
func noteKind(n foldedNote) (string, vm.Tone) {
	if n.Err {
		return "error", vm.ToneBad
	}
	low := strings.ToLower(n.Text)
	switch {
	case strings.Contains(low, "memory"):
		return "memory", vm.ToneMuted
	case strings.Contains(low, "architecture"):
		return "arch", vm.ToneInfo
	case strings.Contains(low, "heartbeat"), strings.Contains(low, "no worker"), strings.Contains(low, "limit"):
		return "signal", vm.ToneWarn
	case strings.Contains(low, "plan"):
		return "plan", vm.ToneInfo
	}
	return "note", vm.ToneNone
}

// foldEvents folds equal events (same kind and text) whose times lie within
// window of each other into one row with Count and Last, and returns at
// most limit rows, the most recent first.
func foldEvents(evs []vm.Event, window time.Duration, limit int) []vm.Event {
	for i := range evs {
		if evs[i].Count < 1 {
			evs[i].Count = 1
		}
		if evs[i].Last.IsZero() {
			evs[i].Last = evs[i].At
		}
	}
	slices.SortStableFunc(evs, func(x, y vm.Event) int { return x.At.Compare(y.At) })
	var out []vm.Event
	open := map[string]int{}
	for _, e := range evs {
		key := e.Kind + "\x00" + e.Text
		if i, ok := open[key]; ok && e.At.Sub(out[i].Last) <= window {
			out[i].Count += e.Count
			if e.Last.After(out[i].Last) {
				out[i].Last = e.Last
			}
			continue
		}
		open[key] = len(out)
		out = append(out, e)
	}
	slices.SortStableFunc(out, func(x, y vm.Event) int { return y.Last.Compare(x.Last) })
	return out[:min(len(out), limit)]
}

// countOf is "1 task", "3 tasks".
func countOf(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// joinDot joins the non-empty parts with " · ".
func joinDot(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}
