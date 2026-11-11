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

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/inbox"
)

// demoSecurityOverview is the overview the Security view goldens paint:
// the prototype's egress, leases and audit records.
func demoSecurityOverview(now time.Time) store.SecurityOverview {
	eg := func(seq int64, decision, worker, task, run, host, reason string) store.SecurityEgress {
		return store.SecurityEgress{Sequence: seq, Decision: decision, Worker: worker, Task: task, RunID: run, Endpoint: host, Reason: reason, At: now.Add(-time.Duration(60-seq) * time.Second)}
	}
	return store.SecurityOverview{
		RunID: "R-7f3a",
		Egress: []store.SecurityEgress{
			eg(44, store.EgressWaiting, "codex-3", "T-023", "RUN-EGRESS-c3", "registry.npmjs.org:443", "outside the run's allowlist: outside manifest"),
			eg(41, store.EgressDenied, "agy-2", "T-031", "RUN-EGRESS-a2", "pypi.org:443", "not in plan"),
			eg(37, store.EgressAllowed, "claude-1", "T-018", "RUN-EGRESS-k1", "api.anthropic.com:443", "provider transport"),
			eg(30, store.EgressAllowed, "codex-1", "T-017", "RUN-EGRESS-c1", "github.com:443", "manifest"),
			eg(22, store.EgressAllowed, "agy-2", "T-031", "RUN-EGRESS-a2", "pypi.org:443", "owner: allow for run"),
		},
		Leases: []store.SecurityLease{
			{ID: "L-3", Worker: "codex-1", Task: "T-017", Purpose: "github read token", Secret: "env/GITHUB_TOKEN", State: "leased", Expires: now.Add(5 * time.Minute)},
			{ID: "L-2", Worker: "claude-1", Task: "T-018", Purpose: "provider credential", Secret: "anthropic", State: "used", Expires: now.Add(13*time.Minute + 40*time.Second)},
		},
		Audit: []store.SecurityAudit{
			{At: now.Add(-20 * time.Second), Type: "PERMISSION_DECIDED", Subject: "network pypi.org:443 · allowed"},
			{At: now.Add(-50 * time.Second), Type: "HONEYPOT_SCAN", Subject: "T-017"},
			{At: now.Add(-21 * time.Minute), Type: "MARSHAL_PLAN_APPROVED", Subject: "v2 by owner"},
		},
	}
}

// demoSecurityEdition is the edition the goldens paint, the same in
// either build.
var demoSecurityEdition = securityEdition{Mode: "Marshal · local ULTRA", Sandbox: "bwrap per lane · host fallback only when bwrap is missing", Secrets: "workers never see auth files; the broker issues short leases", Headline: "egress only through the broker", EgressNote: "every network call a worker makes in this run", LeaseNote: "short, per task, revoked when the lane ends"}

func demoSecurityPaint(now time.Time) securityPaint {
	return securityPaint{Snap: securitySnapshot{Overview: demoSecurityOverview(now), Loaded: true, At: now.Add(-4 * time.Second)}, Selected: "44", InInbox: true, Edition: demoSecurityEdition}
}

// fakeSecurityView is the Security view's source in tests: its decisions
// stand for the Board inbox's decision path.
type fakeSecurityView struct {
	mu        sync.Mutex
	overview  store.SecurityOverview
	runs      []string
	decisions []inbox.Decision
	decideErr error
}

func (f *fakeSecurityView) SecurityOverview(_ context.Context, projectID, runID string) (store.SecurityOverview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, projectID+"/"+runID)
	return f.overview, nil
}

func (f *fakeSecurityView) Decide(_ context.Context, d inbox.Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decisions = append(f.decisions, d)
	return f.decideErr
}

func (f *fakeSecurityView) calls() (int, []inbox.Decision) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs), append([]inbox.Decision(nil), f.decisions...)
}

