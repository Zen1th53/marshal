package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

func TestRecoveredReviewWakesMarshal(t *testing.T) {
	w := emptyBoardWorkspace(t)
	p := demoStallPanel(marshal.Reviewing)
	p.run.Tasks[0].State = marshal.HandedIn
	var notices []string
	w.draftTell = func(s string) { notices = append(notices, s) }
	w.wakeRecoveredReview(p.RunID, p.run)
	if len(notices) != 1 || !strings.Contains(notices[0], p.RunID) || !strings.Contains(notices[0], `"action":"resume"`) {
		t.Fatalf("recovered hand-in did not wake the Marshal to resume: %q", notices)
	}
	notices = nil
	p.run.Pause = &marshal.Pause{Reason: "owner decision"}
	w.wakeRecoveredReview(p.RunID, p.run)
	p.run.Pause = nil
	p.run.State = marshal.AwaitingUser
	w.wakeRecoveredReview(p.RunID, p.run)
	p.run.State = marshal.Reviewing
	p.run.Tasks[0].State = marshal.Queued
	w.wakeRecoveredReview(p.RunID, p.run)
	if len(notices) != 0 {
		t.Fatalf("woke without a resumable hand-in: %q", notices)
	}
	p.run.Tasks[0].State = marshal.HandedIn
	m := w.marshalSession()
	m.mu.Lock()
	m.runID, m.busy = p.RunID, true
	m.mu.Unlock()
	w.wakeRecoveredReview(p.RunID, p.run)
	if len(notices) != 0 {
		t.Fatal("woke a review already in flight")
	}
}

func TestReviewWithoutDriverBecomesOwnerAsk(t *testing.T) {
	old := boardNow
	t.Cleanup(func() { boardNow = old })
	w := emptyBoardWorkspace(t)
	p := demoStallPanel(marshal.Reviewing)
	p.run.Tasks = append(p.run.Tasks, p.run.Tasks[0])
	p.run.Tasks[0].State = marshal.HandedIn
	w.setMarshalPanel(newMarshalPanel(p.RunID, "claude", p.run, ""))
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	atBoardTime(now)
	if in := w.flowInputs(); in.stall.overdue(in.now) {
		t.Fatal("fresh review stalled")
	}
	atBoardTime(now.Add(6 * time.Minute))
	in := w.flowInputs()
	from, ok := fromYou(in)
	if state, _ := flowState(in); state != "YOUR TURN" || ok || from != "hand-in waiting for review for 6m: open the Marshal chat" {
		t.Fatalf("abandoned review: state %q, from you %q %v", state, from, ok)
	}
	if asks := ownerAsks(in); len(asks) != 1 || asks[0].line != from {
		t.Fatalf("missing needs-you item with review stall reason: %+v", asks)
	}
	// An actual review in flight clears the timer, even without a worker lane.
	p = w.marshalPanel()
	p.Board.Run.Driving = true
	w.setMarshalPanel(p)
	if in := w.flowInputs(); in.stall != nil {
		t.Fatal("active review reported stalled")
	}
	p.Board.Run.Driving = false
	w.setMarshalPanel(p)
	atBoardTime(now.Add(7 * time.Minute))
	w.flowInputs()
	atBoardTime(now.Add(11 * time.Minute))
	if in := w.flowInputs(); in.stall.overdue(in.now) {
		t.Fatal("active review did not reset the stall timer")
	}
	for _, tc := range []struct {
		name   string
		change func(*MarshalPanel)
	}{
		{"live lane", func(p *MarshalPanel) { p.Board.Run.ActiveLanes = 1 }},
		{"owner paused", func(p *MarshalPanel) { p.Board.Run.UserPaused = true }},
		{"decision", func(p *MarshalPanel) { p.run.Pause = &marshal.Pause{Reason: "decision"} }},
		{"working task", func(p *MarshalPanel) { p.Board.Tasks[1].State = boardmodel.TaskWorking }},
		{"closed run", func(p *MarshalPanel) { p.run.State = marshal.Closed }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := demoStallPanel(marshal.Reviewing)
			p.run.Tasks[0].State = marshal.HandedIn
			p = newMarshalPanel(p.RunID, "claude", p.run, "")
			tc.change(p)
			w.setMarshalPanel(p)
			if in := w.flowInputs(); in.stall != nil {
				t.Fatalf("%s reported stalled", tc.name)
			}
		})
	}
}
