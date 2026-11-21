package tui

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

func boardWithRun(state marshal.RunState, userPaused bool) model.Board {
	return model.Board{Run: model.RunHead{ID: "R1", State: state, UserPaused: userPaused}}
}

// The top bar's state follows runWord's phase, with the classic header's
// words unchanged.
func TestFlowStateFollowsRunPhase(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		v     boardVisual
		phase runPhase
		word  string
		state string
		tone  vm.Tone
	}{
		{"user paused", boardVisual{board: boardWithRun(marshal.Dispatching, true)}, phaseUserPaused, "paused", "PAUSED", vm.ToneWarn},
		{"stopped", boardVisual{board: boardWithRun(marshal.Dispatching, false), stopped: true}, phaseStopped, "paused", "PAUSED", vm.ToneBad},
		{"waiting", boardVisual{board: boardWithRun(marshal.Dispatching, false), ask: &askCard{Question: "q"}}, phaseWaiting, "waiting for you", "YOUR TURN", vm.ToneWarn},
		{"reviewing", boardVisual{board: boardWithRun(marshal.Reviewing, false)}, phaseReviewing, "reviewing", "REVIEWING", vm.ToneVio},
		{"done", boardVisual{board: boardWithRun(marshal.Closed, false)}, phaseDone, "done", "DONE", vm.ToneOK},
		{"merging", boardVisual{board: boardWithRun(marshal.Merging, false)}, phaseMerging, "merging", "MERGING", vm.ToneInfo},
		{"final check", boardVisual{board: boardWithRun(marshal.Verifying, false)}, phaseFinalCheck, "final check: re-running the checks on the merged result", "FINAL CHECK", vm.ToneInfo},
		{"planning", boardVisual{board: boardWithRun(marshal.Drafting, false)}, phasePlanning, "planning", "PLANNING", vm.ToneInfo},
		{"starting", boardVisual{board: boardWithRun("", false)}, phaseStarting, "starting", "STARTING", vm.ToneMuted},
		{"running", boardVisual{board: boardWithRun(marshal.Dispatching, false)}, phaseRunning, "running", "RUNNING", vm.ToneOK},
	}
	quiet := boardWithRun(marshal.Dispatching, false)
	quiet.Run.WorkerSeen = now.Add(-time.Hour)
	cases = append(cases, struct {
		name  string
		v     boardVisual
		phase runPhase
		word  string
		state string
		tone  vm.Tone
	}{"not running", boardVisual{board: quiet, now: now}, phaseNotRunning, "not running: no worker for 1h00m", "NOT RUNNING", vm.ToneWarn})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			phase, word, _ := c.v.runWord()
			if phase != c.phase || word != c.word {
				t.Fatalf("runWord = %d %q, want %d %q", phase, word, c.phase, c.word)
			}
			if state, tone := flowState(flowIn{hasRun: true, v: c.v}); state != c.state || tone != c.tone {
				t.Fatalf("flowState = %q %v, want %q %v", state, tone, c.state, c.tone)
			}
		})
	}
}

// The NOW line's "from you" and the decision card name the same owner card
// first, and list them in the same order.
func TestOwnerAsksFollowTheCardOrder(t *testing.T) {
	w := &Workspace{}
	w.guard.alert = &guardAlert{paths: []string{"a.go"}}
	w.next.card = &nextCard{RunID: "R1"}
	w.ask.card = &askCard{Question: "which one?"}
	w.req.card = &reqCard{}
	w.change.card = &changeCard{runID: "R1"}
	var kinds []string
	for _, d := range w.v2OwnerCards() {
		kinds = append(kinds, d.card.Kind)
	}
	want := ownerCardOrder[:]
	if len(kinds) != len(want) {
		t.Fatalf("cards %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("cards %v, want %v", kinds, want)
		}
	}

	v := boardVisual{board: boardWithRun(marshal.Dispatching, false)}
	v.guard, _, _, _ = w.guardPaint()
	v.ask, v.req, v.change, v.next = w.currentAsk(), w.currentReq(), w.currentChange(), w.currentNext()
	lines := map[string]string{
		decisionGuard:        "changed files: keep or undo them",
		decisionNext:         "the run is done: choose what is next",
		decisionAsk:          "the Marshal asks: which one?",
		decisionRequirements: "check the requirements",
		decisionChange:       "confirm the plan change",
	}
	asks := ownerAsks(flowIn{hasRun: true, v: v})
	if len(asks) < len(want) {
		t.Fatalf("asks %+v", asks)
	}
	for i, k := range want {
		if asks[i].line != lines[k] {
			t.Fatalf("ask %d = %q, want %q (the %s card)", i, asks[i].line, lines[k], k)
		}
	}

	// Without the guard, "what next?" leads both.
	w.guard.alert = nil
	v.guard = nil
	if got := w.v2OwnerCards()[0].card.Kind; got != decisionNext {
		t.Fatalf("first card %q", got)
	}
	if got, _ := fromYou(flowIn{hasRun: true, v: v}); got != lines[decisionNext]+" · +3 more" {
		t.Fatalf("from you %q", got)
	}
}

// currentReq and currentChange hand out copies: a reader cannot change the
// open card.
func TestCurrentCardsAreCopies(t *testing.T) {
	w := &Workspace{}
	w.req.card = &reqCard{}
	w.change.card = &changeCard{runID: "R1", held: []string{"T-1"}}
	r := w.currentReq()
	r.editing = true
	r.Lines[0].Text = "changed"
	c := w.currentChange()
	c.applying = true
	c.held[0] = "T-9"
	if w.req.card.editing || w.req.card.Lines[0].Text != "" {
		t.Fatalf("currentReq shared the card: %+v", *w.req.card)
	}
	if w.change.card.applying || w.change.card.held[0] != "T-1" {
		t.Fatalf("currentChange shared the card: %+v", *w.change.card)
	}
	w.req.card, w.change.card = nil, nil
	if w.currentReq() != nil || w.currentChange() != nil {
		t.Fatal("no card read as a card")
	}
}

// v verifies, on Board v2, the first provider its checklist shows as "not
// checked yet": the Marshal's, then the workers in v2ProviderOrder.
func TestV2NextUnchecked(t *testing.T) {
	now := time.Now()
	all := map[string]bool{"claude": true, "agy": true, "codex": true, "opencode": true}
	in := v2MarshalIn{Now: now, Probed: true, Probe: v2Probe{Lead: "codex", Installed: all}, SignedIn: map[string]bool{"codex": true}}
	// The classic chips' order would start at codex; v2 lists claude first.
	if got := v2NextUnchecked(in); got != "claude" {
		t.Fatalf("next = %q, want claude", got)
	}
	in.SignedIn = map[string]bool{"claude": true}
	if got := v2NextUnchecked(in); got != "codex" {
		t.Fatalf("an unchecked Marshal provider is not first: %q", got)
	}
	in.SignedIn = map[string]bool{"codex": true, "claude": true}
	in.Probe.Installed = map[string]bool{"claude": true, "codex": true, "opencode": true}
	if got := v2NextUnchecked(in); got != "opencode" {
		t.Fatalf("next = %q, want opencode (agy is not installed)", got)
	}
	in.SignedIn["opencode"] = true
	if got := v2NextUnchecked(in); got != "" {
		t.Fatalf("every provider checked, next = %q", got)
	}
	if got := v2NextUnchecked(v2MarshalIn{Now: now}); got != "" {
		t.Fatalf("before the first lookup, next = %q", got)
	}
}
