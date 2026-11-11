package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/usage"
)

// demoProviders is the overview the Providers view goldens paint: the
// prototype's four providers, one ready, one in a limit, one expired and
// one whose cooldown has ended.
func demoProviders(now time.Time) []store.ProviderStatus {
	return []store.ProviderStatus{
		{Provider: "agy", Model: "gemini-3.8-flash-low", VerifiedAt: now.Add(-26 * time.Hour), ExpiresAt: now.Add(-2 * time.Hour), Expired: true, ActiveLanes: 2},
		{Provider: "claude", Model: "claude-opus-5-5", VerifiedAt: now.Add(-4 * time.Hour), ExpiresAt: now.Add(20 * time.Hour), ActiveLanes: 2},
		{Provider: "codex", Model: "gpt-5.5-codex", VerifiedAt: now.Add(-6 * time.Hour), ExpiresAt: now.Add(18 * time.Hour), ActiveLanes: 3, LastLimitAt: now.Add(-40 * time.Minute), LastLimitReason: "usage limit reached", LimitUntil: now.Add(5 * time.Hour)},
		{Provider: "opencode", Model: "deepseek", VerifiedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(22 * time.Hour), ActiveLanes: 1},
	}
}

func demoProviderPaint(now time.Time) providerPaint {
	return providerPaint{
		Snap:     providerSnapshot{Providers: demoProviders(now), Loaded: true, At: now.Add(-3 * time.Second)},
		Selected: "codex",
		Quotas: map[string]usage.Quota{
			"claude": {Provider: "claude", Known: true, AsOf: now.Add(-5 * time.Minute), Windows: []usage.Window{
				{Label: "5h", LeftPercent: 88, ResetsAt: now.Add(3 * time.Hour)},
				{Label: "wk", LeftPercent: 58, ResetsAt: now.Add(4*24*time.Hour + 2*time.Hour)},
			}},
			"codex":    {Provider: "codex", Known: true, AsOf: now.Add(-5 * time.Minute), Windows: []usage.Window{{Label: "wk", LeftPercent: 42, ResetsAt: now.Add(4 * 24 * time.Hour)}}},
			"agy":      {Provider: "agy", Reason: usage.NotExposed},
			"opencode": {Provider: "opencode", Reason: usage.NotExposed},
		},
	}
}

// fakeProviderView is the Providers view's source in tests.
type fakeProviderView struct {
	mu        sync.Mutex
	providers []store.ProviderStatus
	overviews int
	verifies  []string
	routes    []string
}

func (f *fakeProviderView) RouteTask(_ context.Context, task, worker string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes = append(f.routes, task+" "+worker)
	return "Task " + task + " reassigned to " + worker, nil
}

func (f *fakeProviderView) routed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.routes...)
}

func (f *fakeProviderView) ProviderOverview(context.Context) ([]store.ProviderStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overviews++
	return f.providers, nil
}

func (f *fakeProviderView) VerifyHarness(_ context.Context, provider, model string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifies = append(f.verifies, provider+" "+model)
	return "Verified " + provider + " 1.0 in sandbox through proxy; evidence ev-1", nil
}

func (f *fakeProviderView) calls() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.overviews, append([]string(nil), f.verifies...)
}

func TestProviderViewGoldens(t *testing.T) {
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
			v := boardVisual{board: b, now: now, theme: th, frame: anim.Frame{}, edition: "Marshal", health: map[string]string{"codex": "●", "claude": "●", "agy": "◐", "opencode": "●"}, memory: true, providers: true, provView: true}
			v.hints = goldenHints(v)
			frame := paintProviders(layout.Compute(tc.w, tc.h, layout.Split), th, v, demoProviderPaint(now))
			got := strings.Join(frame, "\n") + "\n"
			path := filepath.Join("testdata", "board", "providers-"+tc.name+".golden")
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
				out := filepath.Join("..", "..", "out", "providers-"+strings.TrimPrefix(tc.name, "split-")+".ans")
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
			for _, want := range []string{"PROVIDERS", "agy", "claude", "codex", "opencode", "ready", "limit", "re-verify", "verify harness"} {
				if !strings.Contains(plain, want) {
					t.Errorf("Providers view lacks %q", want)
				}
			}
			for _, key := range []string{"verify", "esc"} {
				if !strings.Contains(StripANSI(frame[len(frame)-1]), key) {
					t.Errorf("footer lacks %q: %s", key, StripANSI(frame[len(frame)-1]))
				}
			}
		})
	}
}

