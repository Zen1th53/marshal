package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// demoStallPanel is the live Mac demo's run: four independent governed
// tasks, all on codex, queued.
func demoStallPanel(state marshal.RunState) *MarshalPanel {
	run := marshal.Run{State: state, PlanID: "PLAN-demo", Settings: marshal.DefaultSettings()}
	for _, id := range []string{"done-cmd", "due-dates", "color-table", "export-json"} {
		run.Tasks = append(run.Tasks, marshal.Task{PlanTaskID: id, Title: id, Worker: "codex", Mode: marshal.Governed, State: marshal.Queued, Files: []string{id + ".go"}})
	}
	return newMarshalPanel("RUN-1795320601100625000", "claude", run, "plan ready: 4 tasks")
}

// atBoardTime sets the Board's clock to now.
func atBoardTime(now time.Time) {
	boardNow = func() time.Time { return now }
}

// The live Mac demo: the plan was imported, the Marshal waited for the
// runtime to say so before approving, and the Board said PLANNING with
// "nothing · next ask: plan approval" for twenty minutes while four tasks
// sat queued. A plan nobody approves becomes the owner's to look at, with
// the reason, past the bound.
func TestUnapprovedImportedPlanBecomesOwnerAsk(t *testing.T) {
	if !editionExtreme {
		t.Skip("the owner approves the plan in this edition: its plan gate is already the owner's ask")
	}
	old := boardNow
	t.Cleanup(func() { boardNow = old })
	w := emptyBoardWorkspace(t)
	w.setMarshalPanel(demoStallPanel(marshal.Drafting))
	t0 := time.Date(2026, 11, 22, 4, 13, 22, 0, time.UTC)

	atBoardTime(t0)
	in := w.flowInputs()
	if state, _ := flowState(in); state != "PLANNING" {
		t.Fatalf("fresh import: state %q, want PLANNING", state)
	}
	if from, ok := fromYou(in); from != "nothing · next ask: plan approval" || !ok {
		t.Fatalf("fresh import: from you %q %v", from, ok)
	}

	atBoardTime(t0.Add(20 * time.Minute))
	in = w.flowInputs()
	if state, _ := flowState(in); state != "YOUR TURN" {
		t.Fatalf("plan unapproved for 20 minutes: state %q, want YOUR TURN", state)
	}
	if from, ok := fromYou(in); ok || from != "plan in, not approved by the Marshal for 20m: open the Marshal chat" {
		t.Fatalf("stalled plan not asked of the owner with its reason: %q %v", from, ok)
	}
}

// An approved plan whose open tasks all stay queued with no worker and
// nothing paused is asked of the owner too, never left as a silent
// "queued", also mid-run; a run with a worker running, or one the owner
// paused, is not.
func TestApprovedPlanWithNoWorkerBecomesOwnerAsk(t *testing.T) {
	old := boardNow
	t.Cleanup(func() { boardNow = old })
	w := emptyBoardWorkspace(t)
	w.setMarshalPanel(demoStallPanel(marshal.Dispatching))
	t0 := time.Date(2026, 11, 22, 4, 13, 22, 0, time.UTC)
	atBoardTime(t0)
	if state, _ := flowState(w.flowInputs()); state == "YOUR TURN" {
		t.Fatal("a just-approved plan was asked of the owner")
	}
	atBoardTime(t0.Add(20 * time.Minute))
	in := w.flowInputs()
	from, ok := fromYou(in)
	if state, _ := flowState(in); state != "YOUR TURN" || ok || from != "no worker on the queued tasks for 20m: open the Marshal chat" {
		t.Fatalf("stalled dispatch: state %q, from you %q %v", state, from, ok)
	}

	// Mid-run: one task merged, the rest queued with no worker.
	mid := demoStallPanel(marshal.Dispatching)
	mid.run.Tasks[0].State = marshal.Merged
	w.setMarshalPanel(newMarshalPanel(mid.RunID, "claude", mid.run, ""))
	atBoardTime(t0.Add(25 * time.Minute))
	w.flowInputs()
	atBoardTime(t0.Add(35 * time.Minute))
	if from, _ := fromYou(w.flowInputs()); !strings.HasPrefix(from, "no worker on the queued tasks") {
		t.Fatalf("stalled mid-run dispatch: from you %q", from)
	}

	// A worker on one task: the run moves.
	running := demoStallPanel(marshal.Dispatching)
	running.run.Tasks[0].State = marshal.Dispatched
	w.setMarshalPanel(newMarshalPanel(running.RunID, "claude", running.run, ""))
	atBoardTime(t0.Add(40 * time.Minute))
	w.flowInputs()
	atBoardTime(t0.Add(60 * time.Minute))
	if from, _ := fromYou(w.flowInputs()); strings.Contains(from, "open the Marshal chat") {
		t.Fatalf("a running run was reported stalled: %q", from)
	}

	// The owner paused it: the pause is the ask, not a stall.
	paused := demoStallPanel(marshal.Dispatching)
	paused.Board.Run.UserPaused = true
	w.setMarshalPanel(paused)
	atBoardTime(t0.Add(90 * time.Minute))
	w.flowInputs()
	atBoardTime(t0.Add(120 * time.Minute))
	if from, _ := fromYou(w.flowInputs()); from != "you paused the run: resume when ready" {
		t.Fatalf("paused run: from you %q", from)
	}
}
