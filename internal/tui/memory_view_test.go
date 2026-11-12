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

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

var memEpoch = time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)

// demoMemoryOverview is the overview the Memory view goldens paint: the
// prototype's queue, recalls and tools.
func demoMemoryOverview(now time.Time) store.MemoryOverview {
	at := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	return store.MemoryOverview{
		Counts: store.MemoryOverviewCounts{Total: 251, ByKindLife: map[string]map[string]int{
			"semantic": {"durable": 96, "rejected": 40},
			"episodic": {"durable": 44, "rejected": 68, "candidate": 2},
			"finding":  {"candidate": 1},
		}},
		ReviewQueue: []store.MemoryReviewQueueItem{
			{ID: "MEM-c7a1", Kind: "finding", Title: "memory candidates: a duplicate key in data_json is read differently by SQLite and Go", Confidence: "high", Authority: "agent", CreatedAt: at(2 * time.Minute)},
			{ID: "MEM-41be", Kind: "episodic", Title: "recall ranking prefers durable semantic records over episodic ones", Confidence: "medium", Authority: "agent", CreatedAt: at(5 * time.Minute)},
			{ID: "MEM-9d02", Kind: "episodic", Title: "restore preview must list records before confirmation", Confidence: "medium", Authority: "agent", CreatedAt: at(9 * time.Minute)},
		},
		RecentRecalls: store.MemoryRecentRecalls{TotalReceipts: 1943, Recent: []store.MemoryReceiptSummary{
			{CallerID: "claude-1", TaskID: "T-018", QueryDigest: "3fa9c1d2e4b5a6f7", OutcomeStatus: "served", CreatedAt: now.Add(-14 * time.Second)},
			{CallerID: "codex-1", TaskID: "T-017", QueryDigest: "a01b2c3d4e5f6071", OutcomeStatus: "served", CreatedAt: now.Add(-48 * time.Second)},
			{CallerID: "agy-1", TaskID: "T-019", QueryDigest: "77e1d0c9b8a7f6e5", OutcomeStatus: "served", DeniedCount: 1, CreatedAt: now.Add(-2 * time.Minute)},
			{CallerID: "marshal", RunID: "R-7f3a", QueryDigest: "0c0ffee15bad1dea", OutcomeStatus: "served", CreatedAt: now.Add(-6 * time.Minute)},
		}},
		MCPTools:  store.MemoryMCPTools{ToolCalls: map[string]int{"memory_recall": 1943, "memory_remember": 140, "events_list": 612, "run_digest": 233, "memory_restore_preview": 9}, CandidatesCount: 140, ProposalsCount: 443},
		Snapshots: store.MemorySnapshots{TotalSnapshots: 12, NewestSnapshot: at(41 * time.Minute)},
	}
}

func demoMemoryPaint(now time.Time) memoryPaint {
	return memoryPaint{Snap: memorySnapshot{Overview: demoMemoryOverview(now), Loaded: true, At: now.Add(-3 * time.Second), StorePath: ".marshal/state.db"}, Selected: "MEM-c7a1"}
}

// fakeMemory is the Memory view's source in tests.
type fakeMemory struct {
	mu        sync.Mutex
	overview  store.MemoryOverview
	overviews int
	reviews   []string
	record    model.MemoryRecordV2
}

func (f *fakeMemory) MemoryOverview(context.Context) (store.MemoryOverview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overviews++
	return f.overview, nil
}

func (f *fakeMemory) MemoryRecord(_ context.Context, id string) (model.MemoryRecordV2, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.record
	r.ID = id
	return r, nil
}

func (f *fakeMemory) ReviewMemory(_ context.Context, id string, keep bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	verb := "deny"
	if keep {
		verb = "allow"
	}
	f.reviews = append(f.reviews, verb+" "+id)
	return "Memory decision recorded: " + id, nil
}

func (f *fakeMemory) calls() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.overviews, append([]string(nil), f.reviews...)
}

func memWaitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMemoryViewGoldens(t *testing.T) {
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
			v := boardVisual{board: b, now: now, theme: th, frame: anim.Frame{}, edition: "Marshal", health: map[string]string{"codex": "●", "claude": "●", "agy": "◐", "opencode": "●"}, memory: true, memView: true}
			v.hints = goldenHints(v)
			frame, _, _ := paintMemory(layout.Compute(tc.w, tc.h, layout.Split), th, v, demoMemoryPaint(now), 0, true, nil)
			got := strings.Join(frame, "\n") + "\n"
			path := filepath.Join("testdata", "board", "memory-"+tc.name+".golden")
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
				out := filepath.Join("..", "..", "out", "memory-"+strings.TrimPrefix(tc.name, "split-")+".ans")
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
			order := []string{"MEMORY", "REVIEW QUEUE"}
			at := 0
			for _, s := range order {
				i := strings.Index(plain[at:], s+" ")
				if i < 0 {
					t.Errorf("section %s missing or out of order", s)
					continue
				}
				at += i
			}
			for _, key := range []string{"keep", "reject", "evidence", "esc"} {
				if !strings.Contains(StripANSI(frame[len(frame)-1]), key) {
					t.Errorf("footer lacks %q: %s", key, StripANSI(frame[len(frame)-1]))
				}
			}
		})
	}
	// The short view scrolls to the queue's last candidate.
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), now: now}
	lines, _, maxScroll := v.memoryRender(demoMemoryPaint(now), 80, 10, 1000, false)
	if text := strings.Join(lines, "\n"); maxScroll == 0 || !strings.Contains(text, "MEM-9d02") {
		t.Fatalf("scrolled to %d:\n%s", maxScroll, text)
	}
}

func TestMemoryViewContent(t *testing.T) {
	now := memEpoch
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), now: now}
	m := demoMemoryPaint(now)
	lines, _ := v.memoryLines(m, 160)
	text := strings.Join(lines, "\n")
	for _, want := range []string{
		"251 records · 140 durable · 3 waiting review · 108 rejected",
		".marshal/state.db · project memory shared by every window",
		"1,943 served · last 14s ago",
		"12 restore points · last 41m ago",
		"MEM-c7a1 finding · confidence high · agent · 2m ago",
		"[a] keep as durable  [r] reject  [i] evidence",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Memory view lacks %q", want)
		}
	}
	// MCP is machinery: while it works the view shows none of it, neither
	// its counts nor its tools nor the recalls' raw callers and digests.
	for _, absent := range []string{"MCP", "candidates from workers", "proposals audited", "memory_recall", "RECENT RECALLS", "3fa9c1d2", "claude-1", "no gateway calls recorded"} {
		if strings.Contains(text, absent) {
			t.Errorf("Memory view shows %q while MCP works", absent)
		}
	}
	// An MCP failure is shown, as its own line.
	m.Snap.MCP = "The Marshal chat lost its MCP connection."
	lines, _ = v.memoryLines(m, 160)
	if !strings.Contains(strings.Join(lines, "\n"), "MCP            The Marshal chat lost its MCP connection.") {
		t.Errorf("an MCP failure is missing:\n%s", strings.Join(lines, "\n"))
	}
	m.Snap.MCP = ""
	m.Pending = 'r'
	lines, _ = v.memoryLines(m, 160)
	if !strings.Contains(strings.Join(lines, "\n"), "Enter to reject MEM-c7a1 · Esc cancels") {
		t.Error("a pending rejection does not ask for Enter")
	}
	empty := memoryPaint{Snap: memorySnapshot{Loaded: true, At: now}}
	lines, _ = v.memoryLines(empty, 100)
	text = strings.Join(lines, "\n")
	for _, want := range []string{"0 records ·", "nothing waiting review"} {
		if !strings.Contains(text, want) {
			t.Errorf("empty Memory view lacks %q", want)
		}
	}
	one := memoryPaint{Snap: memorySnapshot{Loaded: true, At: now, Overview: store.MemoryOverview{Counts: store.MemoryOverviewCounts{Total: 1}, Snapshots: store.MemorySnapshots{TotalSnapshots: 1}}}}
	lines, _ = v.memoryLines(one, 100)
	text = strings.Join(lines, "\n")
	for _, want := range []string{"1 record ·", "1 restore point"} {
		if !strings.Contains(text, want) {
			t.Errorf("Memory view lacks the singular %q:\n%s", want, text)
		}
	}
	ascii := hostASCII(lines, 100)
	for i, l := range ascii {
		for _, r := range l {
			if r > 127 {
				t.Fatalf("ASCII line %d has %q: %s", i, r, l)
			}
		}
	}
}

