//go:build linux

package tui

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

var writeWiredANS = flag.Bool("board-wired-ans", false, "write the wired Board frame to ../../out/wired-120x40.ans")

// fakeDecisions is a store that hands out a run's decisions after a
// sequence and remembers every read.
type fakeDecisions struct {
	mu     sync.Mutex
	events []events.Event
	afters []events.Sequence
}

func (f *fakeDecisions) MarshalDecisionsAfter(_ context.Context, runID string, after events.Sequence) ([]events.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.afters = append(f.afters, after)
	var out []events.Event
	for _, e := range f.events {
		if e.RunID == runID && e.Sequence > after {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeDecisions) add(e events.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.Sequence = events.Sequence(len(f.events) + 1)
	f.events = append(f.events, e)
}

func fakeRun() marshal.Run {
	return marshal.Run{
		State:    marshal.Dispatching,
		PlanID:   "PLAN-7",
		Settings: marshal.Settings{ReworkLimit: 2},
		Tasks: []marshal.Task{
			{PlanTaskID: "T-017", Title: "memory recall", Worker: "codex", State: marshal.Dispatched, Files: []string{"internal/mcp/memory_recall.go"}},
			{PlanTaskID: "T-018", Title: "candidates", Worker: "claude", State: marshal.Dispatched, Files: []string{"internal/app/memory_candidates.go"}},
			{PlanTaskID: "T-020", Title: "bridge", Worker: "codex", State: marshal.HandedIn, Files: []string{"internal/app/memory_bridge.go"}},
			{PlanTaskID: "T-023", Title: "npm broker", Worker: "codex", State: marshal.Escalated, Files: []string{"internal/netpolicy/broker_npm.go"}},
			{PlanTaskID: "T-009", Title: "events list", Worker: "codex", State: marshal.Merged, Files: []string{"internal/mcp/events_list.go"}},
			{PlanTaskID: "T-033", Title: "migrations", State: marshal.Queued, Files: []string{"internal/store/migrations_canonical.go"}},
		},
	}
}

// boardWorkspace opens a workspace on a fake run the way the real path
// does: the run's panel is published, the store's decisions are read
// through the feed, and the workers come from the tracked tmux windows.
func boardWorkspace(t *testing.T) (*Workspace, *fakeDecisions) {
	t.Helper()
	w := NewWorkspace(nil, "project", "session")
	w.screen = NewScreen(nil)
	start := time.Now().Add(-4 * time.Minute)
	src := &fakeDecisions{}
	for _, e := range []events.Event{
		{Type: events.EventTypeMarshalPlanApproved, At: start},
		{Type: events.EventTypeMarshalTaskDispatched, TaskID: "T-017", At: start.Add(10 * time.Second), Data: map[string]any{"worker": "codex"}},
		{Type: events.EventTypeMarshalTaskDispatched, TaskID: "T-018", At: start.Add(20 * time.Second), Data: map[string]any{"worker": "claude"}},
		{Type: events.EventTypeMarshalUsageCharged, TaskID: "T-017", At: start.Add(time.Minute), Data: map[string]any{"charge_phase": "worker", "usage_known": true, "usage_units": float64(12000)}},
		{Type: events.EventTypeMarshalTaskHandedIn, TaskID: "T-020", At: start.Add(2 * time.Minute), Data: map[string]any{"reason": "hand-in with 4 evidence refs"}},
		{Type: events.EventTypeMarshalEscalated, TaskID: "T-023", At: start.Add(3 * time.Minute), Data: map[string]any{"reason": "egress registry.npmjs.org outside manifest"}},
	} {
		e.RunID = "R-7f3a"
		src.add(e)
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["task-T-017"] = &activeTmuxAgent{id: "task-T-017", role: "task", provider: "codex", taskID: "T-017", runID: "R-7f3a", paneID: "%3", state: "working"}
	w.tmuxActiveWins["task-T-018"] = &activeTmuxAgent{id: "task-T-018", role: "task", provider: "claude", taskID: "T-018", runID: "R-7f3a", paneID: "%4", state: "working"}
	w.tmuxMu.Unlock()
	w.permissions.inbox = []permission.Request{{Kind: "network", TaskID: "T-023", Object: "registry.npmjs.org:443", Scope: "one run", Reason: "outside manifest", Who: "Marshal"}}

	p := newMarshalPanel("R-7f3a", "codex", fakeRun(), "running")
	evs, _, _, err := w.boardFeed.read(t.Context(), src, p.RunID)
	if err != nil {
		t.Fatal(err)
	}
	p.Board = w.projectBoard(p, evs)
	w.setMarshalPanel(p)
	return w, src
}

func plainFrame(w *Workspace, cols, rows int) string {
	lines, _, _ := w.frameLines(cols, rows)
	return StripANSI(strings.Join(lines, "\n"))
}

func TestLiveBoardVitalsUsesCachedProviderStatus(t *testing.T) {
	w, _ := boardWorkspace(t)
	now := time.Now()
	w.prov.mu.Lock()
	w.prov.snap = providerSnapshot{Loaded: true, Providers: []store.ProviderStatus{
		{Provider: "codex", VerifiedAt: now},
		{Provider: "claude", VerifiedAt: now, Expired: true},
		{Provider: "agy", VerifiedAt: now, LimitUntil: now.Add(time.Minute)},
	}}
	w.prov.mu.Unlock()
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false), now: now}
	w.mu.RLock()
	state := w.liveStateLocked()
	w.mu.RUnlock()
	w.fillVitals(&v, state)
	if v.health["codex"] != "ready" || v.health["claude"] != "re-verify" || v.health["agy"] != "limit" {
		t.Fatalf("cached provider readiness words: %v", v.health)
	}
	// The vitals row carries each cached provider's glyph.
	line, _ := v.vitalsLine(200)
	plain := StripANSI(line)
	for _, g := range []string{"✳", "⬡", "◆"} {
		if !strings.Contains(plain, g) {
			t.Errorf("vitals row lacks %q: %q", g, plain)
		}
	}
}

func TestBoardIsTheDefaultScreenForARun(t *testing.T) {
	// This test is about the classic Board itself.
	w, _ := boardWorkspace(t)
	lines, _, _ := w.frameLines(120, 40)
	if len(lines) != 40 {
		t.Fatalf("Board frame has %d rows", len(lines))
	}
	for i, line := range lines {
		if n := VisibleLen(line); n != 120 {
			t.Fatalf("row %d is %d cells", i, n)
		}
	}
	got := StripANSI(strings.Join(lines, "\n"))
	for _, want := range []string{"Marshal", "current run", "running", "TASKS", "LANES", "INBOX", "ACTIVITY", "T-017", "codex-1", "claude-1", "registry.npmjs.org:443", "› running"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Board frame lacks %q:\n%s", want, got)
		}
	}
	if Edition() != "Marshal" && strings.Contains(got, "extreme") {
		t.Fatal("the default build's Board names the Marshal")
	}

	resp, err := w.cmd.Handle(t.Context(), "/view classic")
	if err != nil || !strings.Contains(resp, "classic") {
		t.Fatalf("/view classic: %q, %v", resp, err)
	}
	if w.boardVisible() {
		t.Fatal("the Board still takes permission requests in the classic view")
	}
	got = plainFrame(w, 120, 40)
	if strings.Contains(got, "TASKS") || strings.Contains(got, "LANES") {
		t.Fatalf("/view classic still paints the Board:\n%s", got)
	}
	if !strings.Contains(got, "R-7f3a") {
		t.Fatalf("classic screen lost the run:\n%s", got)
	}
	w.permissions.mu.Lock()
	inbox, queued := len(w.permissions.inbox), !w.permissions.queue.Empty()
	w.permissions.mu.Unlock()
	if inbox != 0 || !queued {
		t.Fatal("/view classic left the inbox's request out of the popup queue")
	}

	if resp, err := w.cmd.Handle(t.Context(), "/view board"); err != nil || resp != "View: Board." {
		t.Fatalf("/view board: %q, %v", resp, err)
	}
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "TASKS") {
		t.Fatalf("/view board did not bring the Board back:\n%s", got)
	}
}

