//go:build linux

package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

var flowNow = time.Date(2026, 11, 20, 19, 31, 0, 0, time.UTC)

// flowRun is a run of n tasks; the first merged of them are merged, the
// next working are dispatched, the rest queued.
func flowRun(state marshal.RunState, n, merged, working int) marshal.Run {
	run := marshal.Run{State: state, PlanID: "PLAN-1", PlanVersion: 1, Settings: marshal.Settings{ReworkLimit: 2}}
	for i := range n {
		t := marshal.Task{PlanTaskID: taskID(i + 1), Title: "task " + taskID(i+1), Worker: "claude", State: marshal.Queued, Files: []string{"web/review.go", "web/codec.go"}}
		switch {
		case i < merged:
			t.State = marshal.Merged
		case i < merged+working:
			t.State = marshal.Dispatched
		}
		run.Tasks = append(run.Tasks, t)
	}
	return run
}

func taskID(i int) string {
	return "T" + string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// flowWith is the flow input for run projected with evs and workers.
func flowWith(run marshal.Run, evs []events.Event, workers []boardmodel.Worker) flowIn {
	b := boardmodel.Project(boardmodel.Snapshot{RunID: "R-1", Run: run, Workers: workers, Now: flowNow}, evs)
	p := &MarshalPanel{RunID: "R-1", Provider: "claude", State: run.State, Board: b, run: run}
	return flowIn{now: flowNow, hasRun: true, panel: p, run: run, v: boardVisual{board: b, now: flowNow}}
}

// U3: two background messages repeated every 30 s for nine minutes are two
// Activity rows with their counts, not 36 rows that push everything else
// out; the same message after a quiet gap longer than the window is a new row.
func TestFlowActivityFoldsRepeatedNotes(t *testing.T) {
	var n boardNotes
	start := flowNow.Add(-30 * time.Minute)
	queued := "Marshal decision queued: chat input is not empty or ready."
	memory := "The Marshal did not review a memory imported from an earlier chat."
	at := start
	for range 18 {
		n.fold(queued, false, at)
		n.fold(memory, false, at.Add(time.Second))
		at = at.Add(30 * time.Second)
	}
	n.fold("Owner instruction recorded.", false, at)
	n.fold(queued, false, at.Add(11*time.Minute))
	in := flowIn{now: flowNow, notes: n.folded}
	got := flowActivity(in)
	if len(got) != 4 {
		t.Fatalf("activity = %+v, want 4 rows", got)
	}
	if got[0].Text != noteSummary(queued) || got[0].Count != 1 {
		t.Fatalf("newest row = %+v, want the queued note again after the gap, count 1", got[0])
	}
	var folded int
	for _, e := range got {
		if e.Count == 18 {
			folded++
			if !e.Last.After(e.At) {
				t.Fatalf("folded row %+v has no later Last", e)
			}
		}
	}
	if folded != 2 {
		t.Fatalf("activity = %+v, want both repeated notes folded ×18", got)
	}
	if got[1].Kind != "note" || got[len(got)-1].Kind != "memory" && got[len(got)-2].Kind != "memory" {
		t.Fatalf("kinds = %+v, want note and memory rows", got)
	}
}

// addNote feeds the fold even for a message equal to the newest, which the
// classic list drops.
func TestAddNoteCountsEveryRepeat(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	for range 3 {
		w.addNote("Marshal decision queued.", false)
	}
	f := w.foldedNotes()
	if len(f) != 1 || f[0].Count != 3 {
		t.Fatalf("folded = %+v, want one note ×3", f)
	}
	if len(w.noteList()) != 1 {
		t.Fatalf("classic notes = %+v, want one", w.noteList())
	}
}

// Run events fold too: the same kind and text within ten minutes is one row.
func TestFoldEventsWindow(t *testing.T) {
	evs := []vm.Event{
		{At: flowNow.Add(-20 * time.Minute), Kind: "signal", Text: "agy-4 slow heartbeat"},
		{At: flowNow.Add(-15 * time.Minute), Kind: "signal", Text: "agy-4 slow heartbeat"},
		{At: flowNow.Add(-6 * time.Minute), Kind: "signal", Text: "agy-4 slow heartbeat"},
		{At: flowNow.Add(-5 * time.Minute), Kind: "merge", Text: "T08 merged"},
		{At: flowNow.Add(-7 * time.Minute), Kind: "review", Text: "agy-4 slow heartbeat"},
	}
	got := foldEvents(evs, foldWindow, 32)
	if len(got) != 3 {
		t.Fatalf("rows = %+v, want 3", got)
	}
	if got[0].Kind != "merge" || got[1].Kind != "signal" || got[1].Count != 3 || !got[1].Last.Equal(flowNow.Add(-6*time.Minute)) {
		t.Fatalf("rows = %+v, want merge then signal ×3 last at -6m", got)
	}
}

func TestFlowActivityFromRunEvents(t *testing.T) {
	run := flowRun(marshal.Dispatching, 2, 1, 1)
	evs := []events.Event{
		{Sequence: 1, RunID: "R-1", Type: events.EventTypeMarshalPlanApproved, At: flowNow.Add(-time.Hour), Data: map[string]any{"plan_version": int64(1)}},
		{Sequence: 2, RunID: "R-1", TaskID: "T01", Type: events.EventTypeMarshalTaskMerged, At: flowNow.Add(-time.Minute)},
	}
	got := flowActivity(flowWith(run, evs, nil))
	if len(got) != 2 || got[0].Kind != "merge" || got[0].Text != "T01 merged" || got[1].Kind != "plan" || !strings.HasPrefix(got[1].Text, "plan approved") {
		t.Fatalf("activity = %+v", got)
	}
}

// FromYou is never empty: "nothing" with the next ask when nothing waits,
// otherwise the first thing that waits and how many more.
func TestFlowFromYouDerivation(t *testing.T) {
	t.Run("no run, no goal", func(t *testing.T) {
		got, ok := fromYou(flowIn{now: flowNow})
		if got != "nothing" || !ok {
			t.Fatalf("from you = %q %v", got, ok)
		}
	})
	t.Run("running, close not authorized", func(t *testing.T) {
		in := flowWith(flowRun(marshal.Dispatching, 3, 1, 1), nil, nil)
		got, ok := fromYou(in)
		if got != "nothing · next ask: final review before merge" || !ok {
			t.Fatalf("from you = %q %v", got, ok)
		}
	})
	t.Run("owner accepts tasks", func(t *testing.T) {
		run := flowRun(marshal.Dispatching, 3, 1, 1)
		run.Settings.AcceptanceMode = marshal.AcceptUser
		if got := nextAsk(flowWith(run, nil, nil)); got != "task acceptance" {
			t.Fatalf("next ask = %q", got)
		}
	})
	t.Run("inbox decision and blocked chat", func(t *testing.T) {
		in := flowWith(flowRun(marshal.Dispatching, 3, 1, 1), nil, nil)
		in.v.board.Inbox = []boardmodel.InboxItem{boardmodel.NewInboxItem(permission.Request{Kind: "network", Object: "api.github.com", TaskID: "T02"})}
		in.chatBlocked = 1
		got, ok := fromYou(in)
		if got != "T02: allow network access to api.github.com · +1 more" || ok {
			t.Fatalf("from you = %q %v", got, ok)
		}
		ib := flowInbox(in)
		if ib.Count != 1 || len(ib.Lines) != 1 || ib.Next != "final review before merge" {
			t.Fatalf("inbox = %+v, want the one decision; the blocked chat is not an inbox item", ib)
		}
	})
	t.Run("blocked chat alone", func(t *testing.T) {
		in := flowWith(flowRun(marshal.Dispatching, 3, 1, 1), nil, nil)
		in.chatBlocked = 2
		if got, ok := fromYou(in); got != "send or clear your unsent text in the Marshal chat" || ok {
			t.Fatalf("from you = %q %v", got, ok)
		}
	})
	t.Run("paused run", func(t *testing.T) {
		run := flowRun(marshal.AwaitingUser, 3, 1, 0)
		run.Pause = &marshal.Pause{Reason: "T02 needs a decision", Resolutions: []string{"/marshal resume"}, Action: marshal.PauseResume, ResumeState: marshal.Dispatching}
		in := flowWith(run, nil, nil)
		got, ok := fromYou(in)
		if got != "run paused: T02 needs a decision" || ok {
			t.Fatalf("from you = %q %v", got, ok)
		}
		if s, tone := flowState(in); s != "YOUR TURN" || tone != vm.ToneWarn {
			t.Fatalf("state = %s %s", s, tone)
		}
	})
	t.Run("user paused", func(t *testing.T) {
		in := flowWith(flowRun(marshal.Dispatching, 3, 1, 1), nil, nil)
		in.v.board.Run.UserPaused = true
		if got, ok := fromYou(in); got != "you paused the run: resume when ready" || ok {
			t.Fatalf("from you = %q %v", got, ok)
		}
	})
	t.Run("planning, owner's turn", func(t *testing.T) {
		in := flowIn{now: flowNow, goalGiven: true, milestone: &planningMark{PlanningMilestone: app.PlanningMilestone{Step: stageArch, Now: "accept the architecture in the browser", OwnerTurn: true}}}
		if got, ok := fromYou(in); got != "accept the architecture in the browser" || ok {
			t.Fatalf("from you = %q %v", got, ok)
		}
		if s, _ := flowState(in); s != "YOUR TURN" {
			t.Fatalf("state = %s", s)
		}
	})
	t.Run("planning, Marshal's turn", func(t *testing.T) {
		in := flowIn{now: flowNow, goalGiven: true, milestone: &planningMark{PlanningMilestone: app.PlanningMilestone{Step: stageTasks, Now: "planning the tasks"}}}
		if got, ok := fromYou(in); got != "nothing · next ask: plan approval" || !ok {
			t.Fatalf("from you = %q %v", got, ok)
		}
	})
	t.Run("architecture accepted by the owner, not applied yet", func(t *testing.T) {
		in := flowIn{now: flowNow, goalGiven: true, milestone: &planningMark{PlanningMilestone: app.PlanningMilestone{Step: stageArch, Now: "applying the architecture you accepted", Applying: true}}}
		if got, ok := fromYou(in); got != "nothing · next ask: plan approval" || !ok {
			t.Fatalf("from you = %q %v", got, ok)
		}
		if st := stageStates(flowStages(in, taskCounts{})); !strings.Contains(st, "*arch") {
			t.Fatalf("stages = %s", st)
		}
	})
}

func stageStates(st []vm.Stage) string {
	var b strings.Builder
	for _, s := range st {
		mark := map[string]string{"done": "+", "current": "*", "todo": "."}[s.State]
		b.WriteString(mark + s.Name)
		if s.Note != "" {
			b.WriteString("(" + s.Note + ")")
		}
		b.WriteString(" ")
	}
	return strings.TrimSpace(b.String())
}

// The Plan pipeline follows the planning milestone before a run exists and
// the run's state after.
func TestFlowStageMapping(t *testing.T) {
	since := flowNow.Add(-4 * time.Minute)
	cases := []struct {
		name string
		in   flowIn
		want string
	}{
		{"design milestone", flowIn{now: flowNow, goalGiven: true, milestone: &planningMark{PlanningMilestone: app.PlanningMilestone{Step: 2, Now: "drafting the design"}, Since: since}},
			"+goal +reqs *design(4m) .arch .tasks .build .review .merge"},
		{"planning after accept", flowIn{now: flowNow, planning: true, archRev: 1, archAccepted: true},
			"+goal +reqs +design +arch(rev 1) *tasks .build .review .merge"},
		{"drafted run", flowWith(flowRun(marshal.Drafting, 4, 0, 0), nil, nil),
			"+goal +reqs +design +arch *tasks .build .review .merge"},
		{"building", flowWith(flowRun(marshal.Dispatching, 12, 5, 4), nil, nil),
			"+goal +reqs +design +arch +tasks *build(5/12) .review .merge"},
		{"all in review", flowWith(func() marshal.Run {
			r := flowRun(marshal.Dispatching, 3, 1, 0)
			r.Tasks[1].State, r.Tasks[2].State = marshal.HandedIn, marshal.HandedIn
			return r
		}(), nil, nil), "+goal +reqs +design +arch +tasks +build(1/3) *review(2 in review) .merge"},
		{"verifying", flowWith(flowRun(marshal.Verifying, 2, 2, 0), nil, nil),
			"+goal +reqs +design +arch +tasks +build(2/2) +review *merge(final check)"},
		{"closed", flowWith(flowRun(marshal.Closed, 2, 2, 0), nil, nil),
			"+goal +reqs +design +arch +tasks +build(2/2) +review +merge"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stageStates(flowPlan(c.in).Stages); got != c.want {
				t.Fatalf("stages\n got %s\nwant %s", got, c.want)
			}
		})
	}
	if p := flowPlan(flowIn{now: flowNow}); p.Stages != nil {
		t.Fatalf("no goal, no run: plan = %+v, want empty", p)
	}
}

