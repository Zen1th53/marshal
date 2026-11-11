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
// Board itself, before any run: an inbox entry with the contract's id,
// revision and purpose, its two buttons and NEEDS YOU in the header. y
// only arms the reply; Esc cancels it and nothing is sent; the details
// button opens the Architecture view.
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
	for _, want := range []string{"the Marshal waits for your reply", "architecture · contract hello-uz rev 1 waits for your acceptance", "Build hello-uz", "[y] accept architecture", "[b] open details", "Tell the Marshal what to build"} {
		if !strings.Contains(text, want) {
			t.Errorf("first-use Board misses %q:\n%s", want, text)
		}
	}
	if !strings.Contains(strings.Split(text, "\n")[39], "y reply") {
		t.Errorf("footer does not offer y: %q", strings.Split(text, "\n")[39])
	}

	key := func(e KeyEvent) bool { return w.handleBoardKey(e) }
	if !key(KeyEvent{Type: KeyRune, Rune: 'y'}) || w.gateArmed() == "" {
		t.Fatal("y did not arm the reply")
	}
	if text := frameText(w, 120, 40); !strings.Contains(text, "send “accept architecture” to the Marshal as your reply?") || !strings.Contains(text, "[Enter] send") {
		t.Fatalf("the armed reply does not say what Enter sends:\n%s", text)
	}
	if !key(KeyEvent{Type: KeyEsc}) || w.gateArmed() != "" {
		t.Fatal("Esc did not cancel the armed reply")
	}
	if len(w.sayEchoList()) != 0 {
		t.Fatalf("a cancelled reply was sent: %+v", w.sayEchoList())
	}
	// Any other key cancels too and is not the reply.
	key(KeyEvent{Type: KeyRune, Rune: 'y'})
	if !key(KeyEvent{Type: KeyRune, Rune: 'x'}) || w.gateArmed() != "" || len(w.sayEchoList()) != 0 {
		t.Fatal("another key did not cancel the reply")
	}

	// The details button opens the architecture the gate is about.
	w.archSource = &fakeArch{}
	clickText(t, w, 120, 40, "[b] open details")
	if !w.archView.Load() {
		t.Fatal("open details did not open the Architecture view")
	}
	w.closeArchView()

	// Accepted (or taken back): the gate goes with the canonical state.
	w.gateContractSource = func(context.Context) (*gateContract, error) { return nil, nil }
	w.refreshGates(t.Context())
	if text := frameText(w, 120, 40); strings.Contains(text, "NEEDS YOU") || strings.Contains(text, "accept architecture") || !strings.Contains(text, "NO RUN YET") {
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

// A drafted plan waiting for approval is an inbox entry on the run's Board
// with the plan view one click away; "waiting for you" replaces the state.
// A plan being drafted, being approved or a major amendment is no such gate.
func TestOwnerGateShowsDraftedPlanAwaitingApproval(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.planSource = fakePlanSource{}
	w.setMarshalPanel(draftingPanel("awaiting plan approval; /marshal approve"))
	text := frameText(w, 170, 40)
	head := strings.Split(text, "\n")[1]
	if !strings.Contains(head, "waiting for you") || strings.Contains(head, "planning") {
		t.Fatalf("header: %q", head)
	}
	for _, want := range []string{"plan · drafted plan waits for your approval · 1 task", "[y] approve the plan", "[7] open the plan"} {
		if !strings.Contains(text, want) {
			t.Errorf("Board misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "INBOX empty") {
		t.Fatalf("the inbox says nothing waits:\n%s", text)
	}
	clickText(t, w, 170, 40, "[7] open the plan")
	if !w.planView.Load() {
		t.Fatal("open the plan did not open the Plan view")
	}
	w.closePlanView()

	for _, note := range []string{"drafting a plan… codex", "approving plan…", "major amendment proposed · /marshal amend approve or deny"} {
		w.setMarshalPanel(draftingPanel(note))
		if gates := w.ownerGates(w.state.Marshal); len(gates) != 0 {
			t.Errorf("%q: gates %+v", note, gates)
		}
	}
	p := draftingPanel("running")
	p.Board.Run.State = marshal.Dispatching
	w.setMarshalPanel(p)
	if gates := w.ownerGates(w.state.Marshal); len(gates) != 0 {
		t.Errorf("a dispatching run has a plan gate: %+v", gates)
	}
}

type fakePlanSource struct{}

func (fakePlanSource) PlanOverview(context.Context, string) (store.PlanOverview, error) {
	return store.PlanOverview{}, nil
}

// The reply buttons send the owner's exact words into the docked chat
// through the Board's say path, only after the arm and the confirm: y then
// Enter for the architecture, a click then a second click for the plan.
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

	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 'y'})
	if out, _ := tmux.CapturePane(ctx, pane); strings.Contains(out, "accept architecture") {
		t.Fatalf("y alone sent the reply:\n%s", out)
	}
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyEnter})
	waitForTmuxOutput(t, pane, "got:accept architecture")
	if text := frameText(w, 120, 40); !strings.Contains(text, "sent “accept architecture” to the Marshal") || strings.Contains(strings.SplitN(text, "\n", 3)[1], "NEEDS YOU") {
		t.Fatalf("the sent gate does not wait for the Marshal:\n%s", text)
	}
	// A sent gate is not armed again by y.
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 'y'})
	if w.gateArmed() != "" || w.composing() || !strings.Contains(w.state.LastOutput, "Already sent") {
		t.Fatalf("a sent gate armed again or y leaked: armed %q composer %q", w.gateArmed(), w.composer.Text())
	}

	w.gateContractSource = func(context.Context) (*gateContract, error) { return nil, nil }
	w.refreshGates(ctx)
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
	if n := len(w.sayEchoList()); n != 2 {
		t.Fatalf("echoes %+v", w.sayEchoList())
	}
}
