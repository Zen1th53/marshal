//go:build linux

package tui

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestPTYGoalCreateEditConstraintRestart(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	const session = "SESSION-goal-edit-pty"
	terminal := startFrozenTUIInProject(t, 40, 180, bin, project, "tui", session)
	terminal.sendLine("/goal create Fix the parser typo. Do not change the public API.")
	terminal.mustSee("[rev 1]")
	terminal.sendLine("/goal edit Fix only the parser typo")
	terminal.mustSee("[rev 2] PENDING: Fix only the parser typo")
	terminal.sendLine("/goal add-constraint Stay offline")
	terminal.mustSee("[rev 3] PENDING: Fix only the parser typo")
	terminal.sendLine("/goal constraints")
	terminal.mustSee("Stay offline")
	terminal.sendLine("/goal unknown should not mutate")
	terminal.mustSee("Usage: /goal create")
	terminal.sendLine("/quit")
	if err := terminal.cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	restarted := startFrozenTUIInProject(t, 40, 180, bin, project, "tui", session)
	restarted.sendLine("/goal")
	restarted.mustSee("Active Goal [v3]: Fix only the parser typo")
	restarted.sendLine("/goal constraints")
	restarted.mustSee("Stay offline")
	runtime, err := app.Open(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	stored, err := runtime.Store().GetActiveGoalContract(context.Background(), session)
	if err != nil || stored.Revision != 3 || stored.Confirmation != model.ConfirmationPending || stored.OriginalRequest != "Fix the parser typo. Do not change the public API." || len(stored.Constraints) != 2 {
		t.Fatalf("restart: %+v %v", stored, err)
	}
	for rev := int64(1); rev <= 3; rev++ {
		if _, err := runtime.Store().GetGoalContract(context.Background(), stored.ID, rev); err != nil {
			t.Fatalf("history %d: %v", rev, err)
		}
	}
}