// Counts, ETA, throughput and rework come from the run's tasks and events.
func TestFlowPlanCountsAndRates(t *testing.T) {
	run := flowRun(marshal.Dispatching, 6, 2, 2)
	run.Tasks[4].State = marshal.Returned
	run.Tasks[4].ReturnsByAgent = map[string]int{"claude": 1}
	evs := []events.Event{
		{Sequence: 1, RunID: "R-1", Type: events.EventTypeMarshalPlanApproved, At: flowNow.Add(-2 * time.Hour)},
		{Sequence: 2, RunID: "R-1", TaskID: "T01", Type: events.EventTypeMarshalTaskDispatched, At: flowNow.Add(-50 * time.Minute)},
		{Sequence: 3, RunID: "R-1", TaskID: "T02", Type: events.EventTypeMarshalTaskDispatched, At: flowNow.Add(-50 * time.Minute)},
		{Sequence: 4, RunID: "R-1", TaskID: "T01", Type: events.EventTypeMarshalTaskMerged, At: flowNow.Add(-30 * time.Minute)},
		{Sequence: 5, RunID: "R-1", TaskID: "T02", Type: events.EventTypeMarshalTaskMerged, At: flowNow.Add(-2 * time.Minute)},
		{Sequence: 6, RunID: "R-1", TaskID: "T02", Type: events.EventTypeMarshalUsageCharged, At: flowNow.Add(-2 * time.Minute), Data: map[string]any{"usage_known": true, "usage_units": int64(1900000)}},
	}
	workers := []boardmodel.Worker{
		{ID: "claude-1", Provider: "claude", TaskID: "T03", Heartbeat: true, Started: flowNow.Add(-10 * time.Minute), Beat: flowNow},
		{ID: "claude-2", Provider: "claude", TaskID: "T04", Heartbeat: true, Started: flowNow.Add(-10 * time.Minute), Beat: flowNow},
	}
	p := flowPlan(flowWith(run, evs, workers))
	if p.Done != 2 || p.Active != 2 || p.Waiting != 2 || p.Total != 6 {
		t.Fatalf("counts = %d/%d/%d/%d", p.Done, p.Active, p.Waiting, p.Total)
	}
	// T01 took 20m, T02 48m: 34m on average, 4 tasks left on 2 lanes.
	if want := 34 * time.Minute * 4 / 2; p.ETA != want {
		t.Fatalf("eta = %s, want %s", p.ETA, want)
	}
	if len(p.Throughput) != 12 || p.Throughput[11] != 1 || p.Throughput[5] != 1 || p.MergedPerHr != 2 {
		t.Fatalf("throughput = %v, %d/h", p.Throughput, p.MergedPerHr)
	}
	if p.Reworked != 1 || p.Tokens != 1900000 || p.Title != "6 tasks" || !strings.HasPrefix(p.Accepted, "accepted rev 1 · ") {
		t.Fatalf("plan = %+v", p)
	}
	if p := flowPlan(flowWith(flowRun(marshal.Dispatching, 3, 0, 1), nil, nil)); p.ETA != 0 {
		t.Fatalf("eta with nothing merged = %s, want 0 (unknown)", p.ETA)
	}
}

