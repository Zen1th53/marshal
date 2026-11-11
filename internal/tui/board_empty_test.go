package tui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// firstUseVisual is the first-use card of project "demo" at a fixed time:
// codex verified, claude waiting for its first verify, agy in a limit
// window, opencode not installed, and one message queued for a busy chat.
func firstUseVisual(th *Theme, now time.Time) boardVisual {
	return boardVisual{
		now: now, theme: th, edition: "Marshal", empty: true, marshalLeads: true, project: "demo", focus: layout.Tasks,
		chips: []providerChip{
			{name: "codex", state: "verified", model: "gpt-5"},
			{name: "claude", state: "unverified"},
			{name: "agy", state: "limited", until: now.Add(40 * time.Minute)},
			{name: "opencode", state: "not installed"},
		},
		echo: []sayEcho{{id: 1, At: now.Add(-20 * time.Second), Text: "build a todo app with a REST API and tests", State: sayQueued}},
	}
}

// TestFirstUseBoardGolden is the Board of a fresh project at the three
// sizes the design names, at the docked half of a 170-column window, and in
// the ASCII theme.
func TestFirstUseBoardGolden(t *testing.T) {
	now := time.Date(2026, 11, 8, 14, 2, 31, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		cols, rows int
		mode       layout.Mode
		docked     bool
		unicode    bool
	}{
		{"first-use-home-080x24", 80, 24, layout.Home, false, true},
		{"first-use-home-120x40", 120, 40, layout.Home, false, true},
		{"first-use-home-170x45", 170, 45, layout.Home, false, true},
		{"first-use-split-085x45", 85, 45, layout.Split, true, true},
		{"first-use-ascii-080x24", 80, 24, layout.Home, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th := NewTheme(ThemeNoColor, tc.unicode, false)
			v := firstUseVisual(th, now)
			v.docked = tc.docked
			// What boardHints gives this card (rule 4 and the tail), fixed
			// here so the golden does not read the host's PATH;
			// TestBoardHintsFollowState checks the live list.
			v.hints = []string{"say.build", "firstuse.chat", "firstuse.verify", "marshal.pick", "palette", "help"}
			if tc.docked {
				v.hints = slices.Insert(v.hints, 5, "marshal.chat")
			}
			var hits []boardHit
			v.hits = &hits
			l := layout.ComputeFor(tc.cols, tc.rows, tc.mode, v.layoutCounts())
			frame := paintFirstUse(l, th, v)
			if len(frame) != tc.rows {
				t.Fatalf("%d rows", len(frame))
			}
			for y, line := range frame {
				if w := VisibleLen(line); w != tc.cols {
					t.Fatalf("row %d: %d cells: %q", y, w, line)
				}
			}
			text := StripANSI(strings.Join(frame, "\n"))
			for _, want := range []string{"Marshal", "demo", "NO RUN", "Tell the Marshal what to build", "PROVIDERS", "codex", "claude"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q:\n%s", want, text)
				}
			}
			if !tc.unicode && strings.ContainsFunc(text, func(r rune) bool { return r > 127 }) {
				t.Fatalf("ASCII theme drew a non-ASCII cell:\n%s", text)
			}
			// The call to action is a button for the chat, and the
			// unverified provider is a button that arms its verify.
			var cta, verify bool
			for _, h := range hits {
				cta = cta || h.kind == hitAction && h.id == "marshal.chat" && strings.Contains(StripANSI(frame[h.row]), "Tell the Marshal")
				verify = verify || h.kind == hitVerify && h.id == "claude"
				if h.row < 0 || h.row >= tc.rows {
					t.Fatalf("click region off the frame: %+v", h)
				}
			}
			if !cta || !verify {
				t.Fatalf("click regions: cta %v verify %v in %+v", cta, verify, hits)
			}
			checkGolden(t, tc.name, frame)
		})
	}
}

// emptyBoardWorkspace is a workspace with no run whose screen is the Board,
// as the raw terminal makes it.
func emptyBoardWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w := NewWorkspace(nil, "demo", "session")
	w.workDir = t.TempDir()
	w.theme = NewTheme(ThemeNoColor, true, false)
	w.screen = NewScreen(NewTerminal(strings.NewReader(""), &strings.Builder{}))
	w.homeBoard = true
	return w
}

// A fresh project opens on the Board, never the classic screen; /view
// classic still reaches the classic screen and /view board comes back.
func TestFirstUseBoardIsTheDefaultScreen(t *testing.T) {
	w := emptyBoardWorkspace(t)
	if !w.boardVisible() {
		t.Fatal("the Board is not the screen with no run")
	}
	lines, _, _ := w.frameLinesFull(120, 40)
	text := StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(text, "Tell the Marshal what to build") || !strings.Contains(text, "NO RUN YET") {
		t.Fatalf("no first-use card:\n%s", text)
	}
	for _, old := range []string{"Talk to the Marshal: /marshal chat", "Native sessions", "ULTRA ACTIVE"} {
		if strings.Contains(text, old) {
			t.Fatalf("classic screen text %q on the Board:\n%s", old, text)
		}
	}
	if got := w.setBoardView(true); !strings.Contains(got, "classic") {
		t.Fatal(got)
	}
	lines, _, _ = w.frameLinesFull(120, 40)
	if strings.Contains(StripANSI(strings.Join(lines, "\n")), "Tell the Marshal what to build") || w.boardVisible() {
		t.Fatal("/view classic still shows the Board")
	}
	if got := w.setBoardView(false); got != "View: Board." {
		t.Fatalf("/view board: %q", got)
	}
	if !w.boardVisible() {
		t.Fatal("/view board did not return to the Board")
	}
}

