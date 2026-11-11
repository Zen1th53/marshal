package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// demoPlanOverview is the overview the Plan view goldens paint: the
// prototype's tasks and dependencies (DEPS) and its two plan versions.
func demoPlanOverview(now time.Time) store.PlanOverview {
	task := func(id, title, scope string, st marshal.TaskState, deps ...string) store.PlanTask {
		return store.PlanTask{ID: id, Title: title, State: st, DependsOn: deps, Scope: []string{scope}}
	}
	return store.PlanOverview{
		RunID: "R-7f3a", PlanID: "PLAN-mcp", PlanVersion: 2, RunState: marshal.Dispatching,
		Tasks: []store.PlanTask{
			task("T-009", "events_list pages by sequence", "internal/mcp/events_list.go", marshal.Merged),
			task("T-011", "stable run digest", "internal/mcp/run_digest*.go", marshal.HandedIn, "T-009"),
			task("T-017", "memory recall tool", "internal/mcp/memory_recall.go", marshal.Dispatched, "T-009"),
			task("T-018", "memory candidates", "internal/app/memory_candidates*.go", marshal.Dispatched, "T-009"),
			task("T-019", "memory restore", "internal/store/memory_restore.go", marshal.Dispatched, "T-018"),
			task("T-020", "memory bridge", "internal/app/memory_bridge.go", marshal.HandedIn, "T-017", "T-018"),
			task("T-023", "npm broker", "internal/netpolicy/broker_npm.go", marshal.Escalated),
			task("T-024", "credential lease", "internal/app/credential_lease.go", marshal.Returned, "T-023"),
			task("T-027", "memory review", "internal/tui/memory_review_extreme.go", marshal.Dispatched, "T-020"),
			task("T-031", "release push", "scripts/release/push_tags.sh", marshal.Escalated, "T-011", "T-024"),
			task("T-033", "canonical migrations", "internal/store/migrations_canonical.go", marshal.Queued, "T-019"),
			task("T-034", "memory docs", "docs/mcp-memory.md", marshal.Queued, "T-020", "T-027"),
		},
		Versions: []store.PlanVersion{
			{Version: 2, State: "APPROVED", Supersedes: 1, Reason: "added T-031 release push (hard gate)", At: now.Add(-21 * time.Minute), Current: true},
			{Version: 1, State: "STALE", At: now.Add(-42 * time.Minute)},
		},
	}
}

func demoPlanPaint(now time.Time) planPaint {
	return planPaint{Snap: planSnapshot{Overview: demoPlanOverview(now), Loaded: true, At: now.Add(-4 * time.Second)}, Selected: "T-018"}
}

// fakePlanView is the Plan view's source in tests.
type fakePlanView struct {
	mu       sync.Mutex
	overview store.PlanOverview
	runs     []string
}

func (f *fakePlanView) PlanOverview(_ context.Context, runID string) (store.PlanOverview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, runID)
	return f.overview, nil
}

func (f *fakePlanView) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs)
}

// boardViewVisual is the Board visual the view goldens paint, every view
// available.
func boardViewVisual(now time.Time, th *Theme) boardVisual {
	return boardVisual{board: demoBoard(now), now: now, theme: th, frame: anim.Frame{}, edition: "Marshal", health: map[string]string{"codex": "●", "claude": "●", "agy": "◐", "opencode": "●"}, memory: true, providers: true, verify: true, security: true, plan: true, arch: true}
}

// checkViewGolden compares a view's frame with its golden and, with
// -board-ans, writes it to ../../out/<name>-<size>.ans for the demo PNGs.
func checkViewGolden(t *testing.T, name, size string, frame []string, w int) {
	t.Helper()
	got := strings.Join(frame, "\n") + "\n"
	path := filepath.Join("testdata", "board", name+"-"+size+".golden")
	if *updateBoardGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs; run with -update-board-golden", path)
	}
	if *writeBoardANS {
		out := filepath.Join("..", "..", "out", name+"-"+strings.TrimPrefix(size, "split-")+".ans")
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i, line := range frame {
		if n := VisibleLen(line); n != w {
			t.Errorf("row %d width %d, want %d", i, n, w)
		}
	}
}

