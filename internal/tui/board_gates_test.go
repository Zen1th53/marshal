//go:build linux

package tui

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tmux"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// walkContract is the contract the walkthrough's Marshal submitted.
func walkContract() *gateContract {
	return &gateContract{ID: "hello-uz", Revision: 1, Digest: "sha256:9089e7832c5c", Purpose: "Build hello-uz: a tiny standard-library Go CLI whose hellouz NAME invocation prints Salom, NAME!, with a table-driven test.", Submitted: time.Now()}
}

// A project contract the Marshal submitted waits for the owner on the
// Board itself, before any run: Board v2's decision card with the contract's
// purpose, a button that opens the architecture in the browser and YOUR TURN
// in the top bar. y and a click open the browser: nothing is armed and
// nothing is ever typed into the chat, because acceptance happens only in
// the browser.
func TestOwnerGateShowsPendingContractOnTheFirstUseBoard(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.gateContractSource = func(context.Context) (*gateContract, error) { return walkContract(), nil }
	w.refreshGates(t.Context())

	// Board v2 shows the gate as the decision card over the ready screen:
	// YOUR TURN and the count in the top bar, the contract's purpose, and
	// the review's buttons; nothing on it accepts the architecture.
	text := frameText(w, 120, 40)
	head := lines0(text)
	if !strings.Contains(head, "YOUR TURN") || strings.Contains(head, " READY ") || !strings.Contains(head, "inbox 1") {
		t.Fatalf("the top bar does not say the owner's turn: %q", head)
	}
	for _, want := range []string{"ACCEPT ARCHITECTURE · rev 1", "Build hello-uz", "a  open in browser", "k  comment", "esc later"} {
		if !strings.Contains(text, want) {
			t.Errorf("the decision card misses %q:\n%s", want, text)
		}
	}
	for _, gone := range []string{"accept here", "[b] open details", "architecture · architecture"} {
		if strings.Contains(text, gone) {
			t.Errorf("the decision card still shows %q:\n%s", gone, text)
		}
	}

	key := func(e KeyEvent) bool { return w.handleBoardKey(e) }
	if !key(KeyEvent{Type: KeyRune, Rune: 'y'}) || w.gateArmed() != "" {
		t.Fatal("y armed a reply instead of opening the browser")
	}
	if !strings.Contains(w.state.LastOutput, "architecture") {
		t.Fatalf("y did not try to open the architecture: %q", w.state.LastOutput)
	}
	clickText(t, w, 120, 40, "a  open in browser")
	if w.gateArmed() != "" {
		t.Fatal("a click armed a reply instead of opening the browser")
	}
	if len(w.sayEchoList()) != 0 {
		t.Fatalf("the architecture gate typed into the chat: %+v", w.sayEchoList())
	}

	// Accepted (or taken back): the gate goes with the canonical state.
	w.gateContractSource = func(context.Context) (*gateContract, error) { return nil, nil }
	w.refreshGates(t.Context())
	if text := frameText(w, 120, 40); strings.Contains(text, "YOUR TURN") || strings.Contains(text, "open in browser") || !strings.Contains(lines0(text), " READY ") {
		t.Fatalf("the gate stayed after the contract left the store:\n%s", text)
	}
	// With no gate waiting, y says why there is nothing to open instead of
	// falling through to open the composer with "to the Marshal › y".
	if !key(KeyEvent{Type: KeyRune, Rune: 'y'}) {
		t.Fatal("y fell through instead of explaining why nothing can open")
	}
	if w.composing() || w.composer.Text() != "" {
		t.Fatalf("y opened the composer: composing=%v text=%q", w.composing(), w.composer.Text())
	}
	if !strings.Contains(w.state.LastOutput, "Nothing to open yet") {
		t.Fatalf("y did not explain why nothing can open: %q", w.state.LastOutput)
	}
}