func TestSecurityViewGoldens(t *testing.T) {
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
			v := boardVisual{board: b, now: now, theme: th, frame: anim.Frame{}, edition: "Marshal", health: map[string]string{"codex": "●", "claude": "●", "agy": "◐", "opencode": "●"}, memory: true, providers: true, verify: true, security: true, secView: true}
			v.hints = goldenHints(v)
			frame := paintSecurity(layout.Compute(tc.w, tc.h, layout.Split), th, v, demoSecurityPaint(now))
			got := strings.Join(frame, "\n") + "\n"
			path := filepath.Join("testdata", "board", "security-"+tc.name+".golden")
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
				out := filepath.Join("..", "..", "out", "security-"+strings.TrimPrefix(tc.name, "split-")+".ans")
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
			for _, want := range []string{"SECURITY", "EGRESS", "waiting", "registry.npmjs.org:443", "[a] allow for run", "[d] deny"} {
				if !strings.Contains(plain, want) {
					t.Errorf("Security view lacks %q", want)
				}
			}
			for _, key := range []string{"allow for run", "deny", "esc"} {
				if !strings.Contains(StripANSI(frame[len(frame)-1]), key) {
					t.Errorf("footer lacks %q: %s", key, StripANSI(frame[len(frame)-1]))
				}
			}
		})
	}
}

func TestSecurityViewContent(t *testing.T) {
	now := memEpoch
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), now: now}
	m := demoSecurityPaint(now)
	lines, sel := v.securityLines(m, 120)
	text := strings.Join(lines, "\n")
	for _, want := range []string{
		"read 4s ago · every 10 s while open",
		"0 trips",
		"CREDENTIAL LEASES",
		"codex-1    T-017        github read token · env/GITHUB_TOKEN · ends in 5:00",
		"claude-1   T-018        provider credential · anthropic · ends in 13:40",
		"PERMISSION_DECIDED · network pypi.org:443 · allowed",
		"[a] allow for run  [d] deny",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Security view lacks %q:\n%s", want, text)
		}
	}
	if sel < 0 || !strings.Contains(lines[sel], "registry.npmjs.org:443") || !strings.Contains(lines[sel], "waiting") {
		t.Fatalf("selected line %d", sel)
	}
	// A waiting call with no inbox request is decided in the inbox.
	m.InInbox = false
	lines, _ = v.securityLines(m, 80)
	if text := strings.Join(lines, "\n"); !strings.Contains(text, "decide it in the inbox") {
		t.Errorf("no-inbox view:\n%s", text)
	}
	// A decided row offers no keys.
	m.Selected = "41"
	lines, _ = v.securityLines(m, 120)
	if text := strings.Join(lines, "\n"); strings.Contains(text, "[a]") {
		t.Errorf("a denied row offers keys:\n%s", text)
	}
	m.Selected, m.Busy = "44", "44"
	lines, _ = v.securityLines(m, 120)
	if text := strings.Join(lines, "\n"); !strings.Contains(text, "deciding registry.npmjs.org:443…") {
		t.Errorf("busy view:\n%s", text)
	}
	m.Snap.Overview.HoneypotTrips, m.Snap.Overview.HoneypotPartial = 2, true
	m.Snap.Overview.Unconfined = &store.SecurityUnconfined{Worker: "codex-1", Task: "T-017", At: now.Add(-3 * time.Minute)}
	lines, _ = v.securityLines(m, 120)
	text = strings.Join(lines, "\n")
	for _, want := range []string{"2 trips · task stopped, do not merge · in the newest audit records", "last lane unconfined: codex-1 T-017 · 3m ago"} {
		if !strings.Contains(text, want) {
			t.Errorf("view lacks %q:\n%s", want, text)
		}
	}
	empty := securityPaint{Snap: securitySnapshot{Loaded: true, At: now}, Edition: demoSecurityEdition}
	lines, _ = v.securityLines(empty, 100)
	text = strings.Join(lines, "\n")
	for _, want := range []string{"no network calls recorded for this run yet", "no active leases", "no audit records yet"} {
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

func securityWorkspace(t *testing.T) (*Workspace, *fakeSecurityView) {
	t.Helper()
	w, _ := boardWorkspace(t)
	f := &fakeSecurityView{overview: demoSecurityOverview(time.Now())}
	w.secSource = f
	w.verSource = &fakeVerifyView{overview: demoVerifyOverview(time.Now())}
	w.provSource = &fakeProviderView{providers: demoProviders(time.Now())}
	w.memSource = &fakeMemory{overview: demoMemoryOverview(time.Now())}
	return w, f
}

func TestSecurityViewKeys(t *testing.T) {
	w, _ := securityWorkspace(t)
	defer w.Close()
	// a and d arm a decision; Enter performs it.
	press := func(r rune) bool {
		ok := w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r})
		if (r == 'a' || r == 'd') && w.secView.Load() {
			w.handleBoardKey(KeyEvent{Type: KeyEnter})
		}
		return ok
	}

	w.composer.SetText("6")
	if press('6') || w.secView.Load() {
		t.Fatal("6 opened the Security view while the composer held text")
	}
	w.composer.SetText("")
	w.boardUI.inspect = "T-017"
	if press('6') || w.secView.Load() {
		t.Fatal("6 opened the Security view over the inspector")
	}
	w.boardUI.inspect = ""
	for _, back := range []KeyEvent{{Type: KeyEsc}, {Type: KeyRune, Rune: '1'}, {Type: KeyRune, Rune: '6'}} {
		if !press('6') || !w.secView.Load() {
			t.Fatal("6 did not open the Security view")
		}
		if !w.handleBoardKey(back) || w.secView.Load() {
			t.Fatalf("%+v did not return to the Board", back)
		}
	}

	// 3, 4 and 5 go to the other views and 6 comes back from each.
	for _, tc := range []struct {
		key  rune
		open func() bool
	}{{'3', w.memView.Load}, {'4', w.provView.Load}, {'5', w.verView.Load}} {
		press('6')
		if !press(tc.key) || w.secView.Load() || !tc.open() {
			t.Fatalf("%c did not switch from the Security view", tc.key)
		}
		if !press('6') || !w.secView.Load() || tc.open() {
			t.Fatalf("6 did not switch back from view %c", tc.key)
		}
		press('1')
	}
	if w.hostWatch.Load() != nil {
		for _, r := range []rune{'2', 'h'} {
			press('6')
			if !press(r) || w.secView.Load() || !w.hostView.Load() {
				t.Fatalf("%c did not switch to the Host view", r)
			}
			if !press('6') || !w.secView.Load() || w.hostView.Load() {
				t.Fatal("6 did not switch from the Host view")
			}
			press('1')
		}
	}

	press('6')
	w.refreshSecurity(t.Context())
	selected := func() string { w.sec.mu.Lock(); defer w.sec.mu.Unlock(); return w.sec.selected }
	if selected() != "44" {
		t.Fatalf("first selection %q", selected())
	}
	press('j')
	press('j')
	if selected() != "37" {
		t.Fatalf("j moved to %q", selected())
	}
	press('k')
	if selected() != "41" {
		t.Fatalf("k moved to %q", selected())
	}

	// The hidden Board's inbox gets no keys while the view is open.
	if w.handleBoardInboxKey(t.Context(), KeyEvent{Type: KeyRune, Rune: 'd'}) {
		t.Fatal("the Board's inbox took a key under the Security view")
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyEsc}) || w.secView.Load() {
		t.Fatal("Esc did not return to the Board")
	}

	// Without a store there is no Security view.
	w.secSource = nil
	if press('6') || w.secView.Load() {
		t.Fatal("6 opened a Security view with nothing to read")
	}
}