func TestPlanViewGoldens(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("BOARD_TRUECOLOR", "")
	now := memEpoch
	th := NewTheme(ThemeDefault, true, true)
	for _, tc := range []struct {
		name string
		w, h int
	}{{"split-120x40", 120, 40}, {"split-80x24", 80, 24}} {
		t.Run(tc.name, func(t *testing.T) {
			v := boardViewVisual(now, th)
			v.planView = true
			l := layout.Compute(tc.w, tc.h, layout.Split)
			frame := paintBodyView(l, th, v, planPane, func(w, h int) []string { return v.planRender(demoPlanPaint(now), w, h) })
			checkViewGolden(t, "plan", tc.name, frame, tc.w)
			plain := StripANSI(strings.Join(frame, "\n"))
			for _, want := range []string{"PLAN", "level 0", "level 3", "T-018", "needs", "unblocks", "VERSIONS", "v2", "approved"} {
				if !strings.Contains(plain, want) {
					t.Errorf("Plan view lacks %q", want)
				}
			}
			if last := StripANSI(frame[len(frame)-1]); !strings.Contains(last, "esc") || !strings.Contains(last, "select") {
				t.Errorf("footer: %s", last)
			}
		})
	}
}

func TestPlanViewContent(t *testing.T) {
	now := memEpoch
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), now: now, board: demoBoard(now)}
	m := demoPlanPaint(now)
	text := strings.Join(v.planLines(m, 120, 38), "\n")
	for _, want := range []string{
		"plan v2 approved 13:41 · 12 tasks",
		"read 4s ago · every 10 s while open",
		"level 0         level 1         level 2         level 3",
		"T-018 memory candidates",
		"needs          T-009",
		"unblocks       T-019, T-020",
		"scope          internal/app/memory_candidates*.go",
		"v2   13:41       approved    · added T-031 release push (hard gate) · the run's plan",
		"v1   13:20       stale       · drafted from the goal",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Plan view lacks %q:\n%s", want, text)
		}
	}
	// The Board's word wins for a task it shows: T-011 is in verification.
	if st := v.planTaskState(m.Snap.Overview.Tasks[1]); st != "verifying" {
		t.Fatalf("T-011 state %q", st)
	}
	g := newPlanGraph(m.Snap.Overview.Tasks)
	if g.Levels != 5 || g.Level["T-034"] != 4 || g.Level["T-031"] != 2 || g.Level["T-023"] != 0 {
		t.Fatalf("levels %d %v", g.Levels, g.Level)
	}
	var order []string
	for _, t := range g.Order {
		order = append(order, t.ID)
	}
	if got := strings.Join(order, " "); got != "T-009 T-023 T-011 T-017 T-018 T-024 T-019 T-020 T-031 T-027 T-033 T-034" {
		t.Fatalf("order %s", got)
	}
	// A cycle does not hang the layout.
	cyc := newPlanGraph([]store.PlanTask{{ID: "a", DependsOn: []string{"b"}}, {ID: "b", DependsOn: []string{"a"}}})
	if len(cyc.Order) != 2 {
		t.Fatalf("cycle order %+v", cyc.Order)
	}
	// Narrow bodies scroll the levels to the selected task's.
	m.Selected = "T-034"
	text = strings.Join(v.planLines(m, 40, 30), "\n")
	if !strings.Contains(text, "level 3") || strings.Contains(text, "level 0 ") {
		t.Errorf("narrow graph does not show the selected level:\n%s", text)
	}
	empty := planPaint{Snap: planSnapshot{Loaded: true, At: now, Overview: store.PlanOverview{RunID: "R-1"}}}
	if text := strings.Join(v.planLines(empty, 100, 20), "\n"); !strings.Contains(text, "the run has no tasks yet") {
		t.Errorf("empty view:\n%s", text)
	}
	lines := v.planRender(demoPlanPaint(now), 100, 30)
	for i, l := range hostASCII(boardASCIIGlyphs(lines), 100) {
		for _, r := range l {
			if r > 127 {
				t.Fatalf("ASCII line %d has %q: %s", i, r, l)
			}
		}
	}
}

// viewsWorkspace is a Board workspace with every view's source faked.
func viewsWorkspace(t *testing.T) (*Workspace, *fakePlanView, *fakeArchView) {
	t.Helper()
	w, _ := securityWorkspace(t)
	p := &fakePlanView{overview: demoPlanOverview(time.Now())}
	a := &fakeArchView{status: demoArchStatus()}
	w.planSource, w.archSource = p, a
	return w, p, a
}