func TestProviderViewContent(t *testing.T) {
	now := memEpoch
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), now: now}
	m := demoProviderPaint(now)
	lines, sel := v.providerLines(m, 120)
	text := strings.Join(lines, "\n")
	until := providerUntil(now.Add(5*time.Hour), now)
	for _, want := range []string{
		"read 3s ago · every 10 s while open",
		"usage limit reached",
		until,
		"26h00m ago",
		"waits for the limit to end",
		"running codex lanes finish; new work waits for " + until + " or a ready one.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Providers view lacks %q:\n%s", want, text)
		}
	}
	row := func(name string) string {
		glyph := providerGlyph(providerOf(name), v.unicode())
		if glyph == "" {
			glyph = "●"
		}
		for _, l := range lines {
			if strings.HasPrefix(l, glyph+" "+name+" ") {
				return l
			}
		}
		t.Fatalf("no row for %s:\n%s", name, text)
		return ""
	}
	for name, state := range map[string]string{"agy": "re-verify", "claude": "ready", "codex": "limit", "opencode": "ready"} {
		if !strings.HasSuffix(strings.TrimSpace(row(name)), state) {
			t.Errorf("%s row does not end in %q: %s", name, state, row(name))
		}
	}
	if sel < 0 || !strings.Contains(lines[sel], "codex") {
		t.Fatalf("selected line %d", sel)
	}
	m.Selected, m.Busy = "agy", "agy"
	lines, _ = v.providerLines(m, 80)
	text = strings.Join(lines, "\n")
	for _, want := range []string{"verifying agy…", "agy's profile expired"} {
		if !strings.Contains(text, want) {
			t.Errorf("busy view lacks %q:\n%s", want, text)
		}
	}
	empty := providerPaint{Snap: providerSnapshot{Loaded: true, At: now}}
	lines, _ = v.providerLines(empty, 100)
	if !strings.Contains(strings.Join(lines, "\n"), "no harness profiles and no lanes yet") {
		t.Error("an empty Providers view says nothing")
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

func TestProvidersViewLimitColumn(t *testing.T) {
	now := memEpoch
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), now: now}
	m := demoProviderPaint(now)
	m.Selected = "agy"
	m.Quotas["opencode"] = usage.Quota{Provider: "opencode", Known: true, AsOf: now.Add(-3 * time.Hour), Stale: true, Windows: []usage.Window{{Label: "wk", LeftPercent: 35, ResetsAt: now.Add(2 * 24 * time.Hour)}}}
	lines, _ := v.providerLines(m, 120)
	text := strings.Join(strings.Fields(StripANSI(strings.Join(lines, "\n"))), " ")
	for _, want := range []string{
		"codex", "usage limit reached", providerUntil(now.Add(5*time.Hour), now),
		"claude", "▓▓░ 42% used · wk", providerUntil(now.Add(4*24*time.Hour+2*time.Hour), now),
		"opencode", "▓▓░ 65% used · wk · 3h old", providerUntil(now.Add(2*24*time.Hour), now),
		"agy", "unavailable", "agy does not write its usage limits where Marshal can read them.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Providers view lacks %q:\n%s", want, text)
		}
	}
}

func providerWorkspace(t *testing.T) (*Workspace, *fakeProviderView) {
	t.Helper()
	w, _ := boardWorkspace(t)
	f := &fakeProviderView{providers: demoProviders(time.Now())}
	w.provSource = f
	w.memSource = &fakeMemory{overview: demoMemoryOverview(time.Now())}
	return w, f
}

