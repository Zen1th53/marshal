//go:build linux

package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The owner cards are Board v2 decision cards: the collectors read the
// classic cards' state, the keys reach the classic cards' key handlers and
// a click on a chip or a line the classic cards' click handlers.

func guardBoard(t *testing.T, byMarshal bool) *Workspace {
	t.Helper()
	w := emptyBoardWorkspace(t)
	t.Cleanup(w.Close)
	t.Cleanup(func() { v2Cards.Delete(w) })
	w.guard.alert = &guardAlert{at: time.Now(), paths: []string{"app/x.py", "app/y.py"}, wasDirty: map[string]bool{}, byMarshal: byMarshal}
	w.guard.last = guardSnapshot{top: t.TempDir(), prefix: "app", files: guardFiles("app/x.py", "??", "1", "app/y.py", " M", "2")}
	return w
}

func changeBoard(t *testing.T, c changeCard) *Workspace {
	t.Helper()
	w := emptyBoardWorkspace(t)
	t.Cleanup(w.Close)
	t.Cleanup(func() { v2Cards.Delete(w) })
	w.change.card = &c
	return w
}

// Each collector fills the card from the classic card's own state: title,
// summary, content and the classic keys as chips, each chip with the click
// action the classic button has.
func TestOwnerCardCollectors(t *testing.T) {
	t.Run("ask", func(t *testing.T) {
		w := askBoard(t)
		defer w.Close()
		defer v2Cards.Delete(w)
		d := cardsFrame(w).Decision
		if d == nil || d.Kind != "ask" || d.Title != "Marshal asks · "+exampleAskQuestion {
			t.Fatalf("ask card %+v", d)
		}
		if len(d.Lines) != 1 || d.Lines[0].Text != exampleAskQuestion {
			t.Fatalf("lines %+v", d.Lines)
		}
		if got := keyNames(d.Actions); !slices.Equal(got, []string{"⏎ build on it", "l leave it", "w wait for it"}) {
			t.Fatalf("actions %q", got)
		}
		if !slices.Equal(d.Refs, []string{"ask:0", "ask:1", "ask:2"}) {
			t.Fatalf("refs %q", d.Refs)
		}
	})
	t.Run("requirements", func(t *testing.T) {
		w := reqBoard(t)
		defer w.Close()
		defer v2Cards.Delete(w)
		d := cardsFrame(w).Decision
		if d == nil || d.Kind != "requirements" || d.Title != "Confirm requirements · 6 of 8" || d.ChangedNote != "2 blank: it asks them one at a time" {
			t.Fatalf("requirements card %+v", d)
		}
		if len(d.Lines) != 8 {
			t.Fatalf("%d lines", len(d.Lines))
		}
		if l := d.Lines[2]; l.Key != "c" || l.Label != "branches" || l.Text != "from main, lands on main" || l.Note != "· git" || l.Ref != "requirements:c" {
			t.Fatalf("line c %+v", l)
		}
		if l := d.Lines[7]; l.Text != "—" || l.Note != "· (to ask)" || l.Tone != vm.ToneMuted {
			t.Fatalf("blank line h %+v", l)
		}
		if got := keyNames(d.Actions); !slices.Equal(got, []string{"⏎ confirm all", "e edit a line"}) || !slices.Equal(d.Refs, []string{"requirements:enter", "requirements:edit"}) {
			t.Fatalf("actions %q refs %q", got, d.Refs)
		}
		w.setReqEditing(true)
		d = cardsFrame(w).Decision
		if got := keyNames(d.Actions); !slices.Equal(got, []string{"esc cancel"}) || d.Corner != "esc cancel" || !strings.HasPrefix(d.ChangedNote, "Which line?") {
			t.Fatalf("editing card %q %q %q", got, d.Corner, d.ChangedNote)
		}
	})
	t.Run("change", func(t *testing.T) {
		for _, c := range []struct {
			card    changeCard
			title   string
			summary string
			actions []string
			refs    []string
		}{
			{changeCard{runID: "run", notStarted: 2, running: 1}, "Change · apply it or keep the current goal", "I paused 2 affected tasks. 1 is already running and will finish first.",
				[]string{"⏎ apply this change", "k keep the current goal"}, []string{"change:apply", "change:keep"}},
			{changeCard{runID: "run", applying: true}, "Change · applying", "Applying your change…", nil, nil},
			{changeCard{runID: "run", applying: true, timedOut: true}, "Change · the run could not pause", "I couldn't pause the run to apply the change. Press Enter to try again.",
				[]string{"⏎ try again"}, []string{"change:apply"}},
			{changeCard{runID: "run", held: []string{"T03"}}, "Change · held by you", "T03 is held until you release it; it will not start.",
				[]string{"x release the hold"}, []string{"change:release"}},
		} {
			w := changeBoard(t, c.card)
			d := cardsFrame(w).Decision
			if d == nil || d.Kind != "change" || d.Title != c.title || d.Summary != c.summary {
				t.Fatalf("change card %+v", d)
			}
			if got := keyNames(d.Actions); !slices.Equal(got, c.actions) || !slices.Equal(d.Refs, c.refs) {
				t.Fatalf("%s: actions %q refs %q", c.title, got, d.Refs)
			}
		}
	})
	t.Run("next", func(t *testing.T) {
		w := emptyBoardWorkspace(t)
		defer w.Close()
		defer v2Cards.Delete(w)
		w.openNext(boardmodel.RunHead{ID: "run", State: marshal.Closed, Done: 3, Total: 4})
		d := cardsFrame(w).Decision
		if d == nil || d.Kind != "next" || d.Title != "What next?" || d.Summary != "Done: 3 of 4 tasks verified and merged. Not done: 1." {
			t.Fatalf("next card %+v", d)
		}
		if got := keyNames(d.Actions); !slices.Equal(got, []string{"n new goal", "d details", "q quit"}) || !slices.Equal(d.Refs, []string{"next:n", "next:d", "next:q"}) {
			t.Fatalf("actions %q refs %q", got, d.Refs)
		}
	})
	t.Run("guard", func(t *testing.T) {
		w := guardBoard(t, true)
		d := cardsFrame(w).Decision
		if d == nil || d.Kind != "guard" || d.Title != "Guard · the Marshal changed project files" {
			t.Fatalf("guard card %+v", d)
		}
		if len(d.Lines) != 2 || d.Lines[0].Text != "x.py" || d.Lines[1].Text != "y.py" {
			t.Fatalf("lines %+v", d.Lines)
		}
		if got := keyNames(d.Actions); !slices.Equal(got, []string{"u undo (recommended)", "K keep"}) || !slices.Equal(d.Refs, []string{"guard:u", "guard:K"}) {
			t.Fatalf("actions %q refs %q", got, d.Refs)
		}
		w.guardArm()
		d = cardsFrame(w).Decision
		if got := keyNames(d.Actions); !slices.Equal(got, []string{"⏎ undo", "esc cancel"}) || d.Corner != "esc cancel" {
			t.Fatalf("armed actions %q corner %q", got, d.Corner)
		}
		if last := d.Lines[len(d.Lines)-1]; last.Text != "undo: delete 1 new file, restore 1 to HEAD, nothing else?" || last.Tone != vm.ToneWarn {
			t.Fatalf("armed line %+v", last)
		}
	})
}