// viewKeys opens view key and checks Esc, 1 and the view's own keys
// return to the Board, and that 2 to 8 switch between the views.
func checkViewKeys(t *testing.T, w *Workspace, open []rune, isOpen func() bool) {
	t.Helper()
	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	key := open[0]
	w.composer.SetText(string(key))
	if press(key) || isOpen() {
		t.Fatalf("%c opened the view while the composer held text", key)
	}
	w.composer.SetText("")
	w.boardUI.inspect = "T-017"
	if press(key) || isOpen() {
		t.Fatalf("%c opened the view over the inspector", key)
	}
	w.boardUI.inspect = ""
	back := []KeyEvent{{Type: KeyEsc}, {Type: KeyRune, Rune: '1'}}
	for _, r := range open {
		back = append(back, KeyEvent{Type: KeyRune, Rune: r})
	}
	for _, o := range open {
		for _, b := range back {
			if !press(o) || !isOpen() {
				t.Fatalf("%c did not open the view", o)
			}
			if !w.handleBoardKey(b) || isOpen() {
				t.Fatalf("%+v did not return to the Board", b)
			}
		}
	}
	others := []struct {
		key  rune
		open func() bool
	}{{'3', w.memView.Load}, {'4', w.provView.Load}, {'5', w.verView.Load}, {'6', w.secView.Load}, {'7', w.planView.Load}, {'b', w.archView.Load}, {'8', w.archView.Load}}
	for _, tc := range others {
		if tc.key == key || strings.ContainsRune(string(open), tc.key) {
			continue
		}
		press(key)
		if !press(tc.key) || isOpen() || !tc.open() {
			t.Fatalf("%c did not switch from the view", tc.key)
		}
		if !press(key) || !isOpen() || tc.open() {
			t.Fatalf("%c did not switch back from view %c", key, tc.key)
		}
		press('1')
	}
	if w.hostWatch.Load() != nil {
		press(key)
		if !press('2') || isOpen() || !w.hostView.Load() {
			t.Fatal("2 did not switch to the Host view")
		}
		if !press(key) || !isOpen() || w.hostView.Load() {
			t.Fatal("the view's key did not switch from the Host view")
		}
		press('1')
	}
}

func TestPlanViewKeys(t *testing.T) {
	w, _, _ := viewsWorkspace(t)
	defer w.Close()
	checkViewKeys(t, w, []rune{'7'}, w.planView.Load)

	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	press('7')
	w.refreshPlan(t.Context())
	selected := func() string { w.plan.mu.Lock(); defer w.plan.mu.Unlock(); return w.plan.selected }
	if selected() != "T-009" {
		t.Fatalf("first selection %q", selected())
	}
	press('j')
	press('j')
	if selected() != "T-011" {
		t.Fatalf("j moved to %q", selected())
	}
	press('k')
	if selected() != "T-023" {
		t.Fatalf("k moved to %q", selected())
	}
	// The hidden Board's inbox gets no keys while the view is open.
	if w.handleBoardInboxKey(t.Context(), KeyEvent{Type: KeyRune, Rune: 'd'}) {
		t.Fatal("the Board's inbox took a key under the Plan view")
	}
	th := NewTheme(ThemeNoColor, true, true)
	frame, _, _ := w.boardFrame(w.state, th, nil, 120, 40)
	if text := strings.Join(frame, "\n"); !strings.Contains(text, "PLAN") || !strings.Contains(text, "T-023 npm broker") {
		t.Fatalf("live frame:\n%s", text)
	}
	press('1')
	w.planSource = nil
	if press('7') || w.planView.Load() {
		t.Fatal("7 opened a Plan view with nothing to read")
	}
}

func TestPlanRefreshOnlyWhileOpen(t *testing.T) {
	old := planRefreshEvery
	planRefreshEvery = 20 * time.Millisecond
	defer func() { planRefreshEvery = old }()
	w, f, _ := viewsWorkspace(t)
	defer w.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.followPlan(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	time.Sleep(100 * time.Millisecond)
	if n := f.calls(); n != 0 {
		t.Fatalf("closed view read %d times", n)
	}
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '7'})
	memWaitFor(t, "a read on open", func() bool { return f.calls() >= 1 })
	memWaitFor(t, "a periodic read", func() bool { return f.calls() >= 3 })
	f.mu.Lock()
	run := f.runs[0]
	f.mu.Unlock()
	if run != "R-7f3a" {
		t.Fatalf("read run %q", run)
	}
	w.handleBoardKey(KeyEvent{Type: KeyEsc})
	time.Sleep(50 * time.Millisecond)
	n := f.calls()
	time.Sleep(150 * time.Millisecond)
	if m := f.calls(); m != n {
		t.Fatalf("closed view kept reading: %d → %d", n, m)
	}
	// Painting does not read: frames come from the last snapshot.
	planRefreshEvery = time.Hour
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '7'})
	memWaitFor(t, "a read on reopen", func() bool { return f.calls() == n+1 })
	th := NewTheme(ThemeNoColor, true, true)
	for range 5 {
		w.boardFrame(w.state, th, nil, 120, 40)
	}
	time.Sleep(50 * time.Millisecond)
	if m := f.calls(); m != n+1 {
		t.Fatalf("frames read the store: %d reads", m-n)
	}
	// Switching to another view stops the reads.
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '6'})
	planRefreshEvery = 20 * time.Millisecond
	w.wakePlan()
	time.Sleep(100 * time.Millisecond)
	if m := f.calls(); m != n+1 {
		t.Fatalf("a closed Plan view read under the Security view: %d", m-n)
	}
}
