package tui

import (
	"context"
	"errors"
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

// demoVerifyOverview is the overview the Verify view goldens paint: the
// prototype's claims, a task before its hand-in, the independent verifier
// and an architecture review.
func demoVerifyOverview(now time.Time) store.VerifyOverview {
	sup := func(text, ev, by string) store.VerifyClaim {
		return store.VerifyClaim{Status: store.ClaimSupported, Text: text, Evidence: ev, CheckedBy: by}
	}
	return store.VerifyOverview{
		RunID: "R-7f3a",
		Tasks: []store.VerifyTask{
			{ID: "T-009", Title: "events list tool", State: marshal.Merged, Worker: "opencode", WorkerProvider: "opencode", Attempt: 1, Reviewer: "codex-2", ReviewerProvider: "codex", OtherProvider: true, Verdict: marshal.VerdictAccept,
				Claims: []store.VerifyClaim{sup("events_list pages by sequence", "internal/mcp/events_list.go:61", "codex-2")}},
			{ID: "T-011", Title: "run digest", State: marshal.Accepted, Worker: "codex", WorkerProvider: "codex", Attempt: 1, Reviewer: "agy-1", ReviewerProvider: "agy", OtherProvider: true, Verdict: marshal.VerdictAccept,
				Claims: []store.VerifyClaim{{Status: store.ClaimSupported, Text: "digest is stable", Evidence: "check:go test ./internal/mcp", CheckedBy: "runtime", Criterion: true}, sup("fresh session reproduces the digest", "verify log a91f2c", "agy-1")}},
			{ID: "T-020", Title: "memory bridge", State: marshal.HandedIn, Worker: "codex", WorkerProvider: "codex", Attempt: 2, Reviewer: "claude-1", ReviewerProvider: "claude", OtherProvider: true, Verdict: marshal.VerdictReturn,
				Claims: []store.VerifyClaim{
					sup("bridge reads only durable records", "internal/app/memory_bridge.go:88", "claude-1"),
					sup("candidate never becomes durable without review", "internal/app/memory_bridge.go:141", "claude-1"),
					sup("test covers rejected candidate", "internal/app/memory_bridge_test.go:52", "claude-1"),
					{Status: store.ClaimUnsupported, Text: "restore keeps tags", Evidence: "no test or code path found", CheckedBy: "claude-1"},
				}},
			{ID: "T-024", Title: "credential lease", State: marshal.Escalated, Worker: "claude", WorkerProvider: "claude", Attempt: 1, Reviewer: "claude-3", ReviewerProvider: "claude", Verdict: marshal.VerdictReturn,
				Claims: []store.VerifyClaim{{Status: store.ClaimUnknown, Text: "lease covers the whole invocation", Evidence: "not reviewed yet", CheckedBy: "—"}}},
			{ID: "T-033", Title: "canonical migrations", State: marshal.Queued, Worker: "agy", WorkerProvider: "agy"},
		},
		Verifier:     &store.VerifyVerifier{Reviewer: "agy-v", Provider: "agy", Verdict: "pass", Commit: "a91f2c", At: now.Add(-2 * time.Minute)},
		Session:      &store.VerifySession{ID: "VER-31c2", State: "PARTIALLY_SATISFIED", At: now.Add(-2 * time.Minute)},
		Architecture: &store.VerifyArchitecture{ReceiptID: "CTX-ARCH-9", Outcome: "COMPATIBLE", Provider: "claude", Pass: 6, At: now.Add(-5 * time.Minute)},
	}
}

func demoVerifyPaint(now time.Time) verifyPaint {
	return verifyPaint{Snap: verifySnapshot{Overview: demoVerifyOverview(now), Loaded: true, At: now.Add(-3 * time.Second)}, Selected: "T-020"}
}

// fakeVerifyView is the Verify view's source in tests.
type fakeVerifyView struct {
	mu        sync.Mutex
	overview  store.VerifyOverview
	runs      []string
	returns   []string
	returnErr error
}

func (f *fakeVerifyView) VerifyOverview(_ context.Context, runID string) (store.VerifyOverview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, runID)
	return f.overview, nil
}

func (f *fakeVerifyView) ReturnTask(_ context.Context, task, reason string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.returns = append(f.returns, task+" "+reason)
	if f.returnErr != nil {
		return "", f.returnErr
	}
	return "Task " + task + " returned (return); /marshal resume to continue.", nil
}

func (f *fakeVerifyView) calls() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs), append([]string(nil), f.returns...)
}

