package tui

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// holdProposalLine builds a MARSHAL_PROPOSAL "hold" line. ids is the inner
// JSON array as a string, the way the Marshal writes it.
func holdProposalLine(ids, reason string) string {
	data, err := json.Marshal(map[string]string{"action": "hold", "ids": ids, "reason": reason})
	if err != nil {
		panic(err)
	}
	return marshalProposalPrefix + string(data)
}

func releaseProposalLine(ids string) string {
	data, err := json.Marshal(map[string]string{"action": "release", "ids": ids})
	if err != nil {
		panic(err)
	}
	return marshalProposalPrefix + string(data)
}

type taskSpec struct {
	id    string
	state marshal.TaskState
}

func tasksFrom(specs []taskSpec) []marshal.Task {
	out := make([]marshal.Task, 0, len(specs))
	for _, s := range specs {
		out = append(out, marshal.Task{PlanTaskID: s.id, State: s.state})
	}
	return out
}

// changeChatWorkspace is a Board with a native chat, an active run "run" whose
// tasks hold the given states, and the marshalSession bound to it.
func changeChatWorkspace(t *testing.T, specs []taskSpec) (*Workspace, *fakeChatTransport) {
	t.Helper()
	w, _, tr := boardWithNativeChat(t)
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = "run"
	m.service = w.runtime.Marshal()
	m.mu.Unlock()
	run := marshal.Run{PlanID: "PLAN-1", PlanVersion: 1, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings(), Tasks: tasksFrom(specs)}
	if _, err := m.service.Store.SetMarshalRun(t.Context(), w.projectID, "run", run, 0); err != nil {
		t.Fatal(err)
	}
	return w, tr
}

// The example hold proposal parses; malformed holds and releases are refused
// as invalid, the same error any other bad proposal gets. An unknown task ID
// is refused when the hold is applied, with the usual Activity line.
func TestHoldProposalValidation(t *testing.T) {
	p, err := parseMarshalProposal(holdProposalLine(`["t1","t2"]`, "switch login to OAuth"))
	if err != nil {
		t.Fatalf("the example hold did not parse: %v", err)
	}
	if p.action != "hold" {
		t.Fatalf("action %q", p.action)
	}
	ids, err := parseHoldIDs(p.ids)
	if err != nil || len(ids) != 2 || ids[0] != "t1" || ids[1] != "t2" {
		t.Fatalf("ids %v err %v", ids, err)
	}
	if _, err := parseMarshalProposal(releaseProposalLine(`["t1"]`)); err != nil {
		t.Fatalf("the example release did not parse: %v", err)
	}

	bad := map[string]string{
		"empty ids":          holdProposalLine(`[]`, "r"),
		"ids not array":      holdProposalLine(`"t1"`, "r"),
		"bad char in id":     holdProposalLine(`["t 1"]`, "r"),
		"empty reason":       holdProposalLine(`["t1"]`, "  "),
		"reason too long":    holdProposalLine(`["t1"]`, strings.Repeat("x", 501)),
		"release with extra": releaseProposalLine(`["t1"]`), /*patched below*/
	}
	// "release" must carry only action and ids; a reason is an extra field.
	bad["release with extra"] = marshalProposalPrefix + `{"action":"release","ids":"[\"t1\"]","reason":"x"}`
	// 51 ids exceeds the cap.
	var many []string
	for i := 0; i < 51; i++ {
		many = append(many, "t"+strings.Repeat("0", 1)+string(rune('a'+i%26))+string(rune('0'+i%10)))
	}
	manyJSON, _ := json.Marshal(many)
	bad["too many ids"] = holdProposalLine(string(manyJSON), "r")
	for name, line := range bad {
		if _, err := parseMarshalProposal(line); err == nil {
			t.Errorf("%s: parsed but should be refused", name)
		}
	}

	// An unknown task ID is refused when the hold is applied.
	w, _ := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
	hp, err := parseMarshalProposal(holdProposalLine(`["nope"]`, "r"))
	if err != nil {
		t.Fatal(err)
	}
	w.applyHold(hp)
	if w.currentChange() != nil {
		t.Fatal("a card opened for an unknown task")
	}
	if w.marshalSession().heldCount() != 0 {
		t.Fatal("an unknown task was held")
	}
	if !changeNoteSeen(w, "Ignored Marshal proposal") {
		t.Fatalf("no ignored-proposal note: %v", w.noteList())
	}
}

func changeNoteSeen(w *Workspace, substr string) bool {
	for _, n := range w.noteList() {
		if strings.Contains(n.Text, substr) {
			return true
		}
	}
	return false
}

