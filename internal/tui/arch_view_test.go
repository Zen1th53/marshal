package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// demoArchStatus is the review state the goldens paint: a draft revision 7
// waiting for the owner.
func demoArchStatus() app.ArchitectureStatus {
	return app.ArchitectureStatus{Exists: true, Revision: 7}
}

// demoArchPaint is what the goldens paint.
func demoArchPaint(now time.Time) archPaint {
	return archPaint{Snap: archSnapshot{Status: demoArchStatus(), Loaded: true, At: now.Add(-4 * time.Second)}}
}

// fakeArchView is the Architecture view's source in tests: it reports a
// status and counts reads and browser opens.
type fakeArchView struct {
	mu     sync.Mutex
	status app.ArchitectureStatus
	err    error
	reads  int
	opens  int
}

func (f *fakeArchView) ArchitectureStatus(context.Context) (app.ArchitectureStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.status, f.err
}

func (f *fakeArchView) Open() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens++
}

func (f *fakeArchView) setStatus(s app.ArchitectureStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = s
}

func (f *fakeArchView) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func (f *fakeArchView) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

func lastOutput(w *Workspace) string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state.LastOutput
}

func TestArchViewGoldens(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("BOARD_TRUECOLOR", "")
	now := memEpoch
	th := NewTheme(ThemeDefault, true, true)
	for _, tc := range []struct {
		name string
		w, h int
		// 80x25: the always-on vitals row takes the top row of every screen,
		// so the body is one row shorter than the terminal. The extra row
		// gives the view the body height it had at 80x24 before the vitals
		// row existed, so every component and rule still fits at the minimum.
	}{{"split-120x40", 120, 40}, {"split-80x25", 80, 25}} {
		t.Run(tc.name, func(t *testing.T) {
			v := boardViewVisual(now, th)
			v.archView = true
			l := layout.Compute(tc.w, tc.h, layout.Split)
			frame := paintBodyView(l, th, v, archPane, func(w, h int) []string { return v.archRender(demoArchPaint(now), w, h) })
			checkViewGolden(t, archGoldenName, tc.name, frame, tc.w)
			plain := StripANSI(strings.Join(frame, "\n"))
			if want := "Architecture · rev 7 · waiting for you · [o] open"; !strings.Contains(plain, want) {
				t.Errorf("Architecture view lacks %q", want)
			}
			// The body is one status line then blank rows: no section titles.
			bodyLines := 0
			for _, l := range frame[1 : len(frame)-1] {
				if strings.TrimSpace(StripANSI(l)) != "" {
					bodyLines++
				}
			}
			if bodyLines != 1 {
				t.Errorf("Architecture body has %d non-blank lines, want 1", bodyLines)
			}
			if last := StripANSI(frame[len(frame)-1]); !strings.Contains(last, "open in browser") || !strings.Contains(last, "esc") {
				t.Errorf("footer: %s", last)
			}
		})
	}
}

// TestArchStatusLineStates covers every state of Required behavior 1.
func TestArchStatusLineStates(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true)}
	draft := func(s app.ArchitectureStatus) archSnapshot { return archSnapshot{Loaded: true, Status: s} }
	for _, tc := range []struct {
		name    string
		snap    archSnapshot
		want    string
		hasOpen bool
	}{
		{"reading", archSnapshot{}, "Architecture · reading…", false},
		{"read failed", archSnapshot{Err: "boom"}, "Architecture · could not be read right now", false},
		{"no review", draft(app.ArchitectureStatus{}), "Architecture · not drafted yet · the Marshal drafts it after your goal", false},
		{"accepted", draft(app.ArchitectureStatus{Exists: true, Revision: 3, Accepted: true}), "Architecture · rev 3 · accepted · [o] open", true},
		{"answering", draft(app.ArchitectureStatus{Exists: true, Revision: 3, Answering: true}), "Architecture · rev 3 · the Marshal is answering your request · [o] open", true},
		{"one open", draft(app.ArchitectureStatus{Exists: true, Revision: 3, Open: 1}), "Architecture · rev 3 · waiting for you · 1 open request · [o] open", true},
		{"two open", draft(app.ArchitectureStatus{Exists: true, Revision: 3, Open: 2}), "Architecture · rev 3 · waiting for you · 2 open requests · [o] open", true},
		{"draft", draft(app.ArchitectureStatus{Exists: true, Revision: 3}), "Architecture · rev 3 · waiting for you · [o] open", true},
	} {
		line, open := v.archStatusLine(tc.snap)
		plain := StripANSI(line)
		if plain != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, plain, tc.want)
		}
		if tc.hasOpen {
			if open < 0 || open > len([]rune(plain)) || string([]rune(plain)[open:]) != "[o] open" {
				t.Errorf("%s: open %d does not point at %q in %q", tc.name, open, "[o] open", plain)
			}
		} else if open != -1 {
			t.Errorf("%s: open %d, want -1", tc.name, open)
		}
	}
}

// TestArchOpenKeyAndClick: the key o and a click on "[o] open" each call the
// source's Open() once.
func TestArchOpenKeyAndClick(t *testing.T) {
	w, _, f := viewsWorkspace(t)
	defer w.Close()
	f.setStatus(app.ArchitectureStatus{Exists: true, Revision: 3})
	w.refreshArch(t.Context())
	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	press('8')
	if !w.archView.Load() {
		t.Fatal("8 did not open the Architecture view")
	}
	before := f.openCount()
	if !press('o') || f.openCount() != before+1 {
		t.Fatalf("o did not open the browser once: %d", f.openCount())
	}
	row, col := cellOf(t, w, 120, 40, "[o] open", 0)
	if !click(w, row, col) {
		t.Fatal("a click on [o] open was not taken")
	}
	if f.openCount() != before+2 {
		t.Fatalf("a click on [o] open did not open the browser once: %d", f.openCount())
	}
}

