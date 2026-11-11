package tui

import (
	"context"
	"flag"
	"fmt"
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

func TestBoardKeysMoveFocusSelectionAndInspector(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.frameLines(120, 40)
	if got := w.boardUI.pane(true); got != layout.Inbox {
		t.Fatalf("with a request waiting the Board opens on %s", got)
	}
	key := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	if !key('t') || w.boardUI.pane(true) != layout.Tasks {
		t.Fatal("t did not focus the tasks")
	}
	first := w.boardUI.task
	if !key('j') || w.boardUI.task == first {
		t.Fatal("j did not move down the tasks")
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyUp}) || w.boardUI.task != first {
		t.Fatal("up did not move back")
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) || w.boardUI.inspect != first {
		t.Fatal("Enter did not open the inspector")
	}
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "IDENTITY") || !strings.Contains(got, first) {
		t.Fatalf("inspector not painted:\n%s", got)
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyEsc}) || w.boardUI.inspect != "" {
		t.Fatal("Esc did not close the inspector")
	}
	if got := plainFrame(w, 120, 40); strings.Contains(got, "IDENTITY") {
		t.Fatal("inspector still painted after Esc")
	}
	if !key('l') || w.boardUI.pane(true) != layout.Lanes {
		t.Fatal("l did not focus the lanes")
	}
	lane := w.boardUI.lane
	if !key('j') || w.boardUI.lane == lane {
		t.Fatal("j did not move down the lanes")
	}
	if !key('i') || w.boardUI.pane(true) != layout.Inbox {
		t.Fatal("i did not focus the inbox")
	}
	// Inside the inbox its own keys apply: i inspects the request.
	if key('i') || key('j') || key('a') {
		t.Fatal("the Board took a key the inbox owns")
	}
	// Esc on the tasks is left to the global keys.
	key('t')
	if w.handleBoardKey(KeyEvent{Type: KeyEsc}) {
		t.Fatal("Esc without an inspector was taken from the global keys")
	}
	// Typing belongs to the composer.
	w.composer.SetText("/vi")
	task := w.boardUI.task
	if key('j') || key('t') || w.boardUI.task != task {
		t.Fatal("the Board took a key while the composer held text")
	}
	if got := plainFrame(w, 120, 40); !strings.Contains(got, " /vi") {
		t.Fatalf("composer text not shown in the footer:\n%s", got)
	}
	w.composer.SetText("")
	// The classic view hands every key back.
	w.setBoardView(true)
	if key('j') || key('t') {
		t.Fatal("the classic view still routes keys to the Board")
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

// The live inspector keeps one view across paints: e shows every event,
// Backspace walks up the delegation chain and closes at its top, Enter
// walks back down, Esc closes.
func TestBoardInspectorKeysSurviveRepaints(t *testing.T) {
	w, src := boardWorkspace(t)
	p := w.marshalPanel()
	next := *p
	next.run.Tasks = append([]marshal.Task(nil), p.run.Tasks...)
	next.run.Tasks[1].ParentID = "T-017" // T-018 was delegated from T-017
	evs, _, _, err := w.boardFeed.read(t.Context(), src, p.RunID)
	if err != nil {
		t.Fatal(err)
	}
	next.Board = w.projectBoard(&next, evs)
	w.setMarshalPanel(&next)
	key := func(e KeyEvent) bool { return w.handleBoardKey(e) }
	inspected := func() string {
		if w.boardUI.insp == nil {
			return ""
		}
		return w.boardUI.insp.TaskID()
	}

	w.frameLines(120, 40)
	key(KeyEvent{Type: KeyRune, Rune: 't'})
	w.boardUI.task = "T-017"
	if !key(KeyEvent{Type: KeyEnter}) || w.boardUI.inspect != "T-017" {
		t.Fatal("Enter did not open the inspector")
	}
	if got := plainFrame(w, 120, 40); strings.Contains(got, "usage.charged") || !strings.Contains(got, "e all events · Enter T-018 · Esc close") {
		t.Fatalf("inspector before e:\n%s", got)
	}
	if !key(KeyEvent{Type: KeyRune, Rune: 'e'}) {
		t.Fatal("e was not taken by the inspector")
	}
	// Two paints: the toggle is the view's state, not one frame's.
	plainFrame(w, 120, 40)
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "usage.charged") || !strings.Contains(got, "e state changes") {
		t.Fatalf("e did not show all events across paints:\n%s", got)
	}
	if !key(KeyEvent{Type: KeyEnter}) || inspected() != "T-018" {
		t.Fatalf("Enter did not walk down to T-018: %q", inspected())
	}
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "T-018  candidates") || !strings.Contains(got, "Backspace T-017") {
		t.Fatalf("inspector on the child:\n%s", got)
	}
	if !key(KeyEvent{Type: KeyBackspace}) || inspected() != "T-017" || w.boardUI.inspect == "" {
		t.Fatalf("Backspace did not walk up to T-017: %q", inspected())
	}
	plainFrame(w, 120, 40)
	if !key(KeyEvent{Type: KeyBackspace}) || w.boardUI.inspect != "" {
		t.Fatal("Backspace at the top of the chain did not close the inspector")
	}
	key(KeyEvent{Type: KeyEnter})
	if !key(KeyEvent{Type: KeyEsc}) || w.boardUI.inspect != "" {
		t.Fatal("Esc did not close the inspector")
	}
	// Reopening starts fresh: state changes only.
	key(KeyEvent{Type: KeyEnter})
	if got := plainFrame(w, 120, 40); strings.Contains(got, "usage.charged") {
		t.Fatalf("a reopened inspector kept all events:\n%s", got)
	}
}