// The card counts only the tasks that had not started; a task already running
// is reported as finishing first.
func TestHoldCardCountsOnlyNotStarted(t *testing.T) {
	w, _ := changeChatWorkspace(t, []taskSpec{
		{"a", marshal.Queued},
		{"b", marshal.Dispatched},
		{"c", marshal.Returned},
	})
	hp, err := parseMarshalProposal(holdProposalLine(`["a","b","c"]`, "switch to OAuth"))
	if err != nil {
		t.Fatal(err)
	}
	w.applyHold(hp)
	card := w.currentChange()
	if card == nil {
		t.Fatal("the card did not open")
	}
	if card.notStarted != 2 || card.running != 1 {
		t.Fatalf("counts: notStarted %d running %d", card.notStarted, card.running)
	}
	want := "I paused 2 affected tasks. 1 is already running and will finish first."
	if got := changeHoldHeadline(card.notStarted, card.running); got != want {
		t.Fatalf("headline %q want %q", got, want)
	}
	text := frameText(w, 120, 40)
	if !strings.Contains(text, "I paused 2 affected tasks") || !strings.Contains(text, "[Enter] apply this change") || !strings.Contains(text, "[k] keep the current goal") {
		t.Fatalf("the card is not on screen:\n%s", text)
	}

	// All held tasks already running: N == 0.
	w2, _ := changeChatWorkspace(t, []taskSpec{{"b", marshal.Dispatched}})
	hp2, _ := parseMarshalProposal(holdProposalLine(`["b"]`, "r"))
	w2.applyHold(hp2)
	c2 := w2.currentChange()
	if c2 == nil || c2.notStarted != 0 || c2.running != 1 {
		t.Fatalf("running-only counts: %+v", c2)
	}
	if got := changeHoldHeadline(c2.notStarted, c2.running); got != "The affected tasks are already running; they will finish first." {
		t.Fatalf("running-only headline %q", got)
	}

	// One not-started task: singular wording.
	if got := changeHoldHeadline(1, 0); got != "I paused 1 affected task." {
		t.Fatalf("singular headline %q", got)
	}
}

// k sends "keep the current goal", releases the hold without any proposal and
// closes the card.
func TestChangeCardKeepReleases(t *testing.T) {
	w, tr := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
	hp, _ := parseMarshalProposal(holdProposalLine(`["a"]`, "switch to OAuth"))
	w.applyHold(hp)
	if !w.marshalSession().isHeldFor("run", "a") {
		t.Fatal("task a was not held")
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'k'}) {
		t.Fatal("k was not taken by the card")
	}
	if w.currentChange() != nil {
		t.Fatal("the card stayed open after keep")
	}
	if w.marshalSession().isHeldFor("run", "a") {
		t.Fatal("keep did not release the hold")
	}
	chatWaitFor(t, "the keep message", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns(); got[0] != "keep the current goal" {
		t.Fatalf("sent %q, want \"keep the current goal\"", got)
	}
}

// Enter sends "apply this change", shows "Applying your change…" and leaves the
// hold in place until the change is applied.
func TestChangeCardApplySays(t *testing.T) {
	w, tr := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
	hp, _ := parseMarshalProposal(holdProposalLine(`["a"]`, "switch to OAuth"))
	w.applyHold(hp)
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken by the card")
	}
	card := w.currentChange()
	if card == nil || !card.applying {
		t.Fatalf("the card is not applying: %+v", card)
	}
	if !w.marshalSession().isHeldFor("run", "a") {
		t.Fatal("apply released the hold before the change was applied")
	}
	chatWaitFor(t, "the apply message", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns(); got[0] != "apply this change" {
		t.Fatalf("sent %q, want \"apply this change\"", got)
	}
	if text := frameText(w, 120, 40); !strings.Contains(text, "Applying your change…") {
		t.Fatalf("the card does not show it is applying:\n%s", text)
	}
}

// A major amendment is not resumed; the hold is still released and the card
// still closes.
func TestBusyAmendMajorDoesNotResume(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.change.card = &changeCard{runID: "run", notStarted: 1, applying: true}
	resumed := false
	w.change.stopFn = func() string { return "" }
	w.change.busyFn = func() bool { return false }
	w.change.amendFn = func(context.Context, string) (bool, error) { return true, nil }
	w.change.resumeFn = func(context.Context) (string, error) { resumed = true; return "", nil }
	w.applyChangeSequence(context.Background(), "big change")
	if resumed {
		t.Fatal("a major amendment was resumed")
	}
	if w.currentChange() != nil {
		t.Fatal("the card stayed open after a major amendment")
	}
}