// Lanes follow the heartbeats: a beat older than 60 s is stale, its age
// and the lane's elapsed time are kept, the selected lane is marked.
func TestFlowLanesStaleness(t *testing.T) {
	run := flowRun(marshal.Dispatching, 3, 0, 2)
	run.Tasks[0].WorkerSelection = &marshal.WorkerSelection{SelectedModel: "sonnet-5.5"}
	workers := []boardmodel.Worker{
		{ID: "claude-1", Provider: "claude", TaskID: "T01", Heartbeat: true, Started: flowNow.Add(-14 * time.Minute), Beat: flowNow.Add(-8 * time.Second)},
		{ID: "claude-2", Provider: "claude", TaskID: "T02", Heartbeat: true, Started: flowNow.Add(-11 * time.Minute), Beat: flowNow.Add(-100 * time.Second)},
	}
	in := flowWith(run, nil, workers)
	in.selectedLane = "claude-2"
	in.limits, in.globalCap = map[string]int{"claude": 4, "agy": 4, "codex": 4}, 12
	got := flowLanes(in)
	if len(got.Items) != 2 {
		t.Fatalf("lanes = %+v", got.Items)
	}
	a, b := got.Items[0], got.Items[1]
	if a.Name != "claude-1" || a.Model != "sonnet-5.5" || a.Task != "T01 task T01" || a.Step != "working" || a.Elapsed != 14*time.Minute || a.LastSignal != 8*time.Second || a.Stale || a.Selected {
		t.Fatalf("lane 1 = %+v", a)
	}
	if b.LastSignal != 100*time.Second || !b.Stale || !b.Selected {
		t.Fatalf("lane 2 = %+v, want stale and selected", b)
	}
	if got.Limits != "limit claude 4 · total 12" {
		t.Fatalf("limits = %q", got.Limits)
	}
	if s := limitsText(map[string]int{"claude": 2, "agy": 4}, 12, nil); s != "limit agy 4 · claude 2 · total 12" {
		t.Fatalf("limits without lanes = %q", s)
	}
}