func TestProviderViewKeys(t *testing.T) {
	w, f := providerWorkspace(t)
	defer w.Close()
	// v arms the probe; Enter runs it.
	press := func(r rune) bool {
		ok := w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r})
		if r == 'v' {
			w.handleBoardKey(KeyEvent{Type: KeyEnter})
		}
		return ok
	}
	key := func(k KeyType) bool { return w.handleBoardKey(KeyEvent{Type: k}) }
	answer := func() string { w.mu.RLock(); defer w.mu.RUnlock(); return w.state.LastOutput }

	w.composer.SetText("4")
	if press('4') || w.provView.Load() {
		t.Fatal("4 opened the Providers view while the composer held text")
	}
	w.composer.SetText("")
	w.boardUI.inspect = "T-017"
	if press('4') || w.provView.Load() {
		t.Fatal("4 opened the Providers view over the inspector")
	}
	w.boardUI.inspect = ""
	for _, back := range []KeyEvent{{Type: KeyEsc}, {Type: KeyRune, Rune: '1'}, {Type: KeyRune, Rune: '4'}} {
		if !press('4') || !w.provView.Load() {
			t.Fatal("4 did not open the Providers view")
		}
		if !w.handleBoardKey(back) || w.provView.Load() {
			t.Fatalf("%+v did not return to the Board", back)
		}
	}

	// 3 goes to the Memory view and 4 comes back; Host view keys likewise.
	press('4')
	if !press('3') || w.provView.Load() || !w.memView.Load() {
		t.Fatal("3 did not switch to the Memory view")
	}
	if !press('4') || !w.provView.Load() || w.memView.Load() {
		t.Fatal("4 did not switch from the Memory view")
	}
	if w.hostWatch.Load() != nil {
		if !press('2') || w.provView.Load() || !w.hostView.Load() {
			t.Fatal("2 did not switch to the Host view")
		}
		if !press('4') || !w.provView.Load() || w.hostView.Load() {
			t.Fatal("4 did not switch from the Host view")
		}
	}

	w.refreshProviders(t.Context())
	selected := func() string { w.prov.mu.Lock(); defer w.prov.mu.Unlock(); return w.prov.selected }
	if selected() != "agy" {
		t.Fatalf("first selection %q", selected())
	}
	press('j')
	press('j')
	if selected() != "codex" {
		t.Fatalf("j moved to %q", selected())
	}

	// codex is in a limit: v says so and does not run the probe.
	if !press('v') {
		t.Fatal("v was not taken")
	}
	if _, v := f.calls(); len(v) != 0 {
		t.Fatalf("verify ran during a limit: %v", v)
	}
	if a := answer(); !strings.Contains(a, "codex: in limit until ") || !strings.HasSuffix(a, "; verify would fail") {
		t.Fatalf("limit answer %q", a)
	}

	// A first frame settles the run's note, so the answer shows after it.
	th := NewTheme(ThemeNoColor, true, true)
	w.boardFrame(w.state, th, nil, 120, 40)
	press('k')
	if selected() != "claude" {
		t.Fatalf("k moved to %q", selected())
	}
	press('v')
	memWaitFor(t, "verify", func() bool { _, v := f.calls(); return len(v) == 1 })
	if _, v := f.calls(); v[0] != "claude claude-opus-5-5" {
		t.Fatalf("verify %v", v)
	}
	memWaitFor(t, "the verify answer", func() bool { return strings.HasPrefix(answer(), "Verified claude") })
	frame, _, _ := w.boardFrame(w.state, th, nil, 120, 40)
	if last := frame[len(frame)-1]; !strings.Contains(last, "Verified claude 1.0") {
		t.Fatalf("footer does not show the verify answer: %s", last)
	}

	// The hidden Board's inbox gets no keys while the view is open.
	if w.handleBoardInboxKey(t.Context(), KeyEvent{Type: KeyRune, Rune: 'd'}) {
		t.Fatal("the Board's inbox took a key under the Providers view")
	}
	if !key(KeyEsc) || w.provView.Load() {
		t.Fatal("Esc did not return to the Board")
	}

	// Without a store there is no Providers view.
	w.provSource = nil
	if press('4') || w.provView.Load() {
		t.Fatal("4 opened a Providers view with nothing to read")
	}
}

// The verify key goes through the /harness verify command path.
func TestProviderVerifyUsesHarnessCommand(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	if w.cmd == nil {
		t.Skip("no command handler")
	}
	_, err := workspaceProviders{w}.VerifyHarness(t.Context(), "codex", "gpt-5.5-codex")
	if err == nil || !strings.Contains(err.Error(), "no runtime") {
		t.Fatalf("verify without a runtime: %v", err)
	}
}

func TestProviderRefreshOnlyWhileOpen(t *testing.T) {
	old := providerRefreshEvery
	providerRefreshEvery = 20 * time.Millisecond
	defer func() { providerRefreshEvery = old }()
	w, f := providerWorkspace(t)
	defer w.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.followProviders(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	time.Sleep(100 * time.Millisecond)
	if n, _ := f.calls(); n != 0 {
		t.Fatalf("closed view read %d times", n)
	}
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '4'})
	memWaitFor(t, "a read on open", func() bool { n, _ := f.calls(); return n >= 1 })
	memWaitFor(t, "a periodic read", func() bool { n, _ := f.calls(); return n >= 3 })
	w.handleBoardKey(KeyEvent{Type: KeyEsc})
	time.Sleep(50 * time.Millisecond)
	n, _ := f.calls()
	time.Sleep(150 * time.Millisecond)
	if m, _ := f.calls(); m != n {
		t.Fatalf("closed view kept reading: %d → %d", n, m)
	}

	// Painting does not read: frames come from the last snapshot.
	providerRefreshEvery = time.Hour
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '4'})
	memWaitFor(t, "a read on reopen", func() bool { m, _ := f.calls(); return m == n+1 })
	th := NewTheme(ThemeNoColor, true, true)
	for range 5 {
		w.boardFrame(w.state, th, nil, 120, 40)
	}
	time.Sleep(50 * time.Millisecond)
	if m, _ := f.calls(); m != n+1 {
		t.Fatalf("frames read the store: %d reads", m-n)
	}
	// A verify reads again at once, not at the next period.
	w.prov.mu.Lock()
	w.prov.selected = "claude"
	w.prov.mu.Unlock()
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'v'})
	w.handleBoardKey(KeyEvent{Type: KeyEnter})
	memWaitFor(t, "a read after the verify", func() bool { m, _ := f.calls(); return m == n+2 })
}

