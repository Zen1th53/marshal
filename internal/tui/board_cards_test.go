package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// confirmedReqCard is fullReqCard sent back after the owner's "confirm all":
// the same lines, the sources now the owner's confirmation.
const confirmedReqCard = `[` +
	`{"line":"a","text":"Add login with email; out: OAuth","source":"Owner confirmed all"},` +
	`{"line":"b","text":"migrations/, main branch history","source":"Owner confirmed line b"},` +
	`{"line":"c","text":"from main, lands on main","source":"Owner confirmed line c"},` +
	`{"line":"d","text":"go test ./... passes","source":"Owner confirmed line d"},` +
	`{"line":"e","text":"go vet ./...","source":"Owner confirmed line e"},` +
	`{"line":"f","text":"internal/auth API","source":"Owner confirmed line f"},` +
	`{"line":"g","text":"no new network deps","source":"Owner confirmed line g"},` +
	`{"line":"h","text":"two hours","source":"Owner confirmed line h"}` +
	`]`

func openReqCard(t *testing.T, w *Workspace, card string) {
	t.Helper()
	p, err := parseMarshalProposal(reqProposalLine(card))
	if err != nil {
		t.Fatal(err)
	}
	w.openRequirements(p)
}

// After "confirm all" the Marshal sends the same card back with the sources
// updated (real-user run, 09:38:29). It is recorded, not reopened, so a later
// Enter (the stopped run's "try again", 09:47:31) never sends "confirm all"
// again. A card whose lines changed is a new card and opens.
func TestConfirmedRequirementsCardNeverReopens(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	openReqCard(t, w, fullReqCard)
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken by the card")
	}
	chatWaitFor(t, "the confirmation", func() bool { return len(tr.sentTurns()) == 1 })
	openReqCard(t, w, confirmedReqCard)
	if w.currentReq() != nil {
		t.Fatal("the confirmed card opened again")
	}
	w.handleBoardKey(KeyEvent{Type: KeyEnter})
	time.Sleep(50 * time.Millisecond)
	if got := tr.sentTurns(); len(got) != 1 {
		t.Fatalf("a later Enter sent %q", got)
	}
	found := false
	for _, n := range w.noteList() {
		found = found || n.Text == "Marshal recorded the requirements you confirmed."
	}
	if !found {
		t.Fatalf("Activity does not record the confirmed card: %+v", w.noteList())
	}
	changed := strings.Replace(confirmedReqCard, "two hours", "one hour", 1)
	openReqCard(t, w, changed)
	if w.currentReq() == nil {
		t.Fatal("a card with a changed line did not open")
	}
}

// A stopped run leads the inbox, so Enter is its "try again": no open card
// below it takes Enter, and their buttons do not claim the key.
func TestStoppedRunTakesEnterBeforeOpenCards(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	w.mu.Lock()
	w.state.Marshal = &MarshalPanel{RunID: "run", Note: "stopped: worker exited 2", Stopped: true}
	w.mu.Unlock()
	openReqCard(t, w, fullReqCard)
	ap, err := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
	if err != nil {
		t.Fatal(err)
	}
	w.openAsk(ap)
	if !w.boardStopWaiting() {
		t.Fatal("the stopped run does not wait for Enter")
	}
	text := frameText(w, 160, 50)
	if strings.Contains(text, "[Enter] confirm all") || strings.Contains(text, "[Enter] build on it") {
		t.Fatalf("a card below the stop claims Enter:\n%s", text)
	}
	if !strings.Contains(text, "[click] build on it") || !strings.Contains(text, "[Enter] try again") {
		t.Fatalf("the stop and the cards' buttons:\n%s", text)
	}
	stopped := boardVisual{theme: NewTheme(ThemeNoColor, true, true), stopped: true, note: "stopped: worker exited 2"}
	if got := StripANSI(strings.Join(stopped.requirementsEntry(*w.currentReq(), 0, 0, 120, 40), "\n")); !strings.Contains(got, "[click] confirm all") || strings.Contains(got, "[Enter]") {
		t.Fatalf("the requirements card under the stop:\n%s", got)
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken")
	}
	if !w.boardUI.stopArmed {
		t.Fatal("Enter did not go to the stopped run")
	}
	time.Sleep(50 * time.Millisecond)
	if got := tr.sentTurns(); len(got) != 0 {
		t.Fatalf("Enter answered a card below the stop: %q", got)
	}
	if w.currentReq() == nil || w.currentAsk() == nil {
		t.Fatal("a card closed")
	}
}

// guardAskBoard is a Board with an open chat, a changed-files alert and the
// Marshal's question about the same files (real-user run, 09:36:21).
func guardAskBoard(t *testing.T) (*Workspace, *fakeChatTransport, *guardAlert, *[]string) {
	t.Helper()
	w, _, tr := boardWithNativeChat(t)
	a := &guardAlert{at: time.Now(), paths: []string{"calc.py", "test_calc.py"}, wasDirty: map[string]bool{}}
	w.guard.alert = a
	w.guard.last = guardSnapshot{top: t.TempDir(), files: guardFiles("calc.py", " M", "1", "test_calc.py", "??", "2")}
	var told []string
	w.guardTell = func(text string) { told = append(told, text) }
	ap, err := parseMarshalProposal(askProposalLine(
		"Keep the existing calculator changes as input to the governed work, or undo them through a worker task?",
		`[{"key":"enter","label":"Keep and build on the changes"},{"key":"u","label":"Undo through a worker task"}]`))
	if err != nil {
		t.Fatal(err)
	}
	w.openAsk(ap)
	if c := w.currentAsk(); c == nil || c.guard != a {
		t.Fatal("the question was not tied to the changed files")
	}
	return w, tr, a, &told
}