// Task rows: files in one cell, writer and reviewer by provider, what a
// queued task waits on.
func TestFlowTasks(t *testing.T) {
	run := flowRun(marshal.Dispatching, 3, 1, 1)
	run.Tasks[2].DependsOn = []string{"T01", "T02"}
	evs := []events.Event{{Sequence: 1, RunID: "R-1", TaskID: "T01", Type: events.EventTypeMarshalTaskAccepted, At: flowNow, Data: map[string]any{"cross_review_provider": "antigravity"}}}
	got := flowTasks(flowWith(run, evs, nil))
	if len(got) != 3 {
		t.Fatalf("tasks = %+v", got)
	}
	want := []vm.Task{
		{ID: "T01", Title: "task T01", Files: "review.go +1", Writer: "claude", Reviewer: "agy", State: "merged"},
		{ID: "T02", Title: "task T02", Files: "review.go +1", Writer: "claude", State: "building"},
		{ID: "T03", Title: "task T03", Files: "review.go +1", Writer: "claude", State: "waiting", Note: "waits T02"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("task %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The broker's tool calls become steps: a new lease starts a session, a
// repeat folds, receipts move only the quiet clock, prompt text never
// shows.
func TestStepsFromBrokerCalls(t *testing.T) {
	var s marshalSteps
	t0 := flowNow.Add(-10 * time.Minute)
	s.note("L1", "claude-code", "project_contract_context", json.RawMessage(`{}`), t0)
	s.note("L1", "claude-code", "project_contract_context", json.RawMessage(`{"read_id":"r","page":2}`), t0.Add(time.Minute))
	s.note("L1", "claude-code", "project_contract_submit", json.RawMessage(`{"submission_id":"s","proposal":{"secret":"prompt text"}}`), t0.Add(2*time.Minute))
	s.note("L1", "claude-code", "submit", json.RawMessage(`{"submission_id":"s2","proposal":{"action":"reassign","task_id":"T03","reason":"do not show this"}}`), t0.Add(3*time.Minute))
	s.note("L1", "claude-code", "receipt", json.RawMessage(`{"submission_id":"s2"}`), t0.Add(4*time.Minute))
	items, last, provider := s.snapshot()
	got := stepsView(items, last, marshalModelLabel(provider, "opus-5.5"), flowNow)
	var lines []string
	for _, st := range got.Items {
		lines = append(lines, st.Kind+" "+st.Text)
	}
	want := []string{"assign move a task to another worker T03", "draft architecture submitted for your review", "read project contract context ×2", "start session on claude"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("steps\n got %q\nwant %q", lines, want)
	}
	if got.Quiet != 6*time.Minute || got.Model != "claude opus-5.5" {
		t.Fatalf("quiet %s model %q", got.Quiet, got.Model)
	}
	for _, l := range lines {
		if strings.Contains(l, "prompt") || strings.Contains(l, "do not show") {
			t.Fatalf("step %q carries submitted text", l)
		}
	}
	s.note("L2", "codex", "memory_receipt", nil, flowNow)
	items, _, _ = s.snapshot()
	if items[0].Kind != "start" || items[0].Text != "session on codex" {
		t.Fatalf("new lease: %+v", items[0])
	}
	if v := stepsView(nil, time.Time{}, "", flowNow); v.Quiet != 0 || v.Items != nil {
		t.Fatalf("no calls: %+v", v)
	}
}

// The whole collector on a governed worker's run: the top bar, NOW row,
// the lane from its heartbeat and the task row agree.
func TestCollectV2FlowOnGovernedRun(t *testing.T) {
	beat := time.Now().Add(-90 * time.Second)
	w, _, _ := governedWorkspace(t, marshal.Dispatched, func() []app.WorkerBeat {
		return []app.WorkerBeat{{RunID: "R-80", TaskID: "browser-fixes", Worker: "codex", Started: time.Now().Add(-5 * time.Minute), Beat: beat}}
	})
	b := w.boardV2()
	if b.Top.Run != "R-80" || b.Top.State != "RUNNING" || b.Top.Workers != 1 || b.Top.Clock.IsZero() {
		t.Fatalf("top = %+v", b.Top)
	}
	if !strings.HasPrefix(b.NowLine.Marshal, "codex") || b.NowLine.Work != "1 running" || b.NowLine.FromYou == "" || !b.NowLine.FromYouOK {
		t.Fatalf("now = %+v", b.NowLine)
	}
	if len(b.Lanes.Items) != 1 || b.Lanes.Items[0].Name != "codex-1" || !b.Lanes.Items[0].Stale {
		t.Fatalf("lanes = %+v, want codex-1 stale after 90s", b.Lanes.Items)
	}
	if len(b.Tasks) != 1 || b.Tasks[0].ID != "browser-fixes" || b.Tasks[0].State != "building" || b.Tasks[0].Files != "app.js" {
		t.Fatalf("tasks = %+v", b.Tasks)
	}
	if b.Plan.Total != 1 || stageStates(b.Plan.Stages) != "+goal +reqs +design +arch +tasks *build(0/1) .review .merge" {
		t.Fatalf("plan = %+v", b.Plan)
	}
}

// The NOW line names the Marshal as the top bar does when it runs in a tmux
// chat window: "claude Opus 5.5 · ...", never the placeholder "Marshal"
// (w.chat, the in-process chat, is empty then).
func TestCollectV2FlowNowNamesTmuxMarshal(t *testing.T) {
	w := emptyBoardWorkspace(t)
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{role: "marshal-chat", provider: "claude", state: "working", args: []string{"--model", "claude-opus-5-5", "--permission-mode", "dontAsk"}}
	b := w.boardV2()
	if b.Marshal.Provider != "claude" || b.Marshal.Model != "Opus 5.5" {
		t.Fatalf("top bar Marshal = %+v", b.Marshal)
	}
	if !strings.HasPrefix(b.NowLine.Marshal, "claude Opus 5.5") {
		t.Fatalf("NOW Marshal = %q, want it to start with %q", b.NowLine.Marshal, "claude Opus 5.5")
	}
	if strings.Contains(b.NowLine.Marshal, "Marshal") {
		t.Fatalf("NOW still names the provider %q", b.NowLine.Marshal)
	}
}