// a and d decide a waiting call through the inbox's decision path, for the
// pending request of the same worker run and endpoint; without one the
// footer sends the owner to the inbox.
func TestSecurityDecideKeys(t *testing.T) {
	w, f := securityWorkspace(t)
	defer w.Close()
	// a and d arm a decision; Enter performs it.
	press := func(r rune) bool {
		ok := w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r})
		if (r == 'a' || r == 'd') && w.secView.Load() {
			w.handleBoardKey(KeyEvent{Type: KeyEnter})
		}
		return ok
	}
	answer := func() string { w.mu.RLock(); defer w.mu.RUnlock(); return w.state.LastOutput }
	sel := func(id string) { w.sec.mu.Lock(); w.sec.selected = id; w.sec.mu.Unlock() }
	press('6')
	w.refreshSecurity(t.Context())

	th := NewTheme(ThemeNoColor, true, true)
	w.boardFrame(w.state, th, nil, 120, 40)
	// The Board's pending network request names no worker run: it is not
	// this call's.
	if !press('a') {
		t.Fatal("a was not taken")
	}
	if a := answer(); a != "No pending request for registry.npmjs.org:443 here: decide it in the inbox" {
		t.Fatalf("a without a request: %q", a)
	}
	frame, _, _ := w.boardFrame(w.state, th, nil, 120, 40)
	if last := frame[len(frame)-1]; !strings.Contains(last, "decide it in the inbox") {
		t.Fatalf("footer: %s", last)
	}
	sel("41")
	press('d')
	if a := answer(); a != "pypi.org:443 is denied; only a waiting call is decided here" {
		t.Fatalf("d on a denied row: %q", a)
	}
	if _, d := f.calls(); len(d) != 0 {
		t.Fatalf("decided without a request: %+v", d)
	}

	req := permission.Request{Kind: "network", TaskID: "T-023", Object: "registry.npmjs.org:443", Scope: "this worker run only, network access", Who: "codex-3", Reason: "codex-3 wants to reach registry.npmjs.org:443. Allow?", RunID: "RUN-EGRESS-c3"}
	w.permissions.mu.Lock()
	w.permissions.inbox = append(w.permissions.inbox, req)
	w.permissions.mu.Unlock()
	sel("44")
	press('a')
	memWaitFor(t, "the allow", func() bool { _, d := f.calls(); return len(d) == 1 })
	_, d := f.calls()
	if d[0].RequestKey != req.Key() || d[0].Action != 'A' || !d[0].Allow || d[0].Blast != "registry.npmjs.org:443 · this worker run only, network access" {
		t.Fatalf("allow decision %+v", d[0])
	}
	memWaitFor(t, "the allow answer", func() bool { return answer() == "Allowed registry.npmjs.org:443 for the run." })

	press('d')
	memWaitFor(t, "the deny", func() bool { _, d := f.calls(); return len(d) == 2 })
	if _, d := f.calls(); d[1].Action != 'd' || d[1].Allow || d[1].RequestKey != req.Key() {
		t.Fatalf("deny decision %+v", d[1])
	}
	memWaitFor(t, "the deny answer", func() bool { return answer() == "Denied registry.npmjs.org:443." })

	// A refused decision shows why, in red.
	f.mu.Lock()
	f.decideErr = errors.New("permission request is no longer pending")
	f.mu.Unlock()
	press('a')
	memWaitFor(t, "the refusal", func() bool { return answer() == "Permission decision failed: permission request is no longer pending" })
}