func TestVerifyViewGoldens(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("BOARD_TRUECOLOR", "")
	now := memEpoch
	b := demoBoard(now)
	th := NewTheme(ThemeDefault, true, true)
	for _, tc := range []struct {
		name string
		w, h int
	}{{"split-120x40", 120, 40}, {"split-80x24", 80, 24}} {
		t.Run(tc.name, func(t *testing.T) {
			v := boardVisual{board: b, now: now, theme: th, frame: anim.Frame{}, edition: "Marshal", health: map[string]string{"codex": "●", "claude": "●", "agy": "◐", "opencode": "●"}, memory: true, providers: true, verify: true, verView: true}
			v.hints = goldenHints(v)
			frame := paintVerify(layout.Compute(tc.w, tc.h, layout.Split), th, v, demoVerifyPaint(now))
			got := strings.Join(frame, "\n") + "\n"
			path := filepath.Join("testdata", "board", "verify-"+tc.name+".golden")
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
				out := filepath.Join("..", "..", "out", "verify-"+strings.TrimPrefix(tc.name, "split-")+".ans")
				if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(out, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for i, line := range frame {
				if n := VisibleLen(line); n != tc.w {
					t.Errorf("row %d width %d, want %d", i, n, tc.w)
				}
			}
			plain := StripANSI(got)
			for _, want := range []string{"VERIFY", "T-009", "T-020", "T-033", "3/4", "CLAIMS · T-020", "UNSUPPORTED", "restore keeps tags", "return unsupported claims"} {
				if !strings.Contains(plain, want) {
					t.Errorf("Verify view lacks %q", want)
				}
			}
			for _, key := range []string{"return", "esc"} {
				if !strings.Contains(StripANSI(frame[len(frame)-1]), key) {
					t.Errorf("footer lacks %q: %s", key, StripANSI(frame[len(frame)-1]))
				}
			}
		})
	}
}

func TestVerifyViewContent(t *testing.T) {
	now := memEpoch
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), now: now}
	m := demoVerifyPaint(now)
	lines, sel := v.verifyLines(m, 120)
	text := strings.Join(lines, "\n")
	for _, want := range []string{
		"read 3s ago · every 10 s while open",
		"UNSUPPORTED restore keeps tags · no test or code path found · checked by claude-1",
		"SUPPORTED   bridge reads only durable records · internal/app/memory_bridge.go:88 · checked by claude-1",
		"[x] return unsupported claims for rework",
		"pass · agy-v (agy)",
		"PARTIALLY_SATISFIED · VER-31c2",
		"COMPATIBLE · 6 pass, 0 fail, 0 unknown · claude",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Verify view lacks %q:\n%s", want, text)
		}
	}
	row := func(id string) string {
		for _, l := range lines {
			if strings.HasPrefix(l, "● "+id+" ") {
				return l
			}
		}
		t.Fatalf("no row for %s:\n%s", id, text)
		return ""
	}
	for id, want := range map[string][]string{
		"T-009": {"merged", "1/1", "codex-2", "other provider"},
		"T-020": {"handed_in", "3/4", "claude-1", "other provider"},
		"T-024": {"escalated", "0/1", "same provider"},
		"T-033": {"queued", "—"},
	} {
		for _, w := range want {
			if !strings.Contains(row(id), w) {
				t.Errorf("%s row lacks %q: %s", id, w, row(id))
			}
		}
	}
	if sel < 0 || !strings.Contains(lines[sel], "T-020") {
		t.Fatalf("selected line %d", sel)
	}
	for sel, want := range map[string]string{
		"T-009": "every claim is SUPPORTED",
		"T-024": "escalated: use /marshal retry T-024",
		"T-033": "no hand-in yet",
	} {
		m.Selected = sel
		lines, _ = v.verifyLines(m, 80)
		if text := strings.Join(lines, "\n"); !strings.Contains(text, want) {
			t.Errorf("%s lacks %q:\n%s", sel, want, text)
		}
	}
	m.Selected, m.Busy = "T-020", "T-020"
	lines, _ = v.verifyLines(m, 80)
	if text := strings.Join(lines, "\n"); !strings.Contains(text, "returning T-020…") {
		t.Errorf("busy view:\n%s", text)
	}
	empty := verifyPaint{Snap: verifySnapshot{Loaded: true, At: now}}
	lines, _ = v.verifyLines(empty, 100)
	text = strings.Join(lines, "\n")
	for _, want := range []string{"the run has no tasks yet", "not recorded yet", "no review recorded"} {
		if !strings.Contains(text, want) {
			t.Errorf("empty view lacks %q:\n%s", want, text)
		}
	}
	for i, l := range hostASCII(lines, 100) {
		for _, r := range l {
			if r > 127 {
				t.Fatalf("ASCII line %d has %q: %s", i, r, l)
			}
		}
	}
}

func verifyWorkspace(t *testing.T) (*Workspace, *fakeVerifyView) {
	t.Helper()
	w, _ := boardWorkspace(t)
	f := &fakeVerifyView{overview: demoVerifyOverview(time.Now())}
	w.verSource = f
	w.provSource = &fakeProviderView{providers: demoProviders(time.Now())}
	w.memSource = &fakeMemory{overview: demoMemoryOverview(time.Now())}
	return w, f
}

