package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestCancelGoalCommitsRevisionTerminationAndReceiptAtomically(t *testing.T) {
	for _, failure := range []string{"", "goal_contracts", "goal_active", "budget_trackers", "command_results", "command_audit", "goal_terminations"} {
		t.Run(failure, func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()
			g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, DesiredOutcome: "x", OriginalRequest: "unaltered", Risk: model.R1, AuthoritySource: "owner", Confirmation: model.ConfirmationApproved, Constraints: []model.Constraint{{ID: "hard", Text: "keep", IsHard: true}}}
			if err := s.SaveGoalContract(ctx, g, 0); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveBudgetTracker(ctx, "s", "g", 1, model.ConsumedBudget{ModelCalls: 2}); err != nil {
				t.Fatal(err)
			}
			record := CommandRecord{ProjectID: "p", Actor: "owner", Key: "cancel", Operation: "goal.cancel", SessionID: "s", TargetID: "g", ExpectedVersion: 1, Digest: "digest"}
			if failure != "" {
				if _, err := s.db.Exec("CREATE TRIGGER fail_cancel BEFORE INSERT ON " + failure + " BEGIN SELECT RAISE(ABORT,'injected'); END"); err != nil {
					t.Fatal(err)
				}
			}
			cancelled, err := s.CancelGoalContract(WithCommand(ctx, record), "s", "g", 1, "stop")
			active, _ := s.GetActiveGoalContract(ctx, "s")
			if failure != "" {
				if err == nil || active.Revision != 1 {
					t.Fatalf("partial cancel %+v %v", active, err)
				}
				if _, found, _ := s.CommandResult(ctx, record); found {
					t.Fatal("receipt survived")
				}
				if _, err := s.GetGoalTermination(ctx, "s", "g", 2); !errors.Is(err, model.ErrNotFound) {
					t.Fatalf("termination survived: %v", err)
				}
				return
			}
			if err != nil || cancelled.Revision != 2 || active.Confirmation != model.ConfirmationCancelled || active.OriginalRequest != g.OriginalRequest || len(active.Constraints) != 1 {
				t.Fatalf("cancel %+v %v", active, err)
			}
			term, err := s.GetGoalTermination(ctx, "s", "g", 2)
			if err != nil || term.State != model.StateCancelled || term.ConsumedBudget.ModelCalls != 2 {
				t.Fatalf("term %+v %v", term, err)
			}
			if v, found, err := s.CommandResult(ctx, record); err != nil || !found || v != 2 {
				t.Fatalf("receipt %d %v %v", v, found, err)
			}
			late := model.GoalTermination{SessionID: "s", GoalID: "g", GoalRevision: 1, State: model.StateSuccess, ReasonCode: model.ReasonGoalAchieved, CompletedAt: time.Now()}
			if err := s.SaveGoalTermination(ctx, late); err == nil {
				t.Fatal("late finalizer overwrote cancellation")
			}
			active.Confirmation = model.ConfirmationPending
			if err := s.SaveGoalContract(ctx, active, 2); err == nil {
				t.Fatal("cancelled goal edited")
			}
		})
	}
}

func TestCancelGoalRevocationCheckedInsideMutation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, DesiredOutcome: "x", Risk: model.R1, AuthoritySource: "owner", Confirmation: model.ConfirmationApproved}
	if err := s.SaveGoalContract(ctx, g, 0); err != nil {
		t.Fatal(err)
	}
	record := CommandRecord{ProjectID: "p", Actor: "owner", Key: "cancel-revoked", Operation: "goal.cancel", SessionID: "s", TargetID: "g", ExpectedVersion: 1, Digest: "digest", CapabilityGrantID: "revoked-or-missing"}
	if _, err := s.CancelGoalContract(WithCommand(ctx, record), "s", "g", 1, "stop"); err == nil {
		t.Fatal("revoked grant committed")
	}
	active, err := s.GetActiveGoalContract(ctx, "s")
	if err != nil || active.Revision != 1 {
		t.Fatalf("revoked grant changed goal %+v %v", active, err)
	}
}

func TestCancellationCannotBeOverwrittenByLateFinalizer(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, DesiredOutcome: "x", Risk: model.R1, AuthoritySource: "owner", Confirmation: model.ConfirmationApproved}
	if err := s.SaveGoalContract(ctx, g, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelGoalContract(ctx, "s", "g", 1, "operator stopped"); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []int64{1, 2} {
		for _, state := range []model.TerminationState{model.StateSuccess, model.StateCancelled} {
			term := model.GoalTermination{SessionID: "s", GoalID: "g", GoalRevision: revision, State: state, ReasonCode: model.ReasonUserCancelled, ReasonDetail: "late", CompletedAt: time.Now()}
			if err := s.SaveGoalTermination(ctx, term); err == nil {
				t.Fatalf("late finalizer wrote revision %d state %s", revision, state)
			}
		}
	}
	term, err := s.GetGoalTermination(ctx, "s", "g", 2)
	if err != nil || term.State != model.StateCancelled || term.ReasonDetail != "operator stopped" {
		t.Fatalf("cancel overwritten %+v %v", term, err)
	}
}