// The real path writes one frame for inspection; run with -board-wired-ans.
func TestBoardWiredFrameANS(t *testing.T) {
	if !*writeWiredANS {
		t.Skip("run with -board-wired-ans")
	}
	t.Setenv("BOARD_TRUECOLOR", "1")
	w, _ := boardWorkspace(t)
	w.theme = NewTheme(ThemeDefault, true, true)
	w.boardUI.focus = layout.Tasks
	lines, _, _ := w.frameLines(120, 40)
	out := filepath.Join("..", "..", "out", "wired-120x40.ans")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBoardFollowsTheRunIncrementally(t *testing.T) {
	w, src := boardWorkspace(t)
	select {
	case <-w.repaint:
	default:
	}
	before := w.marshalPanel()
	if before.Board.Run.Tokens.Value != 12000 {
		t.Fatalf("projected tokens %d", before.Board.Run.Tokens.Value)
	}

	// Nothing new: the store is asked only past the last decision, and the
	// Board is neither re-projected nor repainted.
	if w.refreshBoardFrom(t.Context(), src, before) {
		t.Fatal("a refresh with nothing new published")
	}
	if w.marshalPanel() != before {
		t.Fatal("a refresh with nothing new replaced the panel")
	}
	if n := drainRepaints(w, 50*time.Millisecond); n != 0 {
		t.Fatalf("a refresh with nothing new asked for %d repaints", n)
	}

	src.add(events.Event{RunID: "R-7f3a", Type: events.EventTypeMarshalUsageCharged, TaskID: "T-018", At: time.Now(), Data: map[string]any{"charge_phase": "worker", "usage_known": true, "usage_units": float64(3000)}})
	if !w.refreshBoardFrom(t.Context(), src, before) {
		t.Fatal("a new decision was not published")
	}
	after := w.marshalPanel()
	if after.Board.Run.Tokens.Value != 15000 || after.Board.Seq != 7 {
		t.Fatalf("tail: tokens %d seq %d", after.Board.Run.Tokens.Value, after.Board.Seq)
	}
	if before.Board.Run.Tokens.Value != 12000 {
		t.Fatal("Tail wrote under the Board already handed to the painter")
	}
	if n := drainRepaints(w, 50*time.Millisecond); n != 1 {
		t.Fatalf("a new decision asked for %d repaints, want 1", n)
	}
	src.mu.Lock()
	afters := append([]events.Sequence(nil), src.afters...)
	src.mu.Unlock()
	if want := []events.Sequence{0, 6, 6}; len(afters) != len(want) || afters[0] != want[0] || afters[1] != want[1] || afters[2] != want[2] {
		t.Fatalf("store reads after %v, want %v: each read starts at the last decision seen", afters, want)
	}

	// A worker that leaves changes the lanes without any decision.
	w.tmuxMu.Lock()
	delete(w.tmuxActiveWins, "task-T-018")
	w.tmuxMu.Unlock()
	if !w.refreshBoardFrom(t.Context(), src, after) {
		t.Fatal("a worker change was not published")
	}
	if lanes := w.marshalPanel().Board.Lanes; len(lanes) != 1 || lanes[0].Worker != "codex-1" {
		t.Fatalf("lanes after a worker left: %+v", lanes)
	}
}

func TestBoardRefreshesHeaderWhenPauseHoldChanges(t *testing.T) {
	// This test is about the classic Board itself.
	w, src := boardWorkspace(t)
	select {
	case <-w.repaint:
	default:
	}
	before := w.marshalPanel()
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "running") {
		t.Fatalf("initial Board header does not say running:\n%s", got)
	}

	// A user pause changes only the session hold: there are no new decisions
	// and the tracked workers stay the same.
	w.marshalSession().holdDispatch(before.RunID)
	if !w.refreshBoardFrom(t.Context(), src, before) {
		t.Fatal("a changed pause hold was not published")
	}
	paused := w.marshalPanel()
	if !paused.Board.Run.UserPaused {
		t.Fatal("the projected Board did not record the pause hold")
	}
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "paused") {
		t.Fatalf("Board header does not say paused after hold:\n%s", got)
	}
	if n := drainRepaints(w, 50*time.Millisecond); n != 1 {
		t.Fatalf("a pause hold change asked for %d repaints, want 1", n)
	}

	w.marshalSession().releaseDispatch()
	if !w.refreshBoardFrom(t.Context(), src, paused) {
		t.Fatal("a released pause hold was not published")
	}
	if w.marshalPanel().Board.Run.UserPaused {
		t.Fatal("the projected Board kept the released pause hold")
	}
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "running") {
		t.Fatalf("Board header does not say running after release:\n%s", got)
	}
	if n := drainRepaints(w, 50*time.Millisecond); n != 1 {
		t.Fatalf("a released pause hold asked for %d repaints, want 1", n)
	}
}