// r routes the run's movable tasks around the selected provider through
// /marshal reassign, only after Enter; Esc cancels.
func TestProviderRouteAroundKey(t *testing.T) {
	w, f := providerWorkspace(t)
	defer w.Close()
	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	key := func(k KeyType) bool { return w.handleBoardKey(KeyEvent{Type: k}) }
	answer := func() string { w.mu.RLock(); defer w.mu.RUnlock(); return w.state.LastOutput }
	sel := func(id string) { w.prov.mu.Lock(); w.prov.selected = id; w.prov.mu.Unlock() }
	press('4')
	w.refreshProviders(t.Context())
	th := NewTheme(ThemeNoColor, true, true)
	w.boardFrame(w.state, th, nil, 120, 40)

	// claude's tasks are all dispatched: running lanes are never moved.
	sel("claude")
	if !press('r') || len(f.routed()) != 0 || !strings.Contains(answer(), "claude: no task of the run waits to move") {
		t.Fatalf("r on claude: %q %v", answer(), f.routed())
	}

	// codex is in a limit; its escalated T-023 moves to claude, the first
	// other provider that is ready.
	sel("codex")
	if !press('r') {
		t.Fatal("r was not taken")
	}
	// The queued T-033 has no worker yet, so it stays in either edition.
	if a := answer(); a != "Route T-023 around codex to claude? Enter moves them · Esc cancels" {
		t.Fatalf("r prompt %q", a)
	}
	if len(f.routed()) != 0 {
		t.Fatalf("r routed without confirmation: %v", f.routed())
	}
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "Enter to route T-023 to claude · Esc cancels") {
		t.Fatalf("view does not show the confirmation:\n%s", got)
	}
	if !key(KeyEsc) || !w.provView.Load() {
		t.Fatal("Esc on an armed route left the view")
	}
	key(KeyEnter)
	if len(f.routed()) != 0 {
		t.Fatalf("Enter after Esc routed: %v", f.routed())
	}
	// A move cancels an armed route too.
	press('r')
	press('j')
	press('k')
	key(KeyEnter)
	if len(f.routed()) != 0 {
		t.Fatalf("Enter after a move routed: %v", f.routed())
	}
	press('r')
	if !key(KeyEnter) {
		t.Fatal("Enter on an armed route was not taken")
	}
	memWaitFor(t, "the route", func() bool { return len(f.routed()) == 1 })
	if r := f.routed(); r[0] != "T-023 claude" {
		t.Fatalf("route %v", r)
	}
	memWaitFor(t, "the route answer", func() bool { return answer() == "Routed T-023 from codex to claude; /marshal resume to continue." })
}

// The route key goes through the /marshal reassign command path.
func TestProviderRouteUsesReassignCommand(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	if w.cmd == nil {
		t.Skip("no command handler")
	}
	if _, err := (workspaceProviders{w}).RouteTask(t.Context(), "T-023", ""); err == nil || !strings.Contains(err.Error(), "/marshal reassign") {
		t.Fatalf("an empty worker did not reach /marshal reassign: %v", err)
	}
}

// v only arms the harness probe: nothing runs until Enter, and Esc cancels.
func TestProviderVerifyWaitsForEnter(t *testing.T) {
	w, f := providerWorkspace(t)
	defer w.Close()
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '4'})
	w.refreshProviders(t.Context())
	w.prov.mu.Lock()
	w.prov.selected = "agy"
	w.prov.mu.Unlock()
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'v'}) {
		t.Fatal("v was not taken")
	}
	if _, v := f.calls(); len(v) != 0 {
		t.Fatalf("v ran the probe before Enter: %v", v)
	}
	if w.prov.paint().Verify != "agy" {
		t.Fatal("v did not arm the probe")
	}
	w.handleBoardKey(KeyEvent{Type: KeyEsc})
	if w.prov.paint().Verify != "" || !w.provView.Load() {
		t.Fatal("Esc did not cancel the armed probe")
	}
	if _, v := f.calls(); len(v) != 0 {
		t.Fatalf("Esc ran the probe: %v", v)
	}
}
