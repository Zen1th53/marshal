//go:build !governed

package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// Opening the browser review on a pending contract adds no exception to the
// required-context gate: without an accepted contract, an authenticated
// owner's Marshal model turn is still refused.
func TestRequiredContextStillRefusesModelTurnWithoutAcceptedContract(t *testing.T) {
	r := openNetpolRuntime(t)
	goal := planGoal()
	goal.ProjectID = r.ProjectIdentity()
	goal.SessionID = "SESSION-required"
	if err := r.store.SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m := &MarshalCLI{Provider: "codex", Dir: r.ProjectRoot(), ProjectID: r.ProjectIdentity(), runtime: r, requiredGoalSession: goal.SessionID}
	if _, _, _, err := m.prepareRequiredMarshalModelTurn(control.Context(t.Context()), "Draft the architecture"); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatalf("model turn without an accepted contract: %v", err)
	}
}