// When the run cannot be paused within the budget, the card stays open and
// asks the owner to try again; the amendment is not attempted.
func TestBusyAmendTimeoutKeepsCard(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.change.card = &changeCard{runID: "run", notStarted: 1, applying: true}
	w.change.poll = time.Millisecond
	w.change.timeout = 3 * time.Millisecond
	amendCalled := false
	w.change.stopFn = func() string { return "" }
	w.change.busyFn = func() bool { return true }
	w.change.amendFn = func(context.Context, string) (bool, error) { amendCalled = true; return false, nil }
	w.change.resumeFn = func(context.Context) (string, error) { return "", nil }
	w.applyChangeSequence(context.Background(), "switch to OAuth")
	if amendCalled {
		t.Fatal("the amendment was attempted although the run never paused")
	}
	card := w.currentChange()
	if card == nil || !card.timedOut {
		t.Fatalf("the card did not stay open with a timeout: %+v", card)
	}
	if !strings.Contains(w.state.LastOutput, "I couldn't pause the run to apply the change. Press Enter to try again.") {
		t.Fatalf("no try-again message: %q", w.state.LastOutput)
	}
}

// The ask, requirements and change cards can be open together: they render in
// that order, above the gates, and Enter goes to the topmost one still open.
// Each answered card closes and leaves the others open.
func TestChangeCardBesideAskAndRequirements(t *testing.T) {
	w, tr := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
	hp, err := parseMarshalProposal(holdProposalLine(`["a"]`, "switch to OAuth"))
	if err != nil {
		t.Fatal(err)
	}
	w.applyHold(hp)
	rp, err := parseMarshalProposal(reqProposalLine(fullReqCard))
	if err != nil {
		t.Fatal(err)
	}
	w.openRequirements(rp)
	ap, err := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
	if err != nil {
		t.Fatal(err)
	}
	w.openAsk(ap)
	if w.currentChange() == nil || w.currentReq() == nil || w.currentAsk() == nil {
		t.Fatal("the three cards are not all open")
	}

	text := frameText(w, 160, 80)
	ask := strings.Index(text, "question · "+exampleAskQuestion)
	req := strings.Index(text, "requirements · 8 of 8 filled")
	change := strings.Index(text, "I paused 1 affected task")
	if ask < 0 || req < 0 || change < 0 {
		t.Fatalf("a card is missing (ask %d, requirements %d, change %d):\n%s", ask, req, change, text)
	}
	if !(ask < req && req < change) {
		t.Fatalf("render order is not ask, requirements, change (%d, %d, %d):\n%s", ask, req, change, text)
	}

	// Enter answers the ask first.
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken")
	}
	chatWaitFor(t, "the ask answer", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns()[0]; got != "build on it" {
		t.Fatalf("first Enter sent %q, want the ask's recommended choice", got)
	}
	if w.currentAsk() != nil || w.currentReq() == nil || w.currentChange() == nil {
		t.Fatal("after the ask, only the ask card should have closed")
	}

	// Then the requirements card.
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken")
	}
	chatWaitFor(t, "the confirmation", func() bool { return len(tr.sentTurns()) == 2 })
	if got := tr.sentTurns()[1]; got != "confirm all" {
		t.Fatalf("second Enter sent %q, want \"confirm all\"", got)
	}
	if w.currentReq() != nil || w.currentChange() == nil {
		t.Fatal("after confirming, the change card should be the one still open")
	}

	// Then the change card.
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken")
	}
	chatWaitFor(t, "the apply message", func() bool { return len(tr.sentTurns()) == 3 })
	if got := tr.sentTurns()[2]; got != "apply this change" {
		t.Fatalf("third Enter sent %q, want \"apply this change\"", got)
	}
	if card := w.currentChange(); card == nil || !card.applying {
		t.Fatalf("the change card is not applying: %+v", card)
	}
}

// A hold and a dispatch cannot cross. A task whose launch was already claimed
// when the hold lands is counted as running, not paused; a task held first has
// its claim refused, so it does not launch.
func TestHoldAndDispatchClaimAreAtomic(t *testing.T) {
	w, _ := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}, {"b", marshal.Queued}})
	m := w.marshalSession()
	end, ok := m.claimDispatch("run", "a")
	if !ok {
		t.Fatal("an unheld task's claim was refused")
	}
	hp, _ := parseMarshalProposal(holdProposalLine(`["a","b"]`, "switch to OAuth"))
	w.applyHold(hp)
	card := w.currentChange()
	if card == nil || card.notStarted != 1 || card.running != 1 {
		t.Fatalf("a task mid-launch was counted as paused: %+v", card)
	}
	end()
	if _, ok := m.claimDispatch("run", "b"); ok {
		t.Fatal("a held task's claim was granted")
	}
	if _, ok := m.claimDispatch("other", "b"); !ok {
		t.Fatal("the hold refused another run's task")
	}
}