// keyCase is one owner card and a key sequence: the same keys reach the
// same classic handler on the v2 card as on the classic Board, so the
// state after them is the same.
type keyCase struct {
	name  string
	setup func(t *testing.T) *Workspace
	keys  []KeyEvent
	state func(w *Workspace) string
}

var ownerKeyCases = []keyCase{
	{"ask letter", func(t *testing.T) *Workspace {
		w, _, _ := boardWithNativeChat(t)
		p, _ := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
		w.openAsk(p)
		return w
	}, []KeyEvent{runeKey('l')}, func(w *Workspace) string {
		return "ask open " + boolWord(w.currentAsk() != nil) + " · " + lastActivity(w)
	}},
	{"ask enter", func(t *testing.T) *Workspace {
		w, _, _ := boardWithNativeChat(t)
		p, _ := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
		w.openAsk(p)
		return w
	}, []KeyEvent{{Type: KeyEnter}}, func(w *Workspace) string {
		return "ask open " + boolWord(w.currentAsk() != nil) + " · " + lastActivity(w)
	}},
	{"requirements blanks", func(t *testing.T) *Workspace { return reqBoard(t) }, []KeyEvent{{Type: KeyEnter}}, func(w *Workspace) string {
		return "req open " + boolWord(w.currentReq() != nil) + " · " + lastOutput(w)
	}},
	{"requirements edit", func(t *testing.T) *Workspace { return reqBoard(t) }, []KeyEvent{runeKey('e'), runeKey('c')}, func(w *Workspace) string {
		return "composer " + w.composer.Text() + " · open " + boolWord(w.say.open.Load())
	}},
	{"requirements edit cancel", func(t *testing.T) *Workspace { return reqBoard(t) }, []KeyEvent{runeKey('e'), {Type: KeyEsc}}, func(w *Workspace) string {
		return "editing " + boolWord(w.currentReq().editing)
	}},
	{"requirements confirm", func(t *testing.T) *Workspace {
		w, _, _ := boardWithNativeChat(t)
		p, _ := parseMarshalProposal(reqProposalLine(fullReqCard))
		w.openRequirements(p)
		return w
	}, []KeyEvent{{Type: KeyEnter}}, func(w *Workspace) string {
		return "req open " + boolWord(w.currentReq() != nil) + " · " + lastActivity(w)
	}},
	{"change keep", func(t *testing.T) *Workspace {
		w, _ := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
		hp, _ := parseMarshalProposal(holdProposalLine(`["a"]`, "switch to OAuth"))
		w.applyHold(hp)
		return w
	}, []KeyEvent{runeKey('k')}, func(w *Workspace) string {
		return "change open " + boolWord(w.currentChange() != nil) + " · held " + boolWord(w.marshalSession().isHeldFor("run", "a"))
	}},
	{"change apply", func(t *testing.T) *Workspace {
		w, _ := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
		hp, _ := parseMarshalProposal(holdProposalLine(`["a"]`, "switch to OAuth"))
		w.applyHold(hp)
		return w
	}, []KeyEvent{{Type: KeyEnter}}, func(w *Workspace) string {
		return "applying " + boolWord(w.changeApplying()) + " · held " + boolWord(w.marshalSession().isHeldFor("run", "a"))
	}},
	{"next details", func(t *testing.T) *Workspace {
		w, _, _ := boardWithNativeChat(t)
		w.verSource = &fakeVerifyView{overview: demoVerifyOverview(time.Now())}
		w.openNext(boardmodel.RunHead{ID: "run", State: marshal.Closed, Done: 1, Total: 1})
		return w
	}, []KeyEvent{runeKey('d')}, func(w *Workspace) string {
		return "verify " + boolWord(w.verView.Load()) + " · next open " + boolWord(w.currentNext() != nil)
	}},
	{"next new goal", func(t *testing.T) *Workspace {
		w, _, _ := boardWithNativeChat(t)
		w.openNext(boardmodel.RunHead{ID: "run", State: marshal.Closed, Done: 1, Total: 1})
		return w
	}, []KeyEvent{runeKey('n')}, func(w *Workspace) string {
		return "chat " + boolWord(w.nativeChatFocused()) + " · next open " + boolWord(w.currentNext() != nil)
	}},
	{"next quit", func(t *testing.T) *Workspace {
		w := emptyBoardWorkspace(t)
		w.openNext(boardmodel.RunHead{ID: "run", State: marshal.Closed, Done: 1, Total: 1})
		return w
	}, []KeyEvent{runeKey('q')}, func(w *Workspace) string {
		w.mu.RLock()
		defer w.mu.RUnlock()
		return "exit " + boolWord(w.exitRequested)
	}},
	{"guard arm and cancel", func(t *testing.T) *Workspace { return guardBoard(t, false) }, []KeyEvent{runeKey('u'), {Type: KeyEsc}}, func(w *Workspace) string {
		_, _, armed, _ := w.guardPaint()
		return "armed " + boolWord(armed) + " · " + lastOutput(w)
	}},
	{"guard arm", func(t *testing.T) *Workspace { return guardBoard(t, false) }, []KeyEvent{runeKey('u')}, func(w *Workspace) string {
		_, _, armed, _ := w.guardPaint()
		return "armed " + boolWord(armed)
	}},
	{"guard keep", func(t *testing.T) *Workspace { return guardBoard(t, false) }, []KeyEvent{runeKey('K')}, func(w *Workspace) string {
		return "alert " + boolWord(w.guardAlertNow() != nil) + " · " + lastOutput(w)
	}},
}

