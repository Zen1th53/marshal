//go:build linux && governed

package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// A drafted plan waiting for approval is an inbox entry on the run's Board
// with the plan view one click away; "waiting for you" replaces the state.
// A plan being drafted, being approved or a major amendment is no such gate.
// The governed build keeps this owner plan gate; the shipped build removes it.
func TestOwnerGateShowsDraftedPlanAwaitingApproval(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.planSource = fakePlanSource{}
	w.setMarshalPanel(draftingPanel("awaiting plan approval; /marshal approve"))
	// The Web Board's plan card holds it; the home sends the owner there.
	if d := w.boardV2().Decision; d == nil || d.Kind != decisionPlan {
		t.Fatalf("no plan card: %+v", d)
	}
	text := frameText(w, 170, 40)
	if !strings.Contains(lines0(text), "YOUR TURN") || !strings.Contains(text, "open the web to decide (w)") {
		t.Fatalf("the home does not send the owner to the plan:\n%s", text)
	}

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