// fakeVerifySource is a Providers source that records verifies.
type fakeVerifySource struct {
	providers []store.ProviderStatus
	verified  chan string
}

func (f *fakeVerifySource) ProviderOverview(context.Context) ([]store.ProviderStatus, error) {
	return f.providers, nil
}
func (f *fakeVerifySource) VerifyHarness(_ context.Context, provider, _ string) (string, error) {
	f.verified <- provider
	return "Verified " + provider, nil
}
func (f *fakeVerifySource) RouteTask(context.Context, string, string) (string, error) {
	return "", nil
}

// v arms the verify of the next unverified provider, Enter runs it and any
// other key cancels; a click on an unverified provider arms that one. A
// verify is never one key: the probe calls the model.
func TestFirstUseVerifyKeysAndClick(t *testing.T) {
	w := emptyBoardWorkspace(t)
	now := time.Now()
	src := &fakeVerifySource{verified: make(chan string, 4), providers: []store.ProviderStatus{{Provider: "codex", VerifiedAt: now, ExpiresAt: now.Add(time.Hour)}}}
	w.provSource = src
	lookPath = func(string) (string, error) { return "/bin/true", nil }
	defer func() { lookPath = execLookPath }()
	w.loadFirstUse(context.Background())
	chips := w.firstUseChips(now)
	if chips[0].state != "verified" || chips[1].state != "unverified" {
		t.Fatalf("chips %+v", chips)
	}
	key := func(e KeyEvent) bool { return w.handleBoardKey(e) }

	if !key(KeyEvent{Type: KeyRune, Rune: 'v'}) || w.firstUseArmed() != "claude" {
		t.Fatalf("v armed %q", w.firstUseArmed())
	}
	lines, _, _ := w.frameLinesFull(120, 40)
	if !strings.Contains(StripANSI(strings.Join(lines, "\n")), "Enter verifies claude") {
		t.Fatal("the armed verify does not say Enter runs it")
	}
	if !key(KeyEvent{Type: KeyRune, Rune: 'x'}) || w.firstUseArmed() != "" {
		t.Fatal("another key did not cancel the armed verify")
	}
	select {
	case p := <-src.verified:
		t.Fatalf("cancelled verify ran for %s", p)
	default:
	}
	key(KeyEvent{Type: KeyRune, Rune: 'v'})
	if !key(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter did not run the armed verify")
	}
	select {
	case p := <-src.verified:
		if p != "claude" {
			t.Fatalf("verified %s", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("verify did not run")
	}

	// A click on agy's chip arms agy.
	lines, _, _ = w.frameLinesFull(120, 40)
	_ = lines
	var at boardHit
	for _, h := range w.boardHits.hits {
		if h.kind == hitVerify && h.id == "agy" {
			at = h
		}
	}
	if at.kind == "" {
		t.Fatalf("no click region for agy: %+v", w.boardHits.hits)
	}
	if !w.handleBoardMouse(context.Background(), KeyEvent{Type: KeyMouse, MouseRow: at.row + 1, MouseColumn: at.x0 + 1}) || w.firstUseArmed() != "agy" {
		t.Fatalf("click armed %q", w.firstUseArmed())
	}
}

// The footer of the first-use Board leads with saying what to build and
// shows the switch key when the chat is docked.
func TestFirstUseFooterShowsSwitchHint(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	th := NewTheme(ThemeNoColor, true, false)
	v := firstUseVisual(th, time.Now())
	v.docked = true
	v.hints = w.boardHints(v)
	footer := StripANSI(v.footer(120))
	if !strings.HasPrefix(footer, " s say what to build") {
		t.Fatalf("footer %q does not lead with s say what to build", footer)
	}
	for _, want := range []string{"⏎ chat", "m Marshal", "F11 chat"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer %q lacks %q", footer, want)
		}
	}
	// A run's Board names the switch key too while docked.
	rw := runHintWorkspace(t, "T-017")
	defer rw.Close()
	run := boardVisual{theme: th, now: time.Now(), docked: true}
	run.hints = rw.boardHints(run)
	if f := StripANSI(run.footer(160)); !strings.Contains(f, "F11 chat") {
		t.Fatalf("run footer %q", f)
	}
	// A click on F11 chat takes F11's own path.
	if k, ok := boardAction("marshal.chat"); !ok || k.Run == nil {
		t.Fatal("F11 chat is no button for F11")
	}
}

// TestPTYFreshProjectOpensOnTheBoard launches the real binary in a fresh
// project, as `marshal init` then `marshal` leave it: the first screen is
// the Board's first-use card, never the classic screen, and s then typing
// writes a message to the Marshal, not Board keys.
func TestPTYFreshProjectOpensOnTheBoard(t *testing.T) {
	// One coding CLI is installed: the launch picker has a single candidate,
	// saves it and stays closed, so the first-use card owns the keys.
	fakeProviderCLIs(t, "codex")
	bin := buildMarshalBinary(t)
	project := initProject(t, bin)
	s := startBoardTUIInProject(t, 40, 140, bin, project, "tui")
	s.mustSee("NO RUN YET")
	s.mustSee("Tell the Marshal what to build")
	s.mustSee("PROVIDERS")
	s.mustNotSee("Native sessions")
	s.send("s")
	s.mustSee("to the Marshal ›")
	s.send("build it")
	s.mustSee("build it")
	if strings.Contains(StripANSI(tail(s.output(), 4000)), "ARCHITECTURE") {
		t.Fatal("a letter of the message opened a Board view")
	}
	// Esc drops the message; /view classic still reaches the classic
	// screen.
	s.send("\x1b")
	s.sendLine("/view classic")
	s.mustSee("classic screen")
}
