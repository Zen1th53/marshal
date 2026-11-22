//go:build linux

package tui

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestPausedForTracksOnlyTheHeldRunAndClearsOnResume(t *testing.T) {
	m := &marshalSession{}
	if m.pausedFor("R1") {
		t.Fatal("run was paused before a hold")
	}
	m.holdDispatch("R1")
	if !m.pausedFor("R1") || m.pausedFor("R2") {
		t.Fatal("pause display did not track only the held run")
	}
	m.releaseDispatch()
	if m.pausedFor("R1") {
		t.Fatal("pause display remained after resume")
	}
}

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

// k sends "keep the current goal", releases the hold without any proposal and
// closes the card.
func TestChangeCardKeepReleases(t *testing.T) {
	w, tr := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
	hp, _ := parseMarshalProposal(holdProposalLine(`["a"]`, "switch to OAuth"))
	w.applyHold(hp)
	if !w.marshalSession().isHeldFor("run", "a") {
		t.Fatal("task a was not held")
	}
	if !w.handleV2CardKey(KeyEvent{Type: KeyRune, Rune: 'k'}) {
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

// A major amendment is not resumed; the hold is still released and the card
// still closes.
func TestBusyAmendMajorDoesNotResume(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.change.card = &changeCard{runID: "run", notStarted: 1, applying: true, ownerApplied: true}
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

func ownerHoldProposalLine(ids, reason string) string {
	data, err := json.Marshal(map[string]string{"action": "hold", "ids": ids, "reason": reason, "until": "release"})
	if err != nil {
		panic(err)
	}
	return marshalProposalPrefix + string(data)
}

func marshalInboxText(t *testing.T, w *Workspace) string {
	t.Helper()
	data, err := os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// The regression from a live run: a draft that superseded the watched run
// kept the service wired for the old run ID, while the hold was keyed to the
// new run, so dispatch never saw the hold and launched the task once its
// dependency merged. The service's hooks now take the run being dispatched.
func TestHoldReachesServiceWiredForASupersededRun(t *testing.T) {
	w, _ := changeChatWorkspace(t, []taskSpec{{"live-voice", marshal.Queued}})
	service, _, _, err := w.marshalService(t.Context(), "RUN-superseded")
	if err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.service = service // as importWatchedMarshalDraft keeps it for the new run
	m.mu.Unlock()
	hp, err := parseMarshalProposal(holdProposalLine(`["live-voice"]`, "Owner: hold live-voice"))
	if err != nil {
		t.Fatal(err)
	}
	w.applyHold(hp)
	if !service.HeldTask("run", "live-voice") {
		t.Fatal("the service does not see the hold on the run it dispatches")
	}
	if _, ok := service.ClaimDispatch("run", "live-voice"); ok {
		t.Fatal("the service's dispatch claim was granted for a held task")
	}
	if service.HeldTask("RUN-superseded", "live-voice") {
		t.Fatal("the hold leaked onto another run")
	}
}

// An amendment the Marshal sends on its own while a change card is open runs
// through the busy-apply sequence but does not release the hold: only the
// owner's "apply this change" does. The Marshal is told the hold stays.
func TestMarshalAmendWithoutOwnerApplyKeepsHold(t *testing.T) {
	w, _ := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
	hp, _ := parseMarshalProposal(holdProposalLine(`["a"]`, "Owner: hold a until ready"))
	w.applyHold(hp)
	w.change.stopFn = func() string { return "" }
	w.change.busyFn = func() bool { return false }
	amended := make(chan struct{}, 1)
	w.change.amendFn = func(context.Context, string) (bool, error) { amended <- struct{}{}; return false, nil }
	w.change.resumeFn = func(context.Context) (string, error) { return "", nil }
	w.beginChangeApply(marshalProposal{action: "amend", reason: "b: scope change"})
	<-amended
	chatWaitFor(t, "the sequence to finish", func() bool {
		w.change.mu.Lock()
		defer w.change.mu.Unlock()
		return !w.change.inFlight
	})
	if !w.marshalSession().isHeldFor("run", "a") {
		t.Fatal("the Marshal's own amendment released the hold")
	}
	card := w.currentChange()
	if card == nil || card.applying {
		t.Fatalf("the card did not return to its keys: %+v", card)
	}
	if text := marshalInboxText(t, w); !strings.Contains(text, "hold kept") {
		t.Fatalf("the Marshal was not told the hold stays:\n%s", text)
	}
}

// A release proposal ends the owner's hold; a refused hold tells the Marshal
// why in its inbox; "until" takes only "release" and only on a hold.
func TestOwnerHoldReleaseProposalAndRefusalReceipt(t *testing.T) {
	w, _ := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
	hp, _ := parseMarshalProposal(ownerHoldProposalLine(`["a"]`, "Owner: hold a"))
	w.applyHold(hp)
	rp, _ := parseMarshalProposal(releaseProposalLine(`["a"]`))
	w.applyRelease(rp)
	if w.marshalSession().isHeldFor("run", "a") || w.currentChange() != nil {
		t.Fatal("the release proposal did not end the owner's hold")
	}
	bad, _ := parseMarshalProposal(ownerHoldProposalLine(`["nope"]`, "Owner: hold"))
	w.applyHold(bad)
	if inbox := marshalInboxText(t, w); !strings.Contains(inbox, "hold refused") || !strings.Contains(inbox, "task nope is not in the active run") {
		t.Fatalf("no refusal receipt:\n%s", inbox)
	}
	for _, line := range []string{
		marshalProposalPrefix + `{"action":"hold","ids":"[\"a\"]","reason":"r","until":"later"}`,
		marshalProposalPrefix + `{"action":"release","ids":"[\"a\"]","until":"release"}`,
	} {
		if _, err := parseMarshalProposal(line); err == nil {
			t.Errorf("parsed but should be refused: %s", line)
		}
	}
}