// TestArchOpenWithoutReview: with no review, o opens nothing and says so.
func TestArchOpenWithoutReview(t *testing.T) {
	w, _, f := viewsWorkspace(t)
	defer w.Close()
	f.setStatus(app.ArchitectureStatus{})
	w.refreshArch(t.Context())
	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	press('8')
	before := f.openCount()
	press('o')
	if f.openCount() != before {
		t.Fatalf("o opened the browser with no review: %d", f.openCount())
	}
	if ans := lastOutput(w); ans != "The Marshal has not drafted the architecture yet." {
		t.Fatalf("o answer: %q", ans)
	}
}

// TestArchAcceptedClosesView: a status read that turns Accepted closes the
// open view without a key.
func TestArchAcceptedClosesView(t *testing.T) {
	w, _, f := viewsWorkspace(t)
	defer w.Close()
	f.setStatus(app.ArchitectureStatus{Exists: true, Revision: 2})
	w.refreshArch(t.Context())
	w.openArchView()
	if !w.archView.Load() {
		t.Fatal("the view did not open")
	}
	f.setStatus(app.ArchitectureStatus{Exists: true, Revision: 2, Accepted: true})
	w.refreshArch(t.Context())
	if w.archView.Load() {
		t.Fatal("the view did not close when the review turned accepted")
	}
	if ans := lastOutput(w); ans != "Architecture accepted — back to the Board." {
		t.Fatalf("close answer: %q", ans)
	}
}

// TestArchFailedRefreshDropsTheOldStatus: a read that fails after a good one
// must not keep showing the old status as current.
func TestArchFailedRefreshDropsTheOldStatus(t *testing.T) {
	w, _, f := viewsWorkspace(t)
	defer w.Close()
	f.setStatus(app.ArchitectureStatus{Exists: true, Revision: 3})
	w.refreshArch(t.Context())
	f.mu.Lock()
	f.err = errors.New("state.json is not valid")
	f.mu.Unlock()
	w.refreshArch(t.Context())
	w.arch.mu.Lock()
	snap := w.arch.snap
	w.arch.mu.Unlock()
	if snap.Loaded || snap.Status.Exists || snap.Err == "" {
		t.Fatalf("after a failed refresh the snapshot still reads as current: %+v", snap)
	}
	if line, open := (boardVisual{}).archStatusLine(snap); !strings.Contains(line, "could not be read right now") || open != -1 {
		t.Fatalf("line after a failed refresh = %q (open %d)", line, open)
	}
}

// TestArchWakeReadsWhenClosed: after an Accept wakes the follower, it reads
// even with the view closed, so the move-on is seen (Step 4).
func TestArchWakeReadsWhenClosed(t *testing.T) {
	old := archRefreshEvery
	archRefreshEvery = time.Hour
	defer func() { archRefreshEvery = old }()
	w, _, f := viewsWorkspace(t)
	defer w.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.followArch(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	memWaitFor(t, "the start read", func() bool { return f.calls() == 1 })
	if w.archView.Load() {
		t.Fatal("the start read opened the view")
	}
	n := f.calls()
	w.wakeArch()
	memWaitFor(t, "a read after a wake with the view closed", func() bool { return f.calls() > n })
}

// TestArchReadsAtStartNotPerFrame: the review is read once as the workspace
// starts and on open, never per painted frame.
func TestArchReadsAtStartNotPerFrame(t *testing.T) {
	old := archRefreshEvery
	archRefreshEvery = time.Hour
	defer func() { archRefreshEvery = old }()
	w, _, f := viewsWorkspace(t)
	defer w.Close()
	f.setStatus(app.ArchitectureStatus{Exists: true, Revision: 7})
	th := NewTheme(ThemeNoColor, true, true)
	w.boardFrame(w.state, th, nil, 200, 40)
	if w.archSnapshotNow().Loaded {
		t.Fatal("the review was read before the follower started")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.followArch(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	memWaitFor(t, "a read at start", func() bool { return f.calls() == 1 })
	if w.archView.Load() {
		t.Fatal("the start read opened the view")
	}
	for range 5 {
		w.boardFrame(w.state, th, nil, 120, 40)
	}
	time.Sleep(50 * time.Millisecond)
	if n := f.calls(); n != 1 {
		t.Fatalf("frames read the review: %d reads", n)
	}
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'b'})
	memWaitFor(t, "a read on open", func() bool { return f.calls() == 2 })
}

// TestArchRefreshCadence: the open view refreshes on the same cadence;
// nothing is read while the Board is off screen.
func TestArchRefreshCadence(t *testing.T) {
	old := archRefreshEvery
	archRefreshEvery = 20 * time.Millisecond
	defer func() { archRefreshEvery = old }()
	w, _, f := viewsWorkspace(t)
	defer w.Close()
	f.setStatus(app.ArchitectureStatus{Exists: true, Revision: 7})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.followArch(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	memWaitFor(t, "periodic reads with the view closed", func() bool { return f.calls() >= 3 })
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

// TestArchViewKeys: b and 8 open the view on the workspace source and the
// view keys return to the Board.
func TestArchViewKeys(t *testing.T) {
	w, _, _ := viewsWorkspace(t)
	defer w.Close()
	checkViewKeys(t, w, []rune{'b', '8'}, w.archView.Load)
	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	press('b')
	press('1')
	w.archSource = nil
	if w.archSrc() == nil || !press('b') || !w.archView.Load() {
		t.Fatal("b did not open the Architecture view on the workspace source")
	}
}
