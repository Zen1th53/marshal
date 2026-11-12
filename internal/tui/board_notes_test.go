package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
)

func frameText(w *Workspace, cols, rows int) string {
	lines, _, _ := w.frameLinesFull(cols, rows)
	return StripANSI(strings.Join(lines, "\n"))
}

// clickText clicks the first cell of the frame whose row contains text.
func clickText(t *testing.T, w *Workspace, cols, rows int, text string) {
	t.Helper()
	lines, _, _ := w.frameLinesFull(cols, rows)
	for y, l := range lines {
		plain := StripANSI(l)
		if x := strings.Index(plain, text); x >= 0 {
			col := VisibleLen(plain[:x])
			w.handleBoardMouse(context.Background(), KeyEvent{Type: KeyMouse, MouseRow: y + 1, MouseColumn: col + 2})
			return
		}
	}
	t.Fatalf("%q is not on screen:\n%s", text, StripANSI(strings.Join(lines, "\n")))
}

// Background messages (a re-delivery to the Marshal, a recorded owner
// instruction, a harness verify result) are one Activity line each and
// never open the ANSWER overlay or take the footer; a direct command's
// multi-line answer still opens it, and a background message arriving
// after it was read does not open it again. A click on the note shows the
// whole text.
func TestBackgroundMessagesNeverOpenTheAnswerOverlay(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	w.theme = NewTheme(ThemeNoColor, true, false)
	frameText(w, 170, 45)
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 't'})
	background := []string{
		"The Marshal was busy; sending your message again.",
		"Recorded owner instruction for the project contract. instruction_ref=operator:b64f; contract_id=hello-uz; revision=1; digest=sha256:9089.\nAnswer the pending project-contract Decision for exactly this contract with allow.\n",
		"Verified codex codex-cli 0.167.0 in sandbox through proxy; evidence PROBE-cbb5\nNative chat transport: none qualified for codex; the tmux-docked chat stays.",
	}
	for _, msg := range background {
		w.RecordActivity(msg)
	}
	text := frameText(w, 170, 45)
	if strings.Contains(text, " ANSWER ") {
		t.Fatalf("a background message opened the ANSWER overlay:\n%s", text)
	}
	for _, want := range []string{"note The Marshal was busy", "note Recorded owner instruction for the project contract", "note Verified codex codex-cli 0.167.0"} {
		if !strings.Contains(text, want) {
			t.Errorf("Activity misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Native chat transport") {
		t.Errorf("Activity shows more than one line of a message:\n%s", text)
	}
	if foot := strings.Split(text, "\n")[44]; strings.Contains(foot, "Native chat") || strings.Contains(foot, "Marshal did not answer") {
		t.Errorf("the footer shows a background message: %q", foot)
	}

	// The operator's own command: its answer opens the overlay.
	w.recordCommandResult("/status", "Run R-7f3a\nState dispatching\nTasks 5", nil)
	if text := frameText(w, 170, 45); !strings.Contains(text, " ANSWER ") || !strings.Contains(text, "State dispatching") {
		t.Fatalf("a command's answer did not open the overlay:\n%s", text)
	}
	w.handleBoardAnswerKey(KeyEvent{Type: KeyEsc})
	w.RecordActivity("Marshal decision requested: decision-1 project-contract hello-uz\nsecond line")
	if text := frameText(w, 170, 45); strings.Contains(text, " ANSWER ") {
		t.Fatalf("a background message reopened the read answer:\n%s", text)
	}

	// The whole message is a click away.
	clickText(t, w, 170, 45, "note Recorded owner instruction")
	if text := frameText(w, 170, 45); !strings.Contains(text, " ANSWER ") || !strings.Contains(text, "Answer the pending project-contract Decision") {
		t.Fatalf("a click on the note did not show it whole:\n%s", text)
	}
}

// A v verify on the first-use card ends as the provider chip's new state
// and one Activity line, never an overlay over the card.
func TestFirstUseVerifyResultIsAChipAndOneActivityLine(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	now := time.Now()
	src := &multiLineVerify{fakeVerifySource{verified: make(chan string, 4)}}
	w.provSource = src
	lookPath = func(string) (string, error) { return "/bin/true", nil }
	defer func() { lookPath = execLookPath }()
	w.loadFirstUse(context.Background())
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'v'})
	w.handleBoardKey(KeyEvent{Type: KeyEnter})
	<-src.verified
	src.providers = []store.ProviderStatus{{Provider: "codex", VerifiedAt: now, ExpiresAt: now.Add(time.Hour)}}
	waitFor(t, func() bool { return len(w.noteList()) == 1 && w.firstUse.busy == "" })
	w.refreshProviders(context.Background())
	text := frameText(w, 120, 40)
	if strings.Contains(text, " ANSWER ") || strings.Contains(text, "Native chat transport") {
		t.Fatalf("the verify result covered the card:\n%s", text)
	}
	if !strings.Contains(text, "note Verified codex codex-cli 0.167.0") || !strings.Contains(text, "codex verified") {
		t.Fatalf("no chip state and Activity line:\n%s", text)
	}
}

type multiLineVerify struct{ fakeVerifySource }

func (f *multiLineVerify) VerifyHarness(_ context.Context, provider, _ string) (string, error) {
	f.verified <- provider
	return "Verified " + provider + " codex-cli 0.167.0 in sandbox through proxy; evidence PROBE-1\nNative chat transport: none qualified for " + provider + "; the tmux-docked chat stays.", nil
}