// TestMemoryViewShowsMCPOnlyOnFailure: the live view's MCP line comes from
// the workspace's recorded MCP failure, and F6 opens its detail or says MCP
// works.
func TestMemoryViewShowsMCPOnlyOnFailure(t *testing.T) {
	w, _ := memoryWorkspace(t)
	defer w.Close()
	w.handleTerminalKey(t.Context(), KeyEvent{Type: KeyF6})
	w.mu.RLock()
	answer := w.answer
	w.mu.RUnlock()
	if answer != mcpNoFailure {
		t.Fatalf("F6 with MCP working answered %q", answer)
	}
	if k, _ := boardAction("fkey.mcp"); func() bool { run, _ := k.available(w); return run }() {
		t.Fatal("F6 is offered while MCP works")
	}
	w.chatMCPRecoveryFailed(t.TempDir(), errors.New("the unrecovered chat exited"))
	if !w.mcpFailed() || !strings.Contains(w.mcpFailure(), "lost its MCP connection") {
		t.Fatalf("the failure was not recorded: %q", w.mcpFailure())
	}
	w.handleTerminalKey(t.Context(), KeyEvent{Type: KeyF6})
	w.mu.RLock()
	answer = w.answer
	w.mu.RUnlock()
	if !strings.Contains(answer, "lost its MCP connection") {
		t.Fatalf("F6 after a failure answered %q", answer)
	}
}

func memoryWorkspace(t *testing.T) (*Workspace, *fakeMemory) {
	t.Helper()
	w, _ := boardWorkspace(t)
	f := &fakeMemory{overview: demoMemoryOverview(time.Now()), record: model.MemoryRecordV2{Kind: "finding", Lifecycle: model.MemoryCandidate, Title: "memory candidates: duplicate key", Body: "SQLite keeps the last key; Go keeps the first.", EvidenceIDs: []string{"artifact:diff-a91f2c", "test:TestDuplicateKey"}}}
	w.memSource = f
	return w, f
}