func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func lastActivity(w *Workspace) string {
	notes := w.noteList()
	if len(notes) == 0 {
		return ""
	}
	return notes[0].Text
}

// clickCase is a click on an owner card's chip or line: it records the
// classic card's own click region, so it runs the classic click handler.
func TestOwnerCardClicksRunTheClassicActions(t *testing.T) {
	t.Run("ask choice", func(t *testing.T) {
		w, _, tr := boardWithNativeChat(t)
		defer v2Cards.Delete(w)
		p, _ := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
		w.openAsk(p)
		h, ok := v2Hit(t, w, hitAsk, "2")
		if !ok {
			t.Fatal("no click region on the third choice")
		}
		v2Click(t, w, h)
		chatWaitFor(t, "the answer", func() bool { return len(tr.sentTurns()) == 1 })
		if got := tr.sentTurns(); got[0] != "wait for it" || w.currentAsk() != nil {
			t.Fatalf("sent %q, card %v", got, w.currentAsk())
		}
	})
	t.Run("requirements line and buttons", func(t *testing.T) {
		w := reqBoard(t)
		defer w.Close()
		defer v2Cards.Delete(w)
		h, ok := v2Hit(t, w, hitRequirements, "edit")
		if !ok {
			t.Fatal("no edit button")
		}
		v2Click(t, w, h)
		if !w.currentReq().editing {
			t.Fatal("the edit button did not ask for the line")
		}
		w.setReqEditing(false)
		h, ok = v2Hit(t, w, hitRequirements, "d")
		if !ok {
			t.Fatal("no click region on line d")
		}
		v2Click(t, w, h)
		if got := w.composer.Text(); got != "edit d: go test ./... passes" || !w.say.open.Load() {
			t.Fatalf("a click on line d: composer %q", got)
		}
	})
	t.Run("change keep", func(t *testing.T) {
		w, _ := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
		defer v2Cards.Delete(w)
		hp, _ := parseMarshalProposal(holdProposalLine(`["a"]`, "switch to OAuth"))
		w.applyHold(hp)
		h, ok := v2Hit(t, w, hitChange, "keep")
		if !ok {
			t.Fatal("no keep button")
		}
		v2Click(t, w, h)
		if w.currentChange() != nil || w.marshalSession().isHeldFor("run", "a") {
			t.Fatal("keep did not close the card and release the hold")
		}
	})
	t.Run("next details", func(t *testing.T) {
		w, _, _ := boardWithNativeChat(t)
		defer v2Cards.Delete(w)
		w.verSource = &fakeVerifyView{overview: demoVerifyOverview(time.Now())}
		w.openNext(boardmodel.RunHead{ID: "run", State: marshal.Closed, Done: 1, Total: 1})
		h, ok := v2Hit(t, w, hitNext, "d")
		if !ok {
			t.Fatal("no details button")
		}
		v2Click(t, w, h)
		if !w.verView.Load() {
			t.Fatal("details did not open the Verify view")
		}
	})
	t.Run("guard undo then cancel, keep", func(t *testing.T) {
		w := guardBoard(t, false)
		h, ok := v2Hit(t, w, hitGuard, "guard")
		if !ok || h.action != 'u' {
			t.Fatalf("no undo button: %+v", h)
		}
		v2Click(t, w, h)
		if _, _, armed, _ := w.guardPaint(); !armed {
			t.Fatal("undo did not arm")
		}
		w.frameLines(170, 45)
		var cancel, keep boardHit
		w.boardHits.mu.Lock()
		for _, x := range w.boardHits.hits {
			if x.kind == hitGuard && x.action == 'n' {
				cancel = x
			}
		}
		w.boardHits.mu.Unlock()
		if cancel.kind == "" {
			t.Fatal("no cancel button while armed")
		}
		v2Click(t, w, cancel)
		if _, _, armed, _ := w.guardPaint(); armed {
			t.Fatal("cancel did not disarm")
		}
		w.frameLines(170, 45)
		w.boardHits.mu.Lock()
		for _, x := range w.boardHits.hits {
			if x.kind == hitGuard && x.action == 'K' {
				keep = x
			}
		}
		w.boardHits.mu.Unlock()
		v2Click(t, w, keep)
		if w.guardAlertNow() != nil {
			t.Fatal("keep did not clear the alert")
		}
	})
}