// Answering the Marshal's question with keep keeps the files: the
// files-changed card closes with it and the header no longer counts it.
func TestAskKeepAnswerResolvesChangedFiles(t *testing.T) {
	w, tr, _, _ := guardAskBoard(t)
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken")
	}
	chatWaitFor(t, "the answer", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns()[0]; got != "Keep and build on the changes" {
		t.Fatalf("sent %q", got)
	}
	if a, _, _, _ := w.guardPaint(); a != nil {
		t.Fatal("the files-changed card stayed after the keep answer")
	}
	if w.currentAsk() != nil {
		t.Fatal("the question stayed")
	}
	if text := frameText(w, 160, 40); strings.Contains(text, "⚑ files changed") || strings.Contains(text, "[K] keep") {
		t.Fatalf("the files-changed card is still drawn:\n%s", text)
	}
}

// K on the files-changed card answers the Marshal's open question about them
// with its keep choice, so neither card outlives the decision.
func TestGuardKeepAnswersTheMarshalsQuestion(t *testing.T) {
	w, tr, _, _ := guardAskBoard(t)
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'K'}) {
		t.Fatal("K was not taken")
	}
	chatWaitFor(t, "the answer", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns()[0]; got != "Keep and build on the changes" {
		t.Fatalf("sent %q", got)
	}
	if w.currentAsk() != nil {
		t.Fatal("the question stayed after K")
	}
	if a, _, _, _ := w.guardPaint(); a != nil {
		t.Fatal("the alert stayed after K")
	}
}

