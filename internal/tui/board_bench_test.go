package tui

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
)

// benchRun is a run of n tasks across every state and five providers.
func benchRun(n int) marshal.Run {
	states := []marshal.TaskState{marshal.Merged, marshal.Accepted, marshal.HandedIn, marshal.Dispatched, marshal.Dispatched, marshal.Escalated, marshal.Queued}
	workers := []string{"codex", "claude", "agy", "opencode", "codex"}
	run := marshal.Run{State: marshal.Dispatching, PlanID: "PLAN-bench", Settings: marshal.Settings{ReworkLimit: 2}}
	for i := range n {
		t := marshal.Task{PlanTaskID: fmt.Sprintf("T-%03d", i+1), Title: fmt.Sprintf("task %d", i+1), Worker: workers[i%len(workers)], State: states[i%len(states)], Files: []string{fmt.Sprintf("internal/pkg%02d/file_%03d.go", i%13, i)}}
		if i > 0 {
			t.DependsOn = []string{fmt.Sprintf("T-%03d", i)}
		}
		run.Tasks = append(run.Tasks, t)
	}
	return run
}

// benchWorkspace is a Board workspace on a run of tasks tasks with
// decisions decisions, workers on its dispatched tasks, two requests in the
// inbox, the host watcher sampled, and every view's source faked and read
// once, as an open view would have read it.
func benchWorkspace(tb testing.TB, tasks, decisions int) (*Workspace, *fakeDecisions) {
	tb.Helper()
	w := NewWorkspace(nil, "project", "session")
	tb.Cleanup(w.Close)
	w.screen = NewScreen(nil)
	run := benchRun(tasks)
	start := time.Now().Add(-time.Hour)
	src := &fakeDecisions{}
	src.add(events.Event{RunID: "R-bench", Type: events.EventTypeMarshalPlanApproved, At: start})
	for i := range decisions {
		task := run.Tasks[i%len(run.Tasks)]
		at := start.Add(time.Duration(i+1) * time.Second)
		e := events.Event{RunID: "R-bench", TaskID: task.PlanTaskID, At: at}
		switch i % 4 {
		case 0:
			e.Type, e.Data = events.EventTypeMarshalTaskDispatched, map[string]any{"worker": task.Worker}
		case 1:
			e.Type, e.Data = events.EventTypeMarshalUsageCharged, map[string]any{"charge_phase": "worker", "usage_known": true, "usage_units": float64(500)}
		case 2:
			e.Type, e.Data = events.EventTypeMarshalTaskHandedIn, map[string]any{"reason": "hand-in with evidence"}
		default:
			e.Type, e.Data = events.EventTypeMarshalEscalated, map[string]any{"reason": "egress outside manifest"}
		}
		src.add(e)
	}
	w.tmuxMu.Lock()
	for i, t := range run.Tasks {
		if t.State == marshal.Dispatched {
			id := "task-" + t.PlanTaskID
			w.tmuxActiveWins[id] = &activeTmuxAgent{id: id, role: "task", provider: t.Worker, taskID: t.PlanTaskID, runID: "R-bench", paneID: fmt.Sprintf("%%%d", i+3), state: "working"}
		}
	}
	w.tmuxMu.Unlock()
	w.permissions.inbox = []permission.Request{
		{Kind: "network", TaskID: "T-006", Object: "registry.npmjs.org:443", Scope: "one run", Reason: "outside manifest", Who: "Marshal"},
		{Kind: "read", TaskID: "T-013", Object: "/srv/vendor", Scope: "this project", Reason: "vendored module", Who: "Marshal"},
	}
	p := newMarshalPanel("R-bench", "codex", run, "running")
	evs, _, _, err := w.boardFeed.read(context.Background(), src, p.RunID)
	if err != nil {
		tb.Fatal(err)
	}
	p.Board = w.projectBoard(p, evs)
	w.setMarshalPanel(p)

	hw, _, clock := testWatcher(tb, nil)
	steps(hw, clock, 30)
	w.hostWatch.Store(hw)
	now := time.Now()
	w.memSource = &fakeMemory{overview: demoMemoryOverview(now)}
	w.provSource = &fakeProviderView{providers: demoProviders(now)}
	w.verSource = &fakeVerifyView{overview: demoVerifyOverview(now)}
	w.secSource = &fakeSecurityView{overview: demoSecurityOverview(now)}
	w.planSource = &fakePlanView{overview: demoPlanOverview(now)}
	w.archSource = &fakeArchView{overview: demoArchOverview()}
	ctx := context.Background()
	w.refreshMemory(ctx)
	w.refreshProviders(ctx)
	w.refreshVerify(ctx)
	w.refreshSecurity(ctx)
	w.refreshPlan(ctx)
	w.refreshArch(ctx)
	return w, src
}

// benchViews is each screen the Board paints, opened as its key opens it.
var benchViews = []struct {
	name string
	open func(w *Workspace)
}{
	{"Board", func(w *Workspace) {}},
	{"Inspector", func(w *Workspace) { w.boardUI.inspect = "T-004" }},
	{"Help", func(w *Workspace) { w.openBoardHelp() }},
	{"Host", func(w *Workspace) { w.closeBodyViews(); w.hostView.Store(true) }},
	{"Memory", func(w *Workspace) { w.openMemoryView() }},
	{"Providers", func(w *Workspace) { w.openProviderView() }},
	{"Verify", func(w *Workspace) { w.openVerifyView() }},
	{"Security", func(w *Workspace) { w.openSecurityView() }},
	{"Plan", func(w *Workspace) { w.openPlanView() }},
	{"Architecture", func(w *Workspace) { w.openArchView() }},
}

// resetBenchView closes whatever benchViews opened.
func resetBenchView(w *Workspace) {
	w.closeBodyViews()
	w.boardUI.inspect = ""
	w.boardHelp.Store(false)
}

// BenchmarkBoardView is one frame of each screen at 120x40 for a run of 60
// tasks with a full activity log.
func BenchmarkBoardView(b *testing.B) {
	w, _ := benchWorkspace(b, 60, 400)
	if n := len(w.marshalPanel().Board.Tasks); n < 50 {
		b.Fatalf("%d tasks", n)
	}
	for _, v := range benchViews {
		for _, sz := range [][2]int{{120, 40}, {80, 24}} {
			b.Run(fmt.Sprintf("%s/%dx%d", v.name, sz[0], sz[1]), func(b *testing.B) {
				resetBenchView(w)
				v.open(w)
				b.ReportAllocs()
				for b.Loop() {
					w.frameLines(sz[0], sz[1])
				}
			})
		}
	}
}

// Painting reads nothing: across many frames of every screen at both
// sizes, neither the Board's decisions nor any view's source is read.
func TestBoardViewReadsNoStorePerFrame(t *testing.T) {
	w, src := benchWorkspace(t, 60, 400)
	reads := countViewReads(w)
	src.mu.Lock()
	decisionReads := len(src.afters)
	src.mu.Unlock()
	for _, v := range benchViews {
		resetBenchView(w)
		v.open(w)
		for i := range 200 {
			if i%2 == 0 {
				w.frameLines(120, 40)
			} else {
				w.frameLines(80, 24)
			}
		}
		if n := reads.total(); n != 0 {
			t.Fatalf("frames of %s read a view's source: %s", v.name, reads)
		}
		src.mu.Lock()
		n := len(src.afters)
		src.mu.Unlock()
		if n != decisionReads {
			t.Fatalf("frames of %s read the run's decisions %d times", v.name, n-decisionReads)
		}
	}
}
