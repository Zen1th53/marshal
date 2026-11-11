package tui

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// demoArchOverview is the overview the Architecture view goldens paint:
// the prototype's accepted revision 7.
func demoArchOverview() store.ArchitectureOverview {
	return store.ArchitectureOverview{
		ProjectID: "project",
		Accepted: &store.ArchitectureAccepted{Record: store.ArchitectureRecordContract, ID: "CONTRACT-1", Revision: 7, Digest: "a3f1c9e2b7d04411", By: "owner", Instruction: "accept architecture",
			At: time.Date(2026, 11, 2, 13, 38, 0, 0, time.UTC)},
		Comparable: true, Current: 7,
	}
}

// demoArchPaint is what the goldens paint; demoArchRules comes from the claims test file.
func demoArchPaint(now time.Time) archPaint {
	return archPaint{Snap: archSnapshot{Overview: demoArchOverview(), Loaded: true, At: now.Add(-4 * time.Second)}, Edition: "Marshal 0.0.9", Rules: demoArchRules}
}

// fakeArchView is the Architecture view's source in tests: its pages
// stand for the file and the browser.
type fakeArchView struct {
	mu       sync.Mutex
	overview store.ArchitectureOverview
	reads    []string
	pages    map[string]string
	openErr  error
}

func (f *fakeArchView) ArchitectureOverview(_ context.Context, projectID string) (store.ArchitectureOverview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, projectID)
	return f.overview, nil
}

func (f *fakeArchView) OpenPage(_ context.Context, name string, page []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pages == nil {
		f.pages = map[string]string{}
	}
	f.pages[name] = string(page)
	return "/state/architecture/" + name, f.openErr
}

func (f *fakeArchView) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reads)
}

func TestArchViewGoldens(t *testing.T) {
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
			v.archView = true
			l := layout.Compute(tc.w, tc.h, layout.Split)
			frame := paintBodyView(l, th, v, archPane, func(w, h int) []string { return v.archRender(demoArchPaint(now), w, h) })
			checkViewGolden(t, archGoldenName, tc.name, frame, tc.w)
			plain := StripANSI(strings.Join(frame, "\n"))
			for _, want := range []string{"ARCHITECTURE", "contract rev 7", "a3f1c9e2", "owner 2026-11-02 13:38", "Marshal 0.0.9", "BUSINESS PROCESS", "now: waiting on owner", "▸ " + bpD3, "[o]", "[d]", "COMPONENTS", "RULES IN FORCE"} {
				if !strings.Contains(plain, want) {
					t.Errorf("Architecture view lacks %q", want)
				}
			}
			// Every component and rule fits, at 80x24 too.
			for _, c := range archComponents {
				if !strings.Contains(plain, c[0]) {
					t.Errorf("Architecture view lacks component %q", c[0])
				}
			}
			for _, r := range demoArchRules {
				if !strings.Contains(plain, r) {
					t.Errorf("Architecture view lacks rule %q", r)
				}
			}
			if tc.w >= 100 {
				for _, want := range []string{"accepted by owner 2026-11-02 13:38", bpU5, "independent tasks run and are checked in parallel", archComponents[1][1]} {
					if !strings.Contains(plain, want) {
						t.Errorf("wide Architecture view lacks %q", want)
					}
				}
			}
			if last := StripANSI(frame[len(frame)-1]); !strings.Contains(last, "open in browser") || !strings.Contains(last, "esc") {
				t.Errorf("footer: %s", last)
			}
		})
	}
}