// An undo answer leaves the files-changed card open, since nothing is undone
// yet, and the card says what was answered. The card's own undo then closes
// nothing twice: the question is already answered.
func TestAskUndoAnswerUpdatesChangedFiles(t *testing.T) {
	w, tr, a, _ := guardAskBoard(t)
	// A click on the choice: the u key is the card's own undo, which the
	// guard takes first.
	w.answerAsk(1)
	chatWaitFor(t, "the answer", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns()[0]; got != "Undo through a worker task" {
		t.Fatalf("sent %q", got)
	}
	got, _, _, _ := w.guardPaint()
	if got == nil || got.answered != "Undo through a worker task" {
		t.Fatalf("the alert was not updated: %+v", got)
	}
	if text := frameText(w, 200, 40); !strings.Contains(text, "you answered “Undo through a worker task”; nothing is undone yet") {
		t.Fatalf("the card does not say what was answered:\n%s", text)
	}
	if w.guardAlertNow() != a {
		t.Fatal("the alert was replaced")
	}
}

// The card's undo answers an open question about the same files: the
// question closes and the Marshal is told what was undone.
func TestGuardUndoClosesTheMarshalsQuestion(t *testing.T) {
	w, _, _, told := guardAskBoard(t)
	top := w.guard.last.top
	if err := os.WriteFile(filepath.Join(top, "test_calc.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.guard.alert.paths = []string{"test_calc.py"}
	w.guard.last.files = guardFiles("test_calc.py", "??", "1")
	w.guardSource = func(context.Context) (guardSnapshot, error) {
		return guardSnapshot{top: top, files: guardFiles("test_calc.py", "??", "1")}, nil
	}
	w.guardArm()
	w.guardUndoNow()
	chatWaitFor(t, "the undo", func() bool { a, _, _, _ := w.guardPaint(); return a == nil && w.currentAsk() == nil })
	chatWaitFor(t, "the notice", func() bool { return len(*told) == 1 })
	if !strings.Contains((*told)[0], "removed test_calc.py") {
		t.Fatalf("the Marshal was told %q", *told)
	}
}

// A question about something else is not tied to the changed files.
func TestAskAboutOtherThingsLeavesChangedFiles(t *testing.T) {
	if askAboutGuard(exampleAskQuestion, []askChoice{{"enter", "build on it"}, {"l", "leave it"}}) {
		t.Fatal("a branch question was tied to the changed files")
	}
	if !askAboutGuard("Keep the changes to calc.py?", []askChoice{{"enter", "Keep them"}, {"u", "Undo them"}}) {
		t.Fatal("a keep/undo question about changes was not tied")
	}
}

// y on the architecture gate shows "Opening…" at once, and once the browser
// was asked it says the page is open and where its link is, instead of
// clearing to nothing. Activity names the address without its key.
func TestArchitectureOpenGivesFeedback(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-architecture-feedback", false)
	defer w.Close()
	m := w.marshalSession()
	release := make(chan struct{})
	m.architectureStart = func(context.Context, string, string) (*app.ArchitectureReviewSession, error) {
		<-release
		return &app.ArchitectureReviewSession{URL: "http://127.0.0.1:12345/?token=fixture-secret-key", Done: make(chan struct{})}, nil
	}
	m.architectureBrowser = func(context.Context, string) error { return nil }
	w.openArchitectureReview()
	if w.answer != architectureOpening {
		t.Fatalf("no immediate answer: %q", w.answer)
	}
	close(release)
	chatWaitFor(t, "the opened answer", func() bool {
		w.mu.RLock()
		defer w.mu.RUnlock()
		return w.answer == architectureOpened
	})
	if !strings.Contains(architectureOpened, filepath.Join(".marshal", "architecture", "review", "url.txt")) {
		t.Fatalf("the answer does not say where the link is: %q", architectureOpened)
	}
	assertArchitectureNotes(t, w, "Architecture view opened in your browser: http://127.0.0.1:12345/")
}

// A browser that cannot be opened says so in plain words and points at the
// saved link; the key never reaches Activity.
func TestArchitectureOpenWithoutBrowserPointsAtLink(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-architecture-nobrowser", false)
	defer w.Close()
	m := w.marshalSession()
	m.architectureStart = func(context.Context, string, string) (*app.ArchitectureReviewSession, error) {
		return &app.ArchitectureReviewSession{URL: "http://127.0.0.1:12345/?token=fixture-secret-key", Done: make(chan struct{})}, nil
	}
	m.architectureBrowser = func(context.Context, string) error { return errors.New("open browser: exit status 3") }
	w.openArchitectureReview()
	chatWaitFor(t, "the no-browser answer", func() bool {
		w.mu.RLock()
		defer w.mu.RUnlock()
		return w.answer == architectureNoBrowser && w.answerIsError
	})
	assertArchitectureNotes(t, w, "Architecture view: your browser could not be opened. Open the link in "+architectureURLFile)
	// Pressing y again reuses the page and says the same.
	w.openArchitectureReview()
	chatWaitFor(t, "the second answer", func() bool {
		w.mu.RLock()
		defer w.mu.RUnlock()
		return w.answer == architectureNoBrowser
	})
}

func assertArchitectureNotes(t *testing.T, w *Workspace, want string) {
	t.Helper()
	found := false
	for _, n := range w.noteList() {
		if strings.Contains(n.Text, "token") || strings.Contains(n.Text, "fixture-secret-key") || strings.Contains(n.Text, "REDACTED") {
			t.Fatalf("Activity carries the key: %q", n.Text)
		}
		found = found || strings.HasPrefix(n.Text, want)
	}
	if !found {
		t.Fatalf("Activity misses %q: %+v", want, w.noteList())
	}
}

// The only task in review with nothing waiting for the owner: the header says
// "reviewing", not "waiting for you" (real-user run, 09:41:58), also with a
// stale requirements card open during the run.
func TestHeaderSaysReviewing(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, true)
	review := []model.TaskRow{{ID: "subtract-test", Worker: "codex", State: model.TaskReview}}
	req := &reqCard{}
	for _, c := range []struct {
		name string
		v    boardVisual
		want string
	}{
		{"dispatching, only a review", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Dispatching, Total: 1, Running: 1}, Tasks: review}}, "reviewing"},
		{"reviewing state", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Reviewing}, Tasks: review}}, "reviewing"},
		{"requirements card during the run", boardVisual{theme: th, req: req, board: model.Board{Run: model.RunHead{State: marshal.Dispatching}, Tasks: review}}, "reviewing"},
		{"requirements card while planning", boardVisual{theme: th, req: req, board: model.Board{Run: model.RunHead{State: marshal.Drafting}}}, "waiting for you"},
		{"a worker still working", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Dispatching}, Tasks: append(review, model.TaskRow{ID: "b", State: model.TaskWorking})}}, "running"},
	} {
		if got := StripANSI(c.v.stateChip()); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The picker's reading is the vitals row's at once: right after "codex 33%
// left" the row never says "codex limit unknown".
func TestPickerReadingFeedsTheVitalsRow(t *testing.T) {
	installPickerStubs(t, "codex", "claude")
	reset := time.Now().Add(3 * time.Hour)
	w := pickerWorkspace(t, quotaLeft("codex", 33, reset), quotaUnknown("claude"))
	if _, read := w.usageQuotas(); read {
		t.Fatal("a reading before the picker")
	}
	cands := w.marshalCandidates(t.Context())
	if len(cands) == 0 || !cands[0].known || cands[0].left != 33 {
		t.Fatalf("picker candidates %+v", cands)
	}
	qs, read := w.usageQuotas()
	if !read {
		t.Fatal("the picker's reading was not kept for the vitals row")
	}
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), now: time.Now()}
	v.vitals.quotas, v.vitals.read = qs, read
	v.vitals.installed = map[string]bool{"codex": true, "claude": true}
	line, _ := v.vitalsLine(200)
	line = StripANSI(line)
	if strings.Contains(line, "codex limit unknown") || !strings.Contains(line, "33% left") {
		t.Fatalf("vitals row: %q", line)
	}
}