// A pause that needs a new plan is the paused card with that choice: Enter
// opens the composer to tell the Marshal what to change, as the inbox's
// stop entry does, and the card names what the run asks for.
func TestPausedCardUsePlanOffersTheNewPlan(t *testing.T) {
	ran := recordStopCommands(t)
	w := pausedWorkspace(t, "codex", marshal.Pause{Reason: "T03 was cancelled.", Resolutions: []string{"create and approve an amended plan that removes or replaces the cancelled task and updates its dependencies, then /marshal use-plan"}, Code: marshal.CodeTaskCancelled, Action: marshal.PauseUsePlan})
	b := cardsFrame(w)
	z := b.Paused
	if z == nil || z.Kind != "new_plan" || z.Title != "Paused · needs a new plan" || z.Cause != "T03 was cancelled." {
		t.Fatalf("use-plan card %+v", z)
	}
	if !strings.HasPrefix(z.Will, "open the composer to tell the Marshal what to change.\nThe run asks: create and approve an amended plan") || !strings.Contains(z.Will, "/marshal use-plan") {
		t.Fatalf("will %q", z.Will)
	}
	if got := keyNames(z.Actions); !slices.Equal(got, []string{"⏎ tell the Marshal what to change", "x stop the run"}) {
		t.Fatalf("actions %q", got)
	}
	if !w.handleV2CardKey(KeyEvent{Type: KeyEnter}) || !w.say.open.Load() || len(*ran) != 0 {
		t.Fatalf("Enter: composer open %v, ran %q", w.say.open.Load(), *ran)
	}
}