// The follower polls only while a moving run is on the Board.
func TestBoardFollowerSleepsWhenNothingMoves(t *testing.T) {
	w, _ := boardWorkspace(t)
	if !w.boardMoving() {
		t.Fatal("a dispatching run on the Board is not followed")
	}
	w.setBoardView(true)
	if w.boardMoving() {
		t.Fatal("the classic view still follows the Board")
	}
	w.setBoardView(false)
	p := *w.marshalPanel()
	p.State = marshal.AwaitingUser
	w.setMarshalPanel(&p)
	if w.boardMoving() {
		t.Fatal("a run waiting for the operator is still polled")
	}
}

func TestBoardAnimatesOnlyWhileShown(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.startAnimator()
	defer w.stopAnimator()
	a := w.animator.Load()
	if !a.Running() {
		t.Fatal("a working run on the Board does not animate")
	}
	w.setBoardView(true)
	if a.Running() {
		t.Fatal("the animator kept ticking under the classic view")
	}
	drainRepaints(w, 2*anim.Tick)
	if n := drainRepaints(w, 4*anim.Tick); n != 0 {
		t.Fatalf("%d repaints with the Board hidden", n)
	}
	w.setBoardView(false)
	if !a.Running() {
		t.Fatal("returning to the Board did not resume the animator")
	}
}

