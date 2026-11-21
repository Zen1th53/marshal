//go:build linux

package tui

import (
	"context"
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
	}
}

// A fresh project opens on the Board, never the classic screen; /view
// classic still reaches the classic screen and /view board comes back.
func TestFirstUseBoardIsTheDefaultScreen(t *testing.T) {
	w := emptyBoardWorkspace(t)
	// Closing waits for the background readiness probe the paint starts.
	defer w.Close()
	if !w.boardVisible() {
		t.Fatal("the Board is not the screen with no run")
	}
	// Board v2's first screen: READY in the top bar, the readiness
	// checklist, and say-what-to-build leading the key bar.
	lines, _, _ := w.frameLinesFull(120, 40)
	text := StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(lines0(text), " READY ") || !strings.Contains(text, "Now:") || !strings.Contains(text, "⏎ chat") {
		t.Fatalf("no home screen:\n%s", text)
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
	if strings.Contains(StripANSI(strings.Join(lines, "\n")), "READINESS") || w.boardVisible() {
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
	defer w.Close()
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

	// A click on agy's readiness row arms agy. The checklist lists each
	// installed provider once the off-paint probe has run.
	w.refreshV2Marshal(context.Background(), w.v2ProbeInput(w.v2LiveChat()))
	lines, _, _ = w.frameLinesFull(120, 40)
	if text := StripANSI(strings.Join(lines, "\n")); !strings.Contains(text, "READINESS") || !strings.Contains(text, "agy") {
		t.Fatalf("the readiness checklist does not list agy:\n%s", text)
	}
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

// TestPTYFreshProjectOpensOnTheBoard launches the real binary in a fresh
// project, as `marshal init` then `marshal` leave it: the first screen is
// Board v2's ready screen, never the classic screen, and s then typing
// writes a message to the Marshal, not Board keys.
func TestPTYFreshProjectOpensOnTheBoard(t *testing.T) {
	// One coding CLI is installed: the launch picker has a single candidate,
	// saves it and stays closed, so the ready screen owns the keys.
	fakeProviderCLIs(t, "codex")
	bin := buildMarshalBinary(t)
	project := initProject(t, bin)
	s := startBoardTUIInProject(t, 40, 140, bin, project, "tui")
	// Board v2's ready screen: the readiness checklist with the Marshal's
	// row, and say-what-to-build on the key bar.
	s.mustSee("READINESS")
	s.mustSee("Marshal")
	s.mustSee("say what to build")
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
