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
	// The home screen: From you, the Marshal on its line, and the key bar.
	s.mustSee("From you:")
	s.mustSee("Marshal")
	s.mustSee("⏎ chat")
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
