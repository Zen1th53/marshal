//go:build governed

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