func TestMemoryViewKeys(t *testing.T) {
	w, f := memoryWorkspace(t)
	defer w.Close()
	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	key := func(k KeyType) bool { return w.handleBoardKey(KeyEvent{Type: k}) }

	w.composer.SetText("3")
	if press('3') || w.memView.Load() {
		t.Fatal("3 opened the Memory view while the composer held text")
	}
	w.composer.SetText("")
	w.boardUI.inspect = "T-017"
	if press('3') || w.memView.Load() {
		t.Fatal("3 opened the Memory view over the inspector")
	}
	w.boardUI.inspect = ""
	if !press('3') || !w.memView.Load() {
		t.Fatal("3 did not open the Memory view")
	}
	if !key(KeyEsc) || w.memView.Load() {
		t.Fatal("Esc did not return to the Board")
	}
	press('3')
	w.refreshMemory(t.Context())

	selected := func() string { w.mem.mu.Lock(); defer w.mem.mu.Unlock(); return w.mem.selected }
	if selected() != "MEM-c7a1" {
		t.Fatalf("first selection %q", selected())
	}
	press('j')
	press('j')
	press('j')
	if selected() != "MEM-9d02" {
		t.Fatalf("j moved to %q", selected())
	}
	press('k')
	if selected() != "MEM-41be" {
		t.Fatalf("k moved to %q", selected())
	}

	// a waits for Enter; Esc cancels it and stays in the view.
	press('a')
	if !key(KeyEsc) || !w.memView.Load() {
		t.Fatal("Esc on a pending decision left the view")
	}
	key(KeyEnter)
	if _, reviews := f.calls(); len(reviews) != 0 {
		t.Fatalf("a cancelled decision was recorded: %v", reviews)
	}
	press('a')
	key(KeyEnter)
	memWaitFor(t, "keep", func() bool { _, r := f.calls(); return len(r) == 1 })
	press('j')
	press('r')
	key(KeyEnter)
	memWaitFor(t, "reject", func() bool { _, r := f.calls(); return len(r) == 2 })
	if _, reviews := f.calls(); reviews[0] != "allow MEM-41be" || reviews[1] != "deny MEM-9d02" {
		t.Fatalf("reviews %v", reviews)
	}
	w.mu.RLock()
	answer := w.state.LastOutput
	w.mu.RUnlock()
	if answer != "Memory decision recorded: MEM-9d02" {
		t.Fatalf("answer %q", answer)
	}

	// i opens the evidence; Esc closes it and keeps the view open.
	press('i')
	w.mem.mu.Lock()
	evidence := strings.Join(w.mem.evidence, "\n")
	w.mem.mu.Unlock()
	for _, want := range []string{"EVIDENCE MEM-9d02", "2 refs", "artifact:diff-a91f2c", "SQLite keeps the last key"} {
		if !strings.Contains(evidence, want) {
			t.Errorf("evidence lacks %q:\n%s", want, evidence)
		}
	}
	th := NewTheme(ThemeNoColor, true, true)
	frame, _, _ := w.boardFrame(w.state, th, nil, 120, 40)
	if !strings.Contains(strings.Join(frame, "\n"), "EVIDENCE MEM-9d02") {
		t.Fatal("the evidence overlay was not painted")
	}
	if !key(KeyEsc) || !w.memView.Load() {
		t.Fatal("Esc on the evidence left the view")
	}
	w.mem.mu.Lock()
	closed := w.mem.evidence == nil
	w.mem.mu.Unlock()
	if !closed {
		t.Fatal("Esc did not close the evidence")
	}

	// The hidden Board's inbox gets no keys while the view is open.
	if w.handleBoardInboxKey(t.Context(), KeyEvent{Type: KeyRune, Rune: 'd'}) {
		t.Fatal("the Board's inbox took a key under the Memory view")
	}
	if !press('1') || w.memView.Load() {
		t.Fatal("1 did not return to the Board")
	}

	// Without a store there is no Memory view.
	w.memSource = nil
	if press('3') || w.memView.Load() {
		t.Fatal("3 opened a Memory view with nothing to read")
	}
}

func TestMemoryRefreshOnlyWhileOpen(t *testing.T) {
	old := memoryRefreshEvery
	memoryRefreshEvery = 20 * time.Millisecond
	defer func() { memoryRefreshEvery = old }()
	w, f := memoryWorkspace(t)
	defer w.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.followMemory(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	time.Sleep(100 * time.Millisecond)
	if n, _ := f.calls(); n != 0 {
		t.Fatalf("closed view read %d times", n)
	}
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '3'})
	memWaitFor(t, "a read on open", func() bool { n, _ := f.calls(); return n >= 1 })
	memWaitFor(t, "a periodic read", func() bool { n, _ := f.calls(); return n >= 3 })
	w.handleBoardKey(KeyEvent{Type: KeyEsc})
	time.Sleep(50 * time.Millisecond)
	n, _ := f.calls()
	time.Sleep(150 * time.Millisecond)
	if m, _ := f.calls(); m != n {
		t.Fatalf("closed view kept reading: %d → %d", n, m)
	}

	// A decision reads again at once, not at the next period.
	memoryRefreshEvery = time.Hour
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '3'})
	memWaitFor(t, "a read on reopen", func() bool { m, _ := f.calls(); return m == n+1 })
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'a'})
	w.handleBoardKey(KeyEvent{Type: KeyEnter})
	memWaitFor(t, "a read after the decision", func() bool { m, _ := f.calls(); return m == n+2 })
}