// TestBoardTabCyclesPanes: Tab is the next pane and Shift+Tab the one
// before, never a command completion typed into the composer; with text in
// the composer Tab still completes it.
func TestBoardTabCyclesPanes(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.frameLines(120, 40)
	w.boardUI.focus = layout.Tasks
	for _, want := range []layout.PaneID{layout.Lanes, layout.Inbox, layout.Tasks} {
		if !w.handleBoardKey(KeyEvent{Type: KeyTab}) || w.boardUI.pane(true) != want {
			t.Fatalf("Tab: focus %s, want %s", w.boardUI.pane(true), want)
		}
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyShiftTab}) || w.boardUI.pane(true) != layout.Inbox {
		t.Fatalf("Shift+Tab: focus %s", w.boardUI.pane(true))
	}
	if w.composer.Text() != "" {
		t.Fatalf("Tab typed %q into the composer", w.composer.Text())
	}
	w.composer.SetText("/vi")
	if w.handleBoardKey(KeyEvent{Type: KeyTab}) {
		t.Fatal("the Board took Tab while the composer holds text")
	}
}

// TestBoardMultiLineAnswerOpensOverlay: an answer of several lines is
// shown whole over the Board, not as its last line in the footer; a key
// closes it without doing anything else, and a one-line answer stays in
// the footer.
func TestBoardMultiLineAnswerOpensOverlay(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.frameLines(120, 40)
	w.recordCommandResult("/status", "Status\n  Goal: none\n  Approvals: 0 pending", nil)
	got := plainFrame(w, 120, 40)
	if !strings.Contains(got, "ANSWER") || !strings.Contains(got, "Goal: none") {
		t.Fatalf("multi-line answer not shown whole:\n%s", got)
	}
	task := w.boardUI.task
	if !w.handleBoardAnswerKey(KeyEvent{Type: KeyEsc}) {
		t.Fatal("the overlay did not take Esc")
	}
	if got := plainFrame(w, 120, 40); strings.Contains(got, "ANSWER") || w.boardUI.task != task || w.composer.Text() != "" {
		t.Fatalf("Esc did more than close the answer:\n%s", got)
	}
	// Any other key closes it and goes on: the next command keeps its "/".
	w.recordCommandResult("/status", "Status\n  Goal: none", nil)
	if w.handleBoardAnswerKey(KeyEvent{Type: KeyRune, Rune: '/'}) {
		t.Fatal("the overlay swallowed the first key of the next command")
	}
	if got := plainFrame(w, 120, 40); strings.Contains(got, "ANSWER") {
		t.Fatal("typing did not close the answer")
	}
	w.recordCommandResult("/view chat", "Chat view: tmux (already).", nil)
	if got := plainFrame(w, 120, 40); strings.Contains(got, "ANSWER") || !strings.Contains(got, "Chat view: tmux (already).") {
		t.Fatalf("one-line answer not in the footer:\n%s", got)
	}
}

