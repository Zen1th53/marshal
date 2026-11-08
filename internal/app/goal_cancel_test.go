package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
)

// Integration registers goal.cancel in OpenLocalControl; worker tests install
// only this scratch-project grant so no shared registration is changed here.
func goalCancelFixture(t *testing.T, state model.ConfirmationState) (*Runtime, context.Context, CommandEnvelope, capability.GrantID) {
	t.Helper()
	r := runtimeForPlan(t)
	ctx := context.Background()
	g := planGoal()
	g.ProjectID = r.ProjectIdentity()
	g.Confirmation = state
	if err := r.store.SaveGoalContract(ctx, g, 1); err != nil {
		t.Fatal(err)
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := local.Context(ctx)
	p, _ := auth.LocalFromContext(owner)
	grant := capability.Grant{ID: "cap-cancel-fixture", Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(r.ProjectIdentity()), Kind: capability.KindFilesystemWrite, Scope: capability.Scope{Resource: r.layout.Database, Actions: []string{"goal.cancel"}}, IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour), Issuer: capability.SubjectID(p.ID())}
	if err := r.store.PutCapabilityGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	return r, owner, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: g.SessionID, TargetID: g.ID, ExpectedVersion: 2, IdempotencyKey: "cancel-fixture"}, grant.ID
}

func TestCancelledGoalCannotBeApprovedEditedOrResumed(t *testing.T) {
	for _, state := range []model.ConfirmationState{model.ConfirmationPending, model.ConfirmationApproved, model.ConfirmationDelegated, model.ConfirmationNeedsInput} {
		t.Run(string(state), func(t *testing.T) {
			r, ctx, e, _ := goalCancelFixture(t, state)
			if _, err := r.CommandCancelGoal(context.Background(), e, "stop"); !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("worker cancelled: %v", err)
			}
			result, err := r.CommandCancelGoal(ctx, e, "stop")
			if err != nil {
				t.Fatal(err)
			}
			if result.Goal.Revision != 3 || result.Termination.State != model.StateCancelled {
				t.Fatalf("cancel %+v", result)
			}
			e.ExpectedVersion = 3
			e.IdempotencyKey = "after-cancel"
			if _, err := r.CommandEditGoal(ctx, e, GoalEdit{DesiredOutcome: "revive", Reason: "try"}); err == nil {
				t.Fatal("edit resurrected cancelled goal")
			}
			if _, err := r.CommandReviseGoal(ctx, e, "revive", "try"); err == nil {
				t.Fatal("revise resurrected cancelled goal")
			}
			if _, err := r.ApproveGoal(ctx, e.SessionID, 3); err == nil {
				t.Fatal("approved cancelled goal")
			}
			if err := r.ValidateGoalDispatch(ctx, e.ProjectID, e.SessionID, e.TargetID, 3); err == nil {
				t.Fatal("cancelled goal dispatched")
			}
		})
	}
}

func TestCancelGoalRestartReplayAndRevocation(t *testing.T) {
	r, ctx, e, _ := goalCancelFixture(t, model.ConfirmationApproved)
	result, err := r.CommandCancelGoal(ctx, e, "stop")
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.CommandCancelGoal(ctx, e, "stop")
	if err != nil || again.Goal.Revision != result.Goal.Revision {
		t.Fatalf("replay %+v %v", again, err)
	}
	if _, err := r.CommandCancelGoal(ctx, e, "different"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed payload %v", err)
	}
	projectRoot := r.ProjectRoot()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	local, err := reopened.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(context.Background())
	r = reopened
	again, err = r.CommandCancelGoal(ctx, e, "stop")
	if err != nil || again.Goal.Revision != 3 {
		t.Fatalf("restart replay %+v %v", again, err)
	}
	if err := revokeAllActionGrants(ctx, r.store, "goal.cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommandCancelGoal(ctx, e, "stop"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("revoked replay %v", err)
	}
}

func TestBudgetPatchPreservesOtherLimitsAndConsumption(t *testing.T) {
	r, ctx, e, _ := goalCancelFixture(t, model.ConfirmationApproved)
	tokens := int64(100)
	cost := 2.5
	limits := model.BudgetLimit{MaxTotalTokens: &tokens, MaxCostUSD: &cost, MaxModelCalls: 10, VerificationReserveCalls: 2, MaxHandoffs: 4}
	g, err := r.CommandSetGoalBudget(ctx, e, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.store.SaveBudgetTracker(ctx, e.SessionID, e.TargetID, g.Revision, model.ConsumedBudget{ModelCalls: 3, VerificationCalls: 1}); err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = g.Revision
	e.IdempotencyKey = "patch-calls"
	revised, err := r.CommandPatchGoalBudget(ctx, e, model.BudgetPatch{Dimension: model.BudgetCalls, Count: 12}, "raise calls")
	if err != nil || revised.BudgetLimits.MaxHandoffs != 4 || revised.BudgetLimits.VerificationReserveCalls != 2 || *revised.BudgetLimits.MaxTotalTokens != 100 || revised.Confirmation != model.ConfirmationPending {
		t.Fatalf("patch %+v %v", revised, err)
	}
	used, err := r.store.GetBudgetTracker(ctx, e.SessionID, e.TargetID, revised.Revision)
	if err != nil || used.ModelCalls != 3 || used.VerificationCalls != 1 {
		t.Fatalf("consumption %+v %v", used, err)
	}
}

func TestBudgetEditConflictsWithCancellation(t *testing.T) {
	r, ctx, e, _ := goalCancelFixture(t, model.ConfirmationApproved)
	done := make(chan error, 2)
	go func() { _, err := r.CommandCancelGoal(ctx, e, "stop"); done <- err }()
	e2 := e
	e2.IdempotencyKey = "racing-edit"
	go func() {
		_, err := r.CommandPatchGoalBudget(ctx, e2, model.BudgetPatch{Dimension: model.BudgetCalls, Count: 4}, "edit")
		done <- err
	}()
	successes := 0
	for i := 0; i < 2; i++ {
		if err := <-done; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal(fmt.Sprintf("successful mutations=%d, expected one", successes))
	}
}