// bpNow derives the step from the run's recorded state, not from the task
// counts alone, and flags a step that is not live.
func TestBusinessProcessNow(t *testing.T) {
	now := memEpoch
	b := demoBoard(now)
	if cur, why := bpNow(b, now); cur != "D3" || why != "waiting on owner: 2 decisions in the inbox" {
		t.Fatalf("inbox: %s %s", cur, why)
	}
	b.Inbox = nil
	if cur, why := bpNow(b, now); cur != "M5" || why != "dispatching · 4 working · 2 queued · 2 in check" {
		t.Fatalf("working: %s %s", cur, why)
	}
	with := func(state marshal.RunState, states ...model.TaskState) model.Board {
		c := b
		c.Run.State = state
		c.Tasks = nil
		for i, s := range states {
			c.Tasks = append(c.Tasks, model.TaskRow{ID: string(rune('a' + i)), State: s})
		}
		return c
	}
	for _, tc := range []struct {
		b         model.Board
		step, why string
	}{
		{with(marshal.Dispatching, model.TaskQueued, model.TaskMerged), "M5", "dispatching · 0 working · 1 queued · 0 in check"},
		{with(marshal.Dispatching, model.TaskReview, "verifying", model.TaskMerged), "M6", "dispatching · 2 in check"},
		// Nothing in flight no longer means "assembling the report": the
		// run is still dispatching until the runtime moves it on.
		{with(marshal.Dispatching, model.TaskMerged, model.TaskParked), "M5", "dispatching · 0 working · 0 queued · 0 in check"},
		{with(marshal.Reviewing, model.TaskReview), "M6", "reviewing · 0 working · 0 queued · 1 in check"},
		{with(marshal.Merging, model.TaskMerged), "M7", "merging accepted results"},
		{with(marshal.Verifying, model.TaskMerged), "M7", "verifying the assembled result"},
		{with(marshal.AwaitingUser, model.TaskWorking), "D3", "waiting on owner"},
		{with(marshal.Closed, model.TaskMerged), "U5", "run closed"},
		{with(marshal.Drafting), "M4", "drafting the plan"},
		{with(marshal.Drafting, model.TaskQueued, model.TaskQueued), "D1", "plan of 2 tasks awaiting approval"},
		{with(marshal.Approved, model.TaskQueued), "M5", "plan approved · dispatch starting"},
		// An empty Board is no run at all, not a finished one (it used to read M7).
		{model.Board{}, "U1", "no run yet: waiting for the owner's goal"},
	} {
		if cur, why := bpNow(tc.b, now); cur != tc.step || why != tc.why {
			t.Errorf("%s: got %s %q, want %s %q", tc.b.Run.State, cur, why, tc.step, tc.why)
		}
	}
}

// A run that stopped producing events, a run restored from its snapshot and
// a Board whose store could not be read are labelled, never shown as live.
func TestBusinessProcessFlagsStaleAndRecoveredRuns(t *testing.T) {
	now := memEpoch
	b := demoBoard(now)
	b.Inbox = nil
	if p := bpAt(b, now); p.Flag != "" {
		t.Fatalf("live demo run flagged %q", p.Flag)
	}
	if p := bpAt(b, now.Add(3*time.Hour)); p.Step != "M5" || p.Flag != "stale" || !strings.HasPrefix(p.Why, "no run event for 3h") {
		t.Fatalf("silent run: %+v", p)
	}
	stale := b
	stale.Stale = true
	if cur, why := bpNow(stale, now); cur != "M5" || !strings.HasPrefix(why, "no recent update: the store could not be read") {
		t.Fatalf("unreadable store: %s %s", cur, why)
	}
	recovered := b
	recovered.Activity, recovered.Seq = nil, 0
	if p := bpAt(recovered, now); p.Flag != "recovered" {
		t.Fatalf("restored run: %+v", p)
	}
	if cur, why := bpNow(recovered, now); cur != "M5" || !strings.HasPrefix(why, "restored after a restart: ") {
		t.Fatalf("recovered run bpNow: %s %s", cur, why)
	}
	// A closed run is history, never stale.
	closed := b
	closed.Run.State = marshal.Closed
	if p := bpAt(closed, now.Add(72*time.Hour)); p.Step != "U5" || p.Flag != "" {
		t.Fatalf("closed run: %+v", p)
	}
}

func TestBusinessProcessStepsComplete(t *testing.T) {
	for _, id := range []string{"M1", "L1", "U1", "S1", "M2", "U2", "M3", "M4", "D1", "M5", "A1", "A2", "M6", "D2", "D3", "D4", "M7", "U5"} {
		if !strings.Contains(fmtSteps(), id+" ") {
			t.Fatalf("step %s missing", id)
		}
	}
}

func fmtSteps() string {
	var b strings.Builder
	for _, s := range businessProcess {
		b.WriteString(s.ID + " " + s.Text + "\n")
	}
	return b.String()
}