// TestBoardAnswerSurvivesNoteArrivingWithIt: a command that republishes
// the run as it answers keeps its answer on screen.
func TestBoardAnswerSurvivesNoteArrivingWithIt(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.frameLines(120, 40)
	p := *w.marshalPanel()
	p.Note = "Marshal chat started"
	w.setMarshalPanel(&p)
	w.recordCommandResult("/view chat native", "Chat view saved as native, but the native chat is not qualified here; the tmux-docked chat stays.", nil)
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "not qualified here") {
		t.Fatalf("answer lost to the run note:\n%s", got)
	}
}

// TestTypedOutCommandRunsOnFirstEnter: a command typed out in full runs on
// the first Enter even while the completion menu offers it (and others
// that start with it), instead of Enter only writing the same word again.
func TestTypedOutCommandRunsOnFirstEnter(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.composer.SetText("/task")
	w.composer.cursor = len([]rune("/task"))
	w.completions, w.completionOpen, w.completionSelected = []string{"/task", "/tasks"}, true, false
	if !w.completionShouldSubmit() {
		t.Fatal("/task typed out in full does not run on Enter")
	}
	w.composer.SetText("/tas")
	w.composer.cursor = len([]rune("/tas"))
	if w.completionShouldSubmit() {
		t.Fatal("/tas runs instead of completing")
	}
}

// TestBoardRepeatedAnswerShowsAgain: the same answer twice in a row (/tasks
// then /task) is a new answer and is shown, not taken as already seen.
func TestBoardRepeatedAnswerShowsAgain(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.frameLines(120, 40)
	w.recordCommandResult("/tasks", "No tasks in store.", nil)
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "No tasks in store.") {
		t.Fatal("first answer not shown")
	}
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 't'})
	if got := plainFrame(w, 120, 40); strings.Contains(got, "No tasks in store.") {
		t.Fatal("a key did not retire the answer")
	}
	w.recordCommandResult("/task", "No tasks in store.", nil)
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "No tasks in store.") {
		t.Fatal("the repeated answer was taken as already seen")
	}
}
