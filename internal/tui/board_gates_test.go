package tui

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

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
// Board itself, before any run: an inbox entry with the contract's purpose,
// one button that opens the architecture in the browser and NEEDS YOU in the
// header. y and a click open the browser: nothing is armed and nothing is
// ever typed into the chat, because acceptance happens only in the browser.
func TestOwnerGateShowsPendingContractOnTheFirstUseBoard(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.gateContractSource = func(context.Context) (*gateContract, error) { return walkContract(), nil }
	w.refreshGates(t.Context())

	text := frameText(w, 120, 40)
	head := strings.Split(text, "\n")[1]
	if !strings.Contains(head, "NEEDS YOU") || strings.Contains(head, "NO RUN YET") || !strings.Contains(head, "inbox 1") {
		t.Fatalf("header does not say the Marshal needs the owner: %q", head)
	}
	for _, want := range []string{"the Marshal waits for your reply", "architecture · architecture waits for you in the browser", "Build hello-uz", "[y] open in browser", "Tell the Marshal what to build"} {
		if !strings.Contains(text, want) {
			t.Errorf("first-use Board misses %q:\n%s", want, text)
		}
	}
	for _, gone := range []string{"accept architecture", "[b] open details"} {
		if strings.Contains(text, gone) {
			t.Errorf("first-use Board still shows %q:\n%s", gone, text)
		}
	}

	key := func(e KeyEvent) bool { return w.handleBoardKey(e) }
	if !key(KeyEvent{Type: KeyRune, Rune: 'y'}) || w.gateArmed() != "" {
		t.Fatal("y armed a reply instead of opening the browser")
	}
	if !strings.Contains(w.state.LastOutput, "architecture") {
		t.Fatalf("y did not try to open the architecture: %q", w.state.LastOutput)
	}
	clickText(t, w, 120, 40, "[y] open in browser")
	if w.gateArmed() != "" {
		t.Fatal("a click armed a reply instead of opening the browser")
	}
	if len(w.sayEchoList()) != 0 {
		t.Fatalf("the architecture gate typed into the chat: %+v", w.sayEchoList())
	}

	// Accepted (or taken back): the gate goes with the canonical state.
	w.gateContractSource = func(context.Context) (*gateContract, error) { return nil, nil }
	w.refreshGates(t.Context())
	if text := frameText(w, 120, 40); strings.Contains(text, "NEEDS YOU") || strings.Contains(text, "open in browser") || !strings.Contains(text, "NO RUN YET") {
		t.Fatalf("the gate stayed after the contract left the store:\n%s", text)
	}
	if key(KeyEvent{Type: KeyRune, Rune: 'y'}) {
		t.Fatal("y was taken with no gate waiting")
	}
}

// fakeArch is an Architecture source with nothing accepted.
type fakeArch struct{}

func (fakeArch) ArchitectureOverview(context.Context, string) (store.ArchitectureOverview, error) {
	return store.ArchitectureOverview{}, nil
}
func (fakeArch) OpenPage(context.Context, string, []byte) (string, error) { return "", nil }

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