func TestVerifyViewKeys(t *testing.T) {
	w, _ := verifyWorkspace(t)
	defer w.Close()
	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }

	w.composer.SetText("5")
	if press('5') || w.verView.Load() {
		t.Fatal("5 opened the Verify view while the composer held text")
	}
	w.composer.SetText("")
	w.boardUI.inspect = "T-017"
	if press('5') || w.verView.Load() {
		t.Fatal("5 opened the Verify view over the inspector")
	}
	w.boardUI.inspect = ""
	for _, back := range []KeyEvent{{Type: KeyEsc}, {Type: KeyRune, Rune: '1'}, {Type: KeyRune, Rune: '5'}} {
		if !press('5') || !w.verView.Load() {
			t.Fatal("5 did not open the Verify view")
		}
		if !w.handleBoardKey(back) || w.verView.Load() {
			t.Fatalf("%+v did not return to the Board", back)
		}
	}

	// 3 and 4 go to the other views and 5 comes back from each.
	press('5')
	if !press('3') || w.verView.Load() || !w.memView.Load() {
		t.Fatal("3 did not switch to the Memory view")
	}
	if !press('5') || !w.verView.Load() || w.memView.Load() {
		t.Fatal("5 did not switch from the Memory view")
	}
	if !press('4') || w.verView.Load() || !w.provView.Load() {
		t.Fatal("4 did not switch to the Providers view")
	}
	if !press('5') || !w.verView.Load() || w.provView.Load() {
		t.Fatal("5 did not switch from the Providers view")
	}
	if w.hostWatch.Load() != nil {
		for _, r := range []rune{'2', 'h'} {
			if !press(r) || w.verView.Load() || !w.hostView.Load() {
				t.Fatalf("%c did not switch to the Host view", r)
			}
			if !press('5') || !w.verView.Load() || w.hostView.Load() {
				t.Fatal("5 did not switch from the Host view")
			}
		}
	}

	w.refreshVerify(t.Context())
	selected := func() string { w.ver.mu.Lock(); defer w.ver.mu.Unlock(); return w.ver.selected }
	if selected() != "T-009" {
		t.Fatalf("first selection %q", selected())
	}
	press('j')
	press('j')
	if selected() != "T-020" {
		t.Fatalf("j moved to %q", selected())
	}
	press('k')
	if selected() != "T-011" {
		t.Fatalf("k moved to %q", selected())
	}

	// The hidden Board's inbox gets no keys while the view is open.
	if w.handleBoardInboxKey(t.Context(), KeyEvent{Type: KeyRune, Rune: 'd'}) {
		t.Fatal("the Board's inbox took a key under the Verify view")
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyEsc}) || w.verView.Load() {
		t.Fatal("Esc did not return to the Board")
	}

	// Without a store there is no Verify view.
	w.verSource = nil
	if press('5') || w.verView.Load() {
		t.Fatal("5 opened a Verify view with nothing to read")
	}
}

