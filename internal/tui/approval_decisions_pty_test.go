//go:build linux

package tui

import (
	"context"
	"fmt"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestPTYGoalApprovalAndRejection(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	for _, approve := range []bool{true, false} {
		t.Run(fmt.Sprint(approve), func(t *testing.T) {
			project := initProject(t, bin)
			session := "SESSION-decision-pty"
			terminal := startFrozenTUIInProject(t, 40, 180, bin, project, "tui", session)
			terminal.sendLine("/goal create Fix the parser typo. Do not change the public API.")
			terminal.mustSee("[rev 1]")
			runtime, err := app.Open(context.Background(), project)
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			goal, err := runtime.Store().GetActiveGoalContract(context.Background(), session)
			if err != nil {
				t.Fatal(err)
			}
			terminal.sendLine("/approvals")
			terminal.mustSee(fmt.Sprintf("goal:%s@1", goal.ID))
			verb := "approve"
			want := model.ConfirmationApproved
			display := "CONFIRMED"
			if !approve {
				verb = "reject"
				want = model.ConfirmationCancelled
				display = "CANCELLED"
			}
			line := fmt.Sprintf("/%s goal:%s@1", verb, goal.ID)
			if !approve {
				line += " owner declined formed interpretation"
			}
			terminal.sendLine(line)
			terminal.mustSee(display)
			read, err := runtime.Store().GetActiveGoalContract(context.Background(), session)
			if err != nil || read.Revision != 2 || read.Confirmation != want {
				t.Fatalf("canonical read-back: %+v %v", read, err)
			}
			terminal.sendLine(line)
			terminal.mustSee("conflict")
			terminal.sendLine("/quit")
			if err := terminal.cmd.Wait(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
