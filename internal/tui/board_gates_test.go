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