// The service wiring gives dispatch the claim and Execute the hold-change
// signal, and a hold change wakes a waiting run.
func TestHoldChangeWakesWaitingRun(t *testing.T) {
	w, _ := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
	m := w.marshalSession()
	changed := m.holdChanged()
	hp, _ := parseMarshalProposal(holdProposalLine(`["a"]`, "switch to OAuth"))
	w.applyHold(hp)
	select {
	case <-changed:
	default:
		t.Fatal("applying a hold did not signal a change")
	}
	changed = m.holdChanged()
	w.releaseChange()
	select {
	case <-changed:
	default:
		t.Fatal("releasing the hold did not wake a waiting run")
	}
}

func saysSent(w *Workspace) int {
	w.say.mu.Lock()
	defer w.say.mu.Unlock()
	return w.say.next
}

// While the change is being applied a click on the card does nothing: it
// neither sends "apply this change" again nor releases the hold.
func TestChangeCardClickWhileApplyingSendsOnce(t *testing.T) {
	w, _ := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
	hp, _ := parseMarshalProposal(holdProposalLine(`["a"]`, "switch to OAuth"))
	w.applyHold(hp)
	before := saysSent(w)
	w.clickChange("apply")
	if saysSent(w) != before+1 {
		t.Fatal("the first click did not send the apply message")
	}
	w.clickChange("apply")
	w.clickChange("keep")
	if got := saysSent(w); got != before+1 {
		t.Fatalf("clicks while applying sent %d messages, want 1", got-before)
	}
	card := w.currentChange()
	if card == nil || !card.applying {
		t.Fatalf("the card is no longer applying: %+v", card)
	}
	if !w.marshalSession().isHeldFor("run", "a") {
		t.Fatal("a click while applying released the hold")
	}
}

// A second amend proposal while the change is already being applied is
// refused: only one apply sequence runs.
func TestBusyAmendApplyStartsOnce(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.change.card = &changeCard{runID: "run", notStarted: 1, applying: true}
	release := make(chan struct{})
	stopped := make(chan struct{}, 4)
	var amends atomic.Int32
	w.change.poll = time.Millisecond
	w.change.stopFn = func() string { stopped <- struct{}{}; return "" }
	w.change.busyFn = func() bool {
		select {
		case <-release:
			return false
		default:
			return true
		}
	}
	w.change.amendFn = func(context.Context, string) (bool, error) { amends.Add(1); return false, nil }
	w.change.resumeFn = func(context.Context) (string, error) { return "", nil }
	w.beginChangeApply(marshalProposal{action: "amend", reason: "a switch to OAuth"})
	<-stopped
	w.beginChangeApply(marshalProposal{action: "amend", reason: "a switch to OAuth"})
	if !changeNoteSeen(w, "Ignored Marshal proposal: the change is already being applied.") {
		t.Fatalf("the second amend was not refused: %v", w.noteList())
	}
	close(release)
	chatWaitFor(t, "the card to close", func() bool { return w.currentChange() == nil })
	if got := amends.Load(); got != 1 {
		t.Fatalf("the change was amended %d times, want 1", got)
	}
	if len(stopped) != 0 {
		t.Fatal("a second apply sequence stopped the run")
	}
}

// If the run is no longer busy when the Marshal answers "apply" with an amend,
// the ordinary amendment path runs; when it finishes the hold is released and
// the card closes instead of staying on "Applying your change…".
func TestAmendWhileIdleClosesChangeCard(t *testing.T) {
	w, _ := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
	hp, _ := parseMarshalProposal(holdProposalLine(`["a"]`, "switch to OAuth"))
	w.applyHold(hp)
	w.changeApply()
	if card := w.currentChange(); card == nil || !card.applying {
		t.Fatalf("the card is not applying: %+v", card)
	}
	if _, err := w.marshalAmend(context.Background(), "a switch to OAuth"); err != nil {
		t.Fatalf("the amendment did not start: %v", err)
	}
	chatWaitFor(t, "the card to close", func() bool { return w.currentChange() == nil })
	if w.marshalSession().heldCount() != 0 {
		t.Fatal("the hold was not released after the amendment")
	}
}
