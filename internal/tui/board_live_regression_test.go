package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/board/vm"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/webboard"
)

func TestLiveChatQuestionWinsArchitecture(t *testing.T) {
	in := flowIn{chatAsked: true, v: boardVisual{gates: []ownerGate{{kind: decisionArchitecture, title: "waits for you in the browser"}}}}
	n := flowNowLine(in)
	if !n.ChatAsked || n.FromYou != vm.ChatAskedLine {
		t.Fatalf("question hidden by architecture: %+v", n)
	}
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.gates.contract = &gateContract{ID: "architecture", Revision: 1}
	b := vm.Board{NowLine: vm.NowLine{ChatAsked: true, FromYou: vm.ChatAskedLine}}
	w.collectV2Cards(&b)
	if b.Decision != nil && b.Decision.Kind == decisionArchitecture {
		t.Fatal("architecture card hides chat question")
	}
	if !webboard.FromBoard(b).NowLine.ChatAsked {
		t.Fatal("web lost chat question")
	}
}

func TestLiveArchitectureRequestIsBeingAnswered(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.state.Goal = model.GoalContract{ID: "goal"}
	w.gates.contract = &gateContract{ID: "architecture", Revision: 1}
	w.arch.snap = archSnapshot{Loaded: true, Status: app.ArchitectureStatus{Exists: true, Revision: 1, Answering: true}}
	w.milestone.Store(&planningMark{PlanningMilestone: app.PlanningMilestone{Step: stageArch, Now: "the architecture is ready for you to accept in the browser", OwnerTurn: true}})
	in := w.flowInputs()
	if got := flowNowLine(in); !strings.Contains(got.Marshal, "answering your request") || !got.FromYouOK {
		t.Fatalf("unanswered request: %+v", got)
	}
	if d, ok := w.v2DecisionNow(time.Now()); ok && d.kind == decisionArchitecture {
		t.Fatal("accept offered while Marshal answers")
	}
}

func TestLiveNowTimerRestartsForState(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	oldClock := boardNow
	boardNow = func() time.Time { return now }
	t.Cleanup(func() { boardNow = oldClock })
	w.state.Goal = model.GoalContract{ID: "goal"}
	w.milestone.Store(&planningMark{PlanningMilestone: app.PlanningMilestone{Step: 2, Now: "reading the code"}, Since: now})
	_ = w.flowInputs()
	now = now.Add(7 * time.Minute)
	if got := flowDoing(w.flowInputs()); !strings.Contains(got, "7m") {
		t.Fatalf("unchanged state lost elapsed: %s", got)
	}
	w.arch.snap = archSnapshot{Loaded: true, Status: app.ArchitectureStatus{Exists: true, Revision: 1, Accepted: true}}
	if got := flowDoing(w.flowInputs()); !strings.Contains(got, "planning the tasks") || strings.Contains(got, "7m") {
		t.Fatalf("stale state/time: %s", got)
	}
}

func TestLiveNowTimerRestartsForRevision(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	oldClock := boardNow
	boardNow = func() time.Time { return now }
	t.Cleanup(func() { boardNow = oldClock })
	w.state.Goal = model.GoalContract{ID: "goal"}
	w.arch.snap = archSnapshot{Loaded: true, Status: app.ArchitectureStatus{Exists: true, Revision: 1}}
	w.milestone.Store(&planningMark{PlanningMilestone: app.PlanningMilestone{Step: stageArch, Now: "the architecture is ready for you to accept in the browser", OwnerTurn: true}, Since: now})
	_ = w.flowInputs()
	now = now.Add(26 * time.Minute)
	if got := flowDoing(w.flowInputs()); !strings.Contains(got, "26m") {
		t.Fatalf("unchanged proposal lost elapsed: %s", got)
	}
	w.arch.snap.Status.Revision = 2
	if got := flowDoing(w.flowInputs()); strings.Contains(got, "26m") {
		t.Fatalf("revised proposal kept stale timer: %s", got)
	}
}

func TestLiveStoppedCardCarriesActivityCause(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.state.Marshal = newMarshalPanel("run-1", "codex", marshal.Run{State: marshal.Dispatching}, "The run stopped unexpectedly.")
	w.state.Marshal.Stopped = true
	cause := "Marshal model turn: exit status 1: provider failed\nstderr: connection closed"
	w.addNote("Marshal run stopped: wrong run\nRun: run-other\n", true)
	if z, _ := w.v2PauseNow(time.Now()); z.detail != "" {
		t.Fatalf("used another run's cause: %s", z.detail)
	}
	w.addNote("Marshal run stopped: "+cause+"\nRun: run-1\n", true)
	z, ok := w.v2PauseNow(time.Now())
	if !ok || z.detail != cause {
		t.Fatalf("recorded cause lost: %+v", z)
	}
	found := false
	for _, a := range z.card.Actions {
		if a.Key == "d" {
			found = true
		}
	}
	if !found {
		t.Fatal("no Raw error action")
	}
}