// Enter on a lane shows its worker's tmux pane; without tmux it inspects
// the lane's task.
func TestBoardLaneEnterShowsWorkerPane(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	oldIn, oldFocus := boardInTmux, boardFocusPane
	defer func() { boardInTmux, boardFocusPane = oldIn, oldFocus }()
	var mu sync.Mutex
	var focused []string
	boardFocusPane = func(_ context.Context, pane string, joined bool) error {
		mu.Lock()
		defer mu.Unlock()
		focused = append(focused, fmt.Sprintf("%s %t", pane, joined))
		return nil
	}
	answer := func() string { w.mu.RLock(); defer w.mu.RUnlock(); return w.state.LastOutput }
	w.frameLines(120, 40)
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'l'})
	lane := w.boardUI.lane
	var pane, task string
	for _, l := range w.marshalPanel().Board.Lanes {
		if l.Worker == lane {
			pane, task = l.Pane, l.TaskID
		}
	}
	if pane == "" || task == "" {
		t.Fatalf("lane %q has no pane or task", lane)
	}

	boardInTmux = func(*Workspace) bool { return true }
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter on a lane was not taken")
	}
	memWaitFor(t, "the pane focus", func() bool { mu.Lock(); defer mu.Unlock(); return len(focused) == 1 })
	if focused[0] != pane+" false" || w.boardUI.inspect != "" {
		t.Fatalf("focused %v, inspect %q", focused, w.boardUI.inspect)
	}
	memWaitFor(t, "the answer", func() bool { return strings.HasPrefix(answer(), "View: showing "+lane) })

	boardInTmux = func(*Workspace) bool { return false }
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) || w.boardUI.inspect != task {
		t.Fatalf("Enter without tmux did not inspect %s: %q", task, w.boardUI.inspect)
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

// TestBoardInspectorQCloses: q closes the inspector, as its mockup's bar
// says, instead of starting a command behind it.
func TestBoardInspectorQCloses(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.frameLines(120, 40)
	w.boardUI.focus = layout.Tasks
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) || w.boardUI.inspect == "" {
		t.Fatal("Enter did not open the inspector")
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'q'}) || w.boardUI.inspect != "" {
		t.Fatal("q did not close the inspector")
	}
	if w.composer.Text() != "" {
		t.Fatalf("q went to the composer: %q", w.composer.Text())
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

// TestBoardInspectorDecisionKeys: in the inspector a, A, d and r open the
// task's waiting decision's confirmation in the inbox, never deciding it;
// on a task with nothing waiting they answer so and type nothing.
func TestBoardInspectorDecisionKeys(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.frameLines(120, 40)
	w.boardUI.focus = layout.Tasks
	w.boardUI.task, w.boardUI.inspect = "T-023", "T-023"
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'd'}) {
		t.Fatal("d in the inspector was not taken")
	}
	if w.boardUI.inspect != "" || w.boardUI.pane(true) != layout.Inbox || !w.inboxView.Confirming() || !strings.HasPrefix(w.inboxView.Prompt(), "Enter to confirm d") {
		t.Fatalf("d: inspect %q focus %s prompt %q", w.boardUI.inspect, w.boardUI.pane(true), w.inboxView.Prompt())
	}
	if n := len(w.boardInbox()); n != 1 {
		t.Fatal("d decided the request")
	}
	w.inboxView.Cancel()
	w.boardUI.focus = layout.Tasks
	w.boardUI.task, w.boardUI.inspect = "T-017", "T-017"
	for _, r := range "aAro" {
		if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) || w.boardUI.inspect != "T-017" || w.composer.Text() != "" {
			t.Fatalf("%c on a task with nothing waiting: inspect %q composer %q", r, w.boardUI.inspect, w.composer.Text())
		}
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