func TestArchViewContent(t *testing.T) {
	now := memEpoch
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), now: now, board: demoBoard(now)}
	m := demoArchPaint(now)
	text := strings.Join(v.archLines(m, 120, 38), "\n")
	for _, want := range []string{
		"ARCHITECTURE contract rev 7 · a3f1c9e2 · accepted by owner 2026-11-02 13:38 · Marshal 0.0.9",
		"in force       rev 7 accepted · Marshal acts only inside it",
		"▸ " + bpD3,
		"✓ runtime dispatches approved tasks",
		"· more tasks? → hand out the next ones",
		"Memory / MCP",
		"● hard gates need the typed canonical id",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Architecture view lacks %q:\n%s", want, text)
		}
	}
	// Narrow and short bodies keep the current step in view.
	text = strings.Join(v.archLines(m, 80, 14), "\n")
	if !strings.Contains(text, "▸ "+bpD3[:12]) {
		t.Errorf("short view lost the current step:\n%s", text)
	}
	none := archPaint{Snap: archSnapshot{Loaded: true, At: now, Overview: store.ArchitectureOverview{ProjectID: "p"}}, Edition: "Community dev"}
	text = strings.Join(v.archLines(none, 120, 38), "\n")
	if !strings.Contains(text, "no accepted architecture · Community dev") || !strings.Contains(text, "nothing accepted yet") {
		t.Errorf("no acceptance:\n%s", text)
	}

	// d's answers.
	o := demoArchOverview()
	if got := archDiffAnswer(o); got != "rev 7 unchanged since accepted (digest a3f1c9e2)." {
		t.Errorf("unchanged: %q", got)
	}
	o.Current, o.Changes = 8, []string{"components", "rules"}
	if got := archDiffAnswer(o); got != `rev 8 changed since rev 7 was accepted: components, rules. It waits for "accept architecture".` {
		t.Errorf("changed: %q", got)
	}
	o.Comparable = false
	if got := archDiffAnswer(o); !strings.Contains(got, "cannot tell") {
		t.Errorf("not comparable: %q", got)
	}
	if got := archDiffAnswer(store.ArchitectureOverview{}); !strings.Contains(got, "No accepted architecture") {
		t.Errorf("none: %q", got)
	}
	for i, l := range v.archRender(m, 100, 30) {
		for _, r := range hostASCII(boardASCIIGlyphs([]string{l}), 100)[0] {
			if r > 127 && !v.unicode() {
				t.Fatalf("ASCII line %d has %q", i, r)
			}
		}
	}
	ascii := boardVisual{theme: NewTheme(ThemeNoColor, false, false), now: now, board: demoBoard(now)}
	for i, l := range ascii.archRender(m, 100, 30) {
		for _, r := range l {
			if r > 127 {
				t.Fatalf("ASCII line %d has %q: %s", i, r, l)
			}
		}
	}
}

func TestArchViewKeys(t *testing.T) {
	w, _, _ := viewsWorkspace(t)
	defer w.Close()
	checkViewKeys(t, w, []rune{'b', '8'}, w.archView.Load)
	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	press('b')
	// Without a test source the view reads the workspace store; it is
	// never source-less, so the header chip and the key stay.
	press('1')
	w.archSource = nil
	if w.archSrc() == nil || !press('b') || !w.archView.Load() {
		t.Fatal("b did not open the Architecture view on the workspace store")
	}
}

// o writes the page through the source and names where it is; d answers
// from the last read.
func TestArchOpenAndDiffKeys(t *testing.T) {
	w, _, f := viewsWorkspace(t)
	defer w.Close()
	answer := func() string { w.mu.RLock(); defer w.mu.RUnlock(); return w.state.LastOutput }
	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	press('b')
	if !press('d') || answer() != "The architecture has not been read yet." {
		t.Fatalf("d before a read: %q", answer())
	}
	w.refreshArch(t.Context())
	press('d')
	if a := answer(); a != "rev 7 unchanged since accepted (digest a3f1c9e2)." {
		t.Fatalf("d: %q", a)
	}
	f.mu.Lock()
	f.overview.Current, f.overview.Changes = 8, []string{"components"}
	f.mu.Unlock()
	w.refreshArch(t.Context())
	press('d')
	if a := answer(); !strings.HasPrefix(a, "rev 8 changed since rev 7 was accepted: components.") {
		t.Fatalf("d after a change: %q", a)
	}

	if !press('o') {
		t.Fatal("o was not taken")
	}
	memWaitFor(t, "the page", func() bool {
		return answer() == "Architecture and business process opened in the browser: /state/architecture/rev-7.html"
	})
	f.mu.Lock()
	page := f.pages["rev-7.html"]
	f.mu.Unlock()
	for _, want := range []string{"<title>MARSHAL architecture</title>", "contract rev 7", "now: waiting on owner", `class="now">▸ ` + bpD3 + `</li>`, "RULES IN FORCE", "changed since rev 7"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	// With the inbox empty and tasks working, the page marks M5.
	w.permissions.mu.Lock()
	w.permissions.inbox = nil
	w.permissions.mu.Unlock()
	press('o')
	memWaitFor(t, "the second page", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return strings.Contains(f.pages["rev-7.html"], `class="now">▸ runtime dispatches approved tasks</li>`)
	})
	f.mu.Lock()
	f.openErr = errors.New("xdg-open: not found")
	f.mu.Unlock()
	memWaitFor(t, "o idle", func() bool { w.arch.mu.Lock(); defer w.arch.mu.Unlock(); return !w.arch.busy })
	press('o')
	memWaitFor(t, "the failure", func() bool { return answer() == "Opening the architecture failed: xdg-open: not found" })
	// The hidden Board's inbox gets no keys while the view is open.
	w.permissions.mu.Lock()
	w.permissions.inbox = []permission.Request{{Kind: "network", Object: "x:443"}}
	w.permissions.mu.Unlock()
	if w.handleBoardInboxKey(t.Context(), KeyEvent{Type: KeyRune, Rune: 'd'}) {
		t.Fatal("the Board's inbox took a key under the Architecture view")
	}
}

