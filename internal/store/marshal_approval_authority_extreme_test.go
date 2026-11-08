//go:build !governed

package store

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestCanonicalMarshalApprovalChecksGoalAndContractInsideWriteTransaction(t *testing.T) {
	for _, change := range []string{"none", "goal", "contract"} {
		t.Run(change, func(t *testing.T) {
			s, contract, req := planningFixture(t)
			goal, err := s.GetGoalContract(t.Context(), req.Binding.GoalID, req.Binding.GoalRevision)
			if err != nil {
				t.Fatal(err)
			}
			previous := goal.Revision
			goal.Revision++
			goal.Confirmation = model.ConfirmationApproved
			goal.RevisionReason = "owner confirmed canonical planning goal"
			if err := s.SaveGoalContract(t.Context(), goal, previous); err != nil {
				t.Fatal(err)
			}
			goal, err = s.GetActiveGoalContract(t.Context(), goal.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := s.ReadAcceptedProjectContract(t.Context(), contract.Project)
			if err != nil {
				t.Fatal(err)
			}
			ref := projectcontext.ContractRef{ID: contract.ID, Revision: contract.Revision, Digest: accepted.Digest}
			p := plan.ExecutionPlan{ID: "PLAN-atomic-approval", ProjectID: planTestProject, Goal: plan.BindGoal(goal), Version: 1, State: plan.StateReady, Mode: plan.ModeStandard, RequiredProjectContract: &ref}
			run := marshal.Run{PlanID: p.ID, PlanVersion: 1, State: marshal.Drafting, Settings: marshal.DefaultSettings()}
			if err := s.SaveMarshalPlanState(t.Context(), contract.Project.StorageID, "RUN-atomic-approval", p, 0, run, 0, nil); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "goal":
				changed := goal
				changed.Revision++
				changed.RevisionReason = "owner confirmed a new revision"
				if err := s.SaveGoalContract(t.Context(), changed, goal.Revision); err != nil {
					t.Fatal(err)
				}
			case "contract":
				if _, err := s.db.ExecContext(t.Context(), `UPDATE extreme_project_contract_active SET contract_id=NULL,revision=NULL,digest=NULL,bootstrap_state='review_pending' WHERE project_id=?`, contract.Project.CanonicalID); err != nil {
					t.Fatal(err)
				}
			}
			approved := p
			approved.Version = 2
			approved.State = plan.StateApproved
			run.PlanVersion = 2
			run.State = marshal.Approved
			run.Process05Bound = true
			err = s.SaveMarshalPlanState(t.Context(), contract.Project.StorageID, "RUN-atomic-approval", approved, 1, run, 1, nil)
			if change == "none" {
				if err != nil {
					t.Fatalf("current authority rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("stale Goal or contract reached approved state")
			}
			if _, err := s.GetPlan(t.Context(), p.ID, 2); !errors.Is(err, plan.ErrPlanNotFound) {
				t.Fatalf("stale plan version escaped rollback: %v", err)
			}
			saved, err := s.GetMarshalRun(t.Context(), contract.Project.StorageID, "RUN-atomic-approval")
			if err != nil || saved.Revision != 1 || saved.Value.State != marshal.Drafting {
				t.Fatalf("stale run escaped rollback: %+v %v", saved, err)
			}
		})
	}
}
