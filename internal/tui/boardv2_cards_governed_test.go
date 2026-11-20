//go:build linux && governed

package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Where the edition asks the owner to approve each plan, the drafted plan
// is the decision card. Enter arms the reply, a second Enter sends it
// through the plan gate's own path (sendGate), any other key cancels.
func TestDecisionCardPlanApproval(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	defer v2Cards.Delete(w)
	run := marshal.Run{State: marshal.Drafting, PlanID: "PLAN-1", Settings: marshal.Settings{ReworkLimit: 2},
		Tasks: []marshal.Task{{PlanTaskID: "T01", Title: "store", Worker: "claude", State: marshal.Queued}, {PlanTaskID: "T02", Title: "view", Worker: "codex", State: marshal.Queued}}}
	w.setMarshalPanel(newMarshalPanel("R-1", "claude", run, "plan drafted"))
	d := cardsFrame(w).Decision
	if d == nil || d.Kind != "plan" || d.Title != "Approve plan" || !strings.Contains(d.Summary, "2 tasks") {
		t.Fatalf("plan card %+v", d)
	}
	if d.Actions[0].Key != "⏎" || d.Actions[0].Label != gateReplyPlan {
		t.Fatalf("actions %+v", d.Actions)
	}
	if !w.handleV2CardKey(KeyEvent{Type: KeyEnter}) || w.v2PlanArmed() == "" {
		t.Fatal("Enter did not arm the reply")
	}
	if d := cardsFrame(w).Decision; !strings.HasPrefix(d.Actions[0].Label, "send ") {
		t.Fatalf("armed label %q", d.Actions[0].Label)
	}
	if !w.handleV2CardKey(KeyEvent{Type: KeyRune, Rune: 'q'}) || w.v2PlanArmed() != "" || lastOutput(w) != "Reply not sent." {
		t.Fatalf("another key did not cancel: %q", lastOutput(w))
	}
	w.handleV2CardKey(KeyEvent{Type: KeyEnter})
	w.handleV2CardKey(KeyEvent{Type: KeyEnter})
	// No chat is open in this test: sendGate says so instead of sending.
	if !strings.Contains(lastOutput(w), "was not sent; /marshal chat starts one") {
		t.Fatalf("the second Enter did not go through sendGate: %q", lastOutput(w))
	}
}