// x returns a handed-in task's unsupported claims through /marshal return
// and says what to do for every other task.
func TestVerifyReturnKey(t *testing.T) {
	w, f := verifyWorkspace(t)
	defer w.Close()
	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	answer := func() string { w.mu.RLock(); defer w.mu.RUnlock(); return w.state.LastOutput }
	sel := func(id string) { w.ver.mu.Lock(); w.ver.selected = id; w.ver.mu.Unlock() }
	press('5')
	w.refreshVerify(t.Context())

	for id, want := range map[string]string{
		"T-009": "T-009: every claim is SUPPORTED; nothing to return",
		"T-024": "T-024 is escalated: use /marshal retry T-024",
		"T-033": "T-033: no hand-in yet; nothing to return",
	} {
		sel(id)
		if !press('x') {
			t.Fatal("x was not taken")
		}
		if answer() != want {
			t.Errorf("x on %s: %q, want %q", id, answer(), want)
		}
	}
	// T-011 is accepted with nothing unsupported; mark one unknown to see
	// that a task past its decision is not returned.
	f.mu.Lock()
	f.overview.Tasks[1].Claims[1].Status = store.ClaimUnknown
	f.mu.Unlock()
	w.refreshVerify(t.Context())
	sel("T-011")
	press('x')
	if a := answer(); a != "T-011 is accepted; /marshal return takes only a task awaiting your decision" {
		t.Errorf("x on accepted: %q", a)
	}
	if _, r := f.calls(); len(r) != 0 {
		t.Fatalf("returned without a handed-in task: %v", r)
	}

	th := NewTheme(ThemeNoColor, true, true)
	w.boardFrame(w.state, th, nil, 120, 40)
	sel("T-020")
	// x only arms the return: nothing is sent until Enter, and Esc
	// cancels it, as the Memory view's a and r do.
	press('x')
	if _, r := f.calls(); len(r) != 0 {
		t.Fatalf("x returned without confirmation: %v", r)
	}
	if a := answer(); a != "Return T-020 with 1 unsupported claim(s)? Enter returns it for rework · Esc cancels" {
		t.Fatalf("x prompt %q", a)
	}
	if lines, _, _ := w.boardFrame(w.state, th, nil, 120, 40); !strings.Contains(StripANSI(strings.Join(lines, "\n")), "Enter to return T-020 with 1 unsupported claim(s) for rework · Esc cancels") {
		t.Fatalf("view does not show the confirmation:\n%s", StripANSI(strings.Join(lines, "\n")))
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyEsc}) || !w.verView.Load() {
		t.Fatal("Esc on an armed return left the view")
	}
	w.handleBoardKey(KeyEvent{Type: KeyEnter})
	if _, r := f.calls(); len(r) != 0 {
		t.Fatalf("Enter after Esc returned: %v", r)
	}
	press('x')
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter on an armed return was not taken")
	}
	memWaitFor(t, "the return", func() bool { _, r := f.calls(); return len(r) == 1 })
	if _, r := f.calls(); r[0] != `T-020 unsupported claims: "restore keeps tags" (unsupported)` {
		t.Fatalf("return %q", r[0])
	}
	memWaitFor(t, "the return answer", func() bool { return strings.HasPrefix(answer(), "Task T-020 returned") })
	frame, _, _ := w.boardFrame(w.state, th, nil, 120, 40)
	if last := frame[len(frame)-1]; !strings.Contains(last, "Task T-020 returned") {
		t.Fatalf("footer does not show the return answer: %s", last)
	}

	// A refused return shows why, in red.
	f.mu.Lock()
	f.returnErr = errors.New("task is not awaiting a decision")
	f.mu.Unlock()
	press('x')
	w.handleBoardKey(KeyEvent{Type: KeyEnter})
	memWaitFor(t, "the refusal", func() bool { return answer() == "Return failed: task is not awaiting a decision" })
}

// The return key goes through the /marshal return command path.
func TestVerifyReturnUsesMarshalCommand(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	if w.cmd == nil {
		t.Skip("no command handler")
	}
	_, err := workspaceVerify{w}.ReturnTask(t.Context(), "T-020", "unsupported claims")
	if err == nil {
		t.Fatal("a return with no Marshal service succeeded")
	}
	if _, usage := (workspaceVerify{w}).ReturnTask(t.Context(), "T-020", ""); usage == nil || !strings.Contains(usage.Error(), "/marshal return") {
		t.Fatalf("an empty reason did not reach /marshal return: %v", usage)
	}
}

func TestVerifyRefreshOnlyWhileOpen(t *testing.T) {
	old := verifyRefreshEvery
	verifyRefreshEvery = 20 * time.Millisecond
	defer func() { verifyRefreshEvery = old }()
	w, f := verifyWorkspace(t)
	defer w.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.followVerify(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	time.Sleep(100 * time.Millisecond)
	if n, _ := f.calls(); n != 0 {
		t.Fatalf("closed view read %d times", n)
	}
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '5'})
	memWaitFor(t, "a read on open", func() bool { n, _ := f.calls(); return n >= 1 })
	memWaitFor(t, "a periodic read", func() bool { n, _ := f.calls(); return n >= 3 })
	f.mu.Lock()
	run := f.runs[0]
	f.mu.Unlock()
	if run != "R-7f3a" {
		t.Fatalf("read run %q", run)
	}
	w.handleBoardKey(KeyEvent{Type: KeyEsc})
	time.Sleep(50 * time.Millisecond)
	n, _ := f.calls()
	time.Sleep(150 * time.Millisecond)
	if m, _ := f.calls(); m != n {
		t.Fatalf("closed view kept reading: %d → %d", n, m)
	}

	// Painting does not read: frames come from the last snapshot.
	verifyRefreshEvery = time.Hour
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '5'})
	memWaitFor(t, "a read on reopen", func() bool { m, _ := f.calls(); return m == n+1 })
	th := NewTheme(ThemeNoColor, true, true)
	for range 5 {
		w.boardFrame(w.state, th, nil, 120, 40)
	}
	time.Sleep(50 * time.Millisecond)
	if m, _ := f.calls(); m != n+1 {
		t.Fatalf("frames read the store: %d reads", m-n)
	}
	// A return reads again at once, not at the next period.
	w.ver.mu.Lock()
	w.ver.selected = "T-020"
	w.ver.mu.Unlock()
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'x'})
	w.handleBoardKey(KeyEvent{Type: KeyEnter})
	memWaitFor(t, "a read after the return", func() bool { m, _ := f.calls(); return m == n+2 })
}
