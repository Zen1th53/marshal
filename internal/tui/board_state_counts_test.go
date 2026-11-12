package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// The requirements card waits for the person: the header's inbox counts it,
// and the call to action no longer asks for a goal the Marshal already has.
func TestRequirementsCardIsCountedAndHidesTheCallToAction(t *testing.T) {
	w := reqBoard(t)
	defer w.Close()
	text := frameText(w, 120, 40)
	head := strings.SplitN(text, "\n", 3)[1]
	if !strings.Contains(head, "inbox 1") {
		t.Fatalf("the header does not count the requirements card: %q", head)
	}
	if strings.Contains(text, firstUseCTA) {
		t.Fatalf("the call to action still asks for a goal:\n%s", text)
	}
}

// Once a goal is given the first-use card stops asking for one; with none
// (or a cancelled one) it asks.
func TestFirstUseCallToActionHidesOnceAGoalIsGiven(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	if text := frameText(w, 120, 40); !strings.Contains(text, firstUseCTA) {
		t.Fatalf("a fresh project does not ask for a goal:\n%s", text)
	}
	w.mu.Lock()
	w.state.Goal = model.GoalContract{ID: "GOAL-1", Revision: 1, Confirmation: model.ConfirmationApproved}
	w.mu.Unlock()
	text := frameText(w, 120, 40)
	if strings.Contains(text, firstUseCTA) {
		t.Fatalf("the call to action stays after the goal was given:\n%s", text)
	}
	if !strings.Contains(text, "The Marshal has your goal") {
		t.Fatalf("the card does not say the Marshal has the goal:\n%s", text)
	}
	if strings.Contains(text, "GOAL-1") {
		t.Fatalf("the goal id is on the card:\n%s", text)
	}
	w.mu.Lock()
	w.state.Goal.Confirmation = model.ConfirmationCancelled
	w.mu.Unlock()
	if text := frameText(w, 120, 40); !strings.Contains(text, firstUseCTA) {
		t.Fatalf("a cancelled goal still hides the call to action:\n%s", text)
	}
}

// Every card the header counts is listed: a closed run with changed files,
// a Marshal question and the "what next?" card counts three and shows all
// three, beside the chat as at full width.
func TestInboxListsEveryEntryTheHeaderCounts(t *testing.T) {
	for _, docked := range []bool{true, false} {
		w := emptyBoardWorkspace(t)
		w.dock.docked.Store(docked)
		ap, err := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
		if err != nil {
			t.Fatal(err)
		}
		w.openAsk(ap)
		w.noteRunClosed(closedPanel("RUN-1", marshal.Verifying, 1, 1, 0, 0), closedPanel("RUN-1", marshal.Closed, 1, 1, 0, 0))
		w.guard.alert = &guardAlert{at: time.Now(), paths: []string{"x.py"}, wasDirty: map[string]bool{}}
		p := closedPanel("RUN-1", marshal.Closed, 1, 1, 0, 0)
		p.Board.Tasks = []boardmodel.TaskRow{{ID: "calc-add", State: boardmodel.TaskMerged, Worker: "codex"}}
		w.mu.Lock()
		w.state.Marshal = p
		w.mu.Unlock()
		for _, cols := range []int{80, 120} {
			text := frameText(w, cols, 40)
			head := strings.SplitN(text, "\n", 3)[1]
			if !strings.Contains(head, "⚑3") && !strings.Contains(head, "inbox 3") {
				t.Errorf("docked %v at %d: the header does not count three: %q", docked, cols, head)
			}
			for _, want := range []string{"[n] new goal", "question · ", "x.py"} {
				if !strings.Contains(text, want) {
					t.Errorf("docked %v at %d: the inbox does not list %q:\n%s", docked, cols, want, text)
				}
			}
		}
		w.Close()
	}
}

// While this process drives the run, a hand-in the Marshal is reviewing is
// in review, not stalled; once the run is no longer driven (the operation
// ended, the run stopped or waits for the person) the same row is stalled.
func TestReviewRowIsNotStalledWhileTheMarshalDrivesTheRun(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	run := marshal.Run{State: marshal.Reviewing, Tasks: []marshal.Task{{PlanTaskID: "calc-add", Worker: "codex", State: marshal.HandedIn}}}
	stalled := func() bool {
		p := newMarshalPanel("RUN-1", "codex", run, "running")
		b := w.projectBoard(p, nil)
		return b.Stalled(b.Tasks[0])
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.runID, m.busy = "RUN-1", true
	m.mu.Unlock()
	if stalled() {
		t.Fatal("a hand-in under review by the driven run is shown stalled")
	}
	run.State = marshal.AwaitingUser
	if !stalled() {
		t.Fatal("a hand-in of a run waiting for the person is not shown stalled")
	}
	run.State = marshal.Reviewing
	m.mu.Lock()
	m.busy = false
	m.mu.Unlock()
	if !stalled() {
		t.Fatal("a hand-in nothing drives is not shown stalled")
	}
}

// A merged task's elapsed cell keeps how long it took, however long ago it
// merged.
func TestMergedTaskRowElapsedDoesNotTick(t *testing.T) {
	at := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)
	b := boardmodel.Board{Tasks: []boardmodel.TaskRow{{ID: "calc-add", State: boardmodel.TaskMerged, Worker: "codex", Began: at, Since: at.Add(90 * time.Second)}}}
	for _, later := range []time.Duration{0, 2 * time.Minute, 7 * time.Minute} {
		v := rowsVisual(b, ThemeNoColor, true)
		v.now = at.Add(90*time.Second + later)
		line := StripANSI(taskLine(t, v.tasks(layout.Pane{ID: layout.Tasks, Rect: layout.Rect{W: 120, H: 4}}), "calc-add"))
		if !strings.Contains(line, "01:30") {
			t.Fatalf("%s after the merge the row reads %q, want 01:30", later, line)
		}
	}
}
