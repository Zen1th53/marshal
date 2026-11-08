//go:build linux

package tui

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

// This is an integration qualification test: I must install goalActionBindings
// and register goal.cancel before it can pass on the candidate extreme binary.
// A PTMX skip is an unmet release qualification, never evidence of a pass.
func TestPTYBudgetMenuEditAndDestructiveGoalCancellation(t *testing.T) {
	if !editionExtreme {
		t.Skip("candidate navigation qualification requires (default build)")
	}
	openPTY(t) // Discover sandbox PTMX refusal before building a child binary.
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	const session = "SESSION-goal-budget-menu"
	terminal := startFrozenTUIInProject(t, 40, 160, bin, project, "tui", session)
	terminal.mustSee("Marshal")
	terminal.sendLine("/goal create Fix the parser typo. Do not change the public API.")
	terminal.mustSee("[rev 1]")
	terminal.send("\x0e")
	goalMenuPTYAction(t, terminal, "Model-call limit")
	terminal.mustSee("New limit (or clear)")
	// The Extreme budget command (CommandSetGoalBudget) takes the limit alone;
	// it has no operator reason, so the typed form is a single field.
	terminal.send("4\r")
	terminal.mustSee("Proceed")
	terminal.send("\x1b[C\r")
	terminal.mustSee("PENDING; confirm and create a new plan")
	revised := readGoalMenuPTY(t, project, session)
	if revised.Revision != 2 || revised.BudgetLimits == nil || revised.BudgetLimits.MaxModelCalls != 4 || revised.Confirmation != model.ConfirmationPending {
		t.Fatalf("budget readback %+v", revised)
	}
	terminal.send("\x1b")
	goalMenuPTYAction(t, terminal, "Cancel goal")
	terminal.mustSee("Reason for cancellation")
	terminal.send("stop this goal\r")
	terminal.mustSee("Proceed")
	// Confirmation opens on Cancel. Repeated Enter must not cancel the goal.
	terminal.send("\r")
	unchanged := readGoalMenuPTY(t, project, session)
	if unchanged.Revision != 2 || unchanged.Confirmation == model.ConfirmationCancelled {
		t.Fatalf("default Cancel mutated %+v", unchanged)
	}
	terminal.send("\x1b")
	goalMenuPTYAction(t, terminal, "Cancel goal")
	terminal.mustSee("Reason for cancellation")
	terminal.send("stop this goal\r")
	terminal.mustSee("Proceed")
	terminal.send("y\x1b[C\r")
	terminal.mustSee("cancelled at revision 3")
	cancelled := readGoalMenuPTY(t, project, session)
	if cancelled.Revision != 3 || cancelled.Confirmation != model.ConfirmationCancelled || cancelled.OriginalRequest != revised.OriginalRequest || len(cancelled.Constraints) != len(revised.Constraints) {
		t.Fatalf("cancel readback %+v", cancelled)
	}
}

func goalMenuPTYAction(t *testing.T, s *ptySession, title string) {
	t.Helper()
	s.send("\x0b") // Ctrl+K opens the navigation destination palette.
	s.send(title)
	s.send("\r") // Navigate to the action bar; this cannot submit.
	s.send("\r") // Open the closed typed form.
}
func readGoalMenuPTY(t *testing.T, project, session string) model.GoalContract {
	t.Helper()
	r, err := app.Open(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	g, err := r.Store().GetActiveGoalContract(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