// The workspace's page goes under the project's state directory.
func TestArchOpenPageNeedsProject(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	if _, err := (workspaceArch{w}).OpenPage(t.Context(), "rev-1.html", []byte("x")); err == nil || !strings.Contains(err.Error(), "no project is open") {
		t.Fatalf("no runtime: %v", err)
	}
}

// The architecture is read once as the workspace starts, so the header's
// chip has a revision before b is ever pressed; frames never read.
func TestArchChipReadsAtStart(t *testing.T) {
	old := archRefreshEvery
	archRefreshEvery = time.Hour
	defer func() { archRefreshEvery = old }()
	w, _, f := viewsWorkspace(t)
	defer w.Close()
	th := NewTheme(ThemeNoColor, true, true)
	w.boardFrame(w.state, th, nil, 200, 40)
	if loaded, _ := w.archState(); loaded {
		t.Fatalf("the store was read before the follower started")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.followArch(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	memWaitFor(t, "a read at start", func() bool { return f.calls() == 1 })
	if w.archView.Load() {
		t.Fatal("the start read opened the view")
	}
	memWaitFor(t, "the accepted revision", func() bool {
		loaded, rev := w.archState()
		return loaded && rev == 7
	})
	for range 5 {
		w.boardFrame(w.state, th, nil, 120, 40)
	}
	time.Sleep(50 * time.Millisecond)
	if n := f.calls(); n != 1 {
		t.Fatalf("frames read the store: %d reads", n)
	}
	// Opening the view reads at once; closing it does not.
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'b'})
	memWaitFor(t, "a read on open", func() bool { return f.calls() == 2 })
	w.handleBoardKey(KeyEvent{Type: KeyEsc})
	time.Sleep(50 * time.Millisecond)
	if n := f.calls(); n != 2 {
		t.Fatalf("closing read the store: %d reads", n)
	}
}

// The chip and the open view refresh on the same cadence; nothing is read
// while the Board is off screen.
func TestArchRefreshCadence(t *testing.T) {
	old := archRefreshEvery
	archRefreshEvery = 20 * time.Millisecond
	defer func() { archRefreshEvery = old }()
	w, _, f := viewsWorkspace(t)
	defer w.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.followArch(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	memWaitFor(t, "periodic reads with the view closed", func() bool { return f.calls() >= 3 })
	f.mu.Lock()
	project := f.reads[0]
	f.mu.Unlock()
	if project != "project" {
		t.Fatalf("read project %q", project)
	}
	if w.archRevision() != 7 {
		t.Fatalf("header revision %d", w.archRevision())
	}
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '8'})
	n := f.calls()
	memWaitFor(t, "periodic reads with the view open", func() bool { return f.calls() >= n+3 })
	w.handleBoardKey(KeyEvent{Type: KeyEsc})
	w.mu.Lock()
	w.boardClassic = true
	w.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	n = f.calls()
	time.Sleep(150 * time.Millisecond)
	if m := f.calls(); m != n {
		t.Fatalf("read with the Board off screen: %d → %d", n, m)
	}
}

func TestArchViewHasNoStepCodes(t *testing.T) {
	re := regexp.MustCompile(`\b(M[1-7]|U[125]|D[1-4]|A[12]|S1|L1)\b`)

	rendered := strings.Join(boardVisual{}.archProcess("D3", "waiting on owner", 80, 18, true), "\n")
	plain := StripANSI(rendered)
	if match := re.FindString(plain); match != "" {
		t.Fatalf("archProcess contains step code %q:\n%s", match, plain)
	}
	if !strings.Contains(plain, "owner states the goal") {
		t.Fatalf("archProcess missing goal step:\n%s", plain)
	}

	page := string(archPage(store.ArchitectureOverview{}, "Marshal", nil, "D3", "waiting on owner", time.Now()))
	if match := re.FindString(page); match != "" {
		t.Fatalf("archPage contains step code %q:\n%s", match, page)
	}
	if !strings.Contains(page, "owner states the goal") {
		t.Fatalf("archPage missing goal step:\n%s", page)
	}
}