// Pressing y on the Board when nothing waits explains why in the footer and
// never opens the composer ("to the Marshal › y"), while typing y inside an
// already-open composer still inserts the letter normally.
func TestBoardYWithNoArchitectureNeverOpensComposer(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	ctx := t.Context()

	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 'y'})
	if w.composing() || w.composer.Text() != "" {
		t.Fatalf("y opened composer: composing=%v text=%q", w.composing(), w.composer.Text())
	}
	if got := w.state.LastOutput; !strings.Contains(got, "Nothing to open yet") {
		t.Fatalf("unexpected answer before architecture: %q", got)
	}

	// Once an architecture is already accepted, y says so instead of saying
	// the Marshal has not sent one.
	w.planning.Store(true)
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 'y'})
	if w.composing() || w.composer.Text() != "" {
		t.Fatalf("y opened composer after accept: composing=%v text=%q", w.composing(), w.composer.Text())
	}
	if got := w.state.LastOutput; !strings.Contains(got, "already accepted") {
		t.Fatalf("unexpected answer after architecture accepted: %q", got)
	}

	// Once the composer is already open (e.g. via s), typing y inserts it.
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 's'})
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 'y'})
	if !w.composing() || w.composer.Text() != "y" {
		t.Fatalf("y inside open composer failed: composing=%v text=%q", w.composing(), w.composer.Text())
	}
}

// fakeArch is an Architecture source with no review drafted.
type fakeArch struct{}

func (fakeArch) ArchitectureStatus(context.Context) (app.ArchitectureStatus, error) {
	return app.ArchitectureStatus{}, nil
}
func (fakeArch) Open() {}

// drafting is the walkthrough's run after the Marshal drafted its plan:
// one task, waiting for the owner's approval.
func draftingPanel(note string) *MarshalPanel {
	run := marshal.Run{State: marshal.Drafting, PlanID: "PLAN-01d4", Settings: marshal.Settings{ReworkLimit: 2}, Tasks: []marshal.Task{{PlanTaskID: "hello-cli", Title: "implement and test hello-uz", Worker: "codex", Files: []string{"main.go", "main_test.go"}}}}
	p := newMarshalPanel("RUN-1794117001273523713", "codex", run, note)
	p.Board = boardmodel.Project(boardmodel.Snapshot{RunID: p.RunID, Run: run}, nil)
	return p
}

type fakePlanSource struct{}

func (fakePlanSource) PlanOverview(context.Context, string) (store.PlanOverview, error) {
	return store.PlanOverview{}, nil
}

// The plan's reply button sends the owner's exact words into the docked chat
// through the Board's say path, only after the arm and the confirm (a click
// then a second click); the architecture gate never sends anything.
func TestOwnerGateRepliesReachTheChatExactly(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	w := realTmuxWorkspace(t)
	t.Cleanup(w.Close)
	w.screen = NewScreen(NewTerminal(strings.NewReader(""), &strings.Builder{}))
	w.homeBoard = true
	oldPoll, oldSettle := sayPoll, saySettle
	sayPoll, saySettle = 50*time.Millisecond, 80*time.Millisecond
	defer func() { sayPoll, saySettle = oldPoll, oldSettle }()
	pane := fakeChatPane(t, w)
	ctx := context.Background()
	w.gateContractSource = func(context.Context) (*gateContract, error) { return walkContract(), nil }
	w.refreshGates(ctx)

	// The architecture gate opens the browser: y and Enter never type into
	// the chat.
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 'y'})
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyEnter})
	time.Sleep(4 * saySettle)
	if out, _ := tmux.CapturePane(ctx, pane); strings.Contains(out, "accept architecture") {
		t.Fatalf("the architecture gate typed into the chat:\n%s", out)
	}
	if w.gateArmed() != "" || len(w.sayEchoList()) != 0 {
		t.Fatalf("the architecture gate armed or sent: armed %q echoes %+v", w.gateArmed(), w.sayEchoList())
	}

	w.gateContractSource = func(context.Context) (*gateContract, error) { return nil, nil }
	w.refreshGates(ctx)
	if !ownerPlanGate {
		// The shipped build has no owner plan gate, so only the architecture
		// reply above is exercised.
		return
	}
	w.setMarshalPanel(draftingPanel("awaiting plan approval; /marshal approve"))
	clickText(t, w, 170, 40, "[y] approve the plan")
	if w.gateArmed() == "" {
		t.Fatal("a click did not arm the plan reply")
	}
	if out, _ := tmux.CapturePane(ctx, pane); strings.Contains(out, "approve the plan") {
		t.Fatalf("one click sent the reply:\n%s", out)
	}
	clickText(t, w, 170, 40, "[Enter] send")
	waitForTmuxOutput(t, pane, "got:approve the plan")
	if n := len(w.sayEchoList()); n != 1 {
		t.Fatalf("echoes %+v", w.sayEchoList())
	}
}