// The workspace source decides through decideInbox, the Board inbox's
// path: a decision for a request that is not pending is refused there.
func TestSecurityDecideUsesInboxPath(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	err := workspaceSecurity{w}.Decide(t.Context(), inbox.Decision{RequestKey: "gone", Action: 'A', Allow: true})
	if err == nil || !strings.Contains(err.Error(), "no longer pending") {
		t.Fatalf("decision for a request not pending: %v", err)
	}
}

func TestSecurityRefreshOnlyWhileOpen(t *testing.T) {
	old := securityRefreshEvery
	securityRefreshEvery = 20 * time.Millisecond
	defer func() { securityRefreshEvery = old }()
	w, f := securityWorkspace(t)
	defer w.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.followSecurity(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	time.Sleep(100 * time.Millisecond)
	if n, _ := f.calls(); n != 0 {
		t.Fatalf("closed view read %d times", n)
	}
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '6'})
	memWaitFor(t, "a read on open", func() bool { n, _ := f.calls(); return n >= 1 })
	memWaitFor(t, "a periodic read", func() bool { n, _ := f.calls(); return n >= 3 })
	f.mu.Lock()
	run := f.runs[0]
	f.mu.Unlock()
	if run != "project/R-7f3a" {
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
	securityRefreshEvery = time.Hour
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '6'})
	memWaitFor(t, "a read on reopen", func() bool { m, _ := f.calls(); return m == n+1 })
	th := NewTheme(ThemeNoColor, true, true)
	for range 5 {
		w.boardFrame(w.state, th, nil, 120, 40)
	}
	time.Sleep(50 * time.Millisecond)
	if m, _ := f.calls(); m != n+1 {
		t.Fatalf("frames read the store: %d reads", m-n)
	}
	// A decision reads again at once, not at the next period.
	w.permissions.mu.Lock()
	w.permissions.inbox = append(w.permissions.inbox, permission.Request{Kind: "network", Object: "registry.npmjs.org:443", Scope: "this worker run only, network access", RunID: "RUN-EGRESS-c3"})
	w.permissions.mu.Unlock()
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'd'})
	w.handleBoardKey(KeyEvent{Type: KeyEnter})
	memWaitFor(t, "a read after the decision", func() bool { m, _ := f.calls(); return m == n+2 })
}

// The view names the build's edition.
func TestSecurityEditionInfo(t *testing.T) {
	ed := securityEditionInfo()
	want := "standard · ULTRA by lease"
	if editionExtreme {
		want = "Marshal"
	}
	if ed.Mode != want || ed.Sandbox == "" || ed.Secrets == "" || ed.Headline == "" || ed.EgressNote == "" || ed.LeaseNote == "" {
		t.Fatalf("edition %+v", ed)
	}
}
