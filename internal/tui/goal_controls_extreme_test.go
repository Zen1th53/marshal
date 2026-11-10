//go:build !governed

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
)

// unprovenGoalMenuAuthority cancels a pending goal without leaving durable
// proof, so the override's Prepare still refuses a confirmed goal. It satisfies
// pendingGoalControlAuthority so configureGoalControlBindings installs the
// shipped CTUI-0048 override.
type unprovenGoalMenuAuthority struct{ *fakeAuthority }

func (a *unprovenGoalMenuAuthority) CancelPendingGoal(ctx context.Context, e app.CommandEnvelope) (model.GoalContract, error) {
	result, err := a.CurrentGoal(ctx)
	if err != nil {
		return result, err
	}
	result.Revision = e.ExpectedVersion + 1
	result.Confirmation = model.ConfirmationCancelled
	return result, nil
}

func menuGoalFixture(t *testing.T, session string, confirmation model.ConfirmationState) (*Workspace, *app.Runtime, model.GoalContract) {
	t.Helper()
	ws, rt := realControlWorkspace(t, session)
	now := time.Now().UTC()
	goal := model.GoalContract{ID: "GOAL-menu-control", SessionID: session, ProjectID: rt.ProjectIdentity(), Revision: 1, OriginalRequest: "finish exact scoped work", DesiredOutcome: "finish exact scoped work", RequestDigest: "goal-menu-original", ConstitutionVersion: constitution.Current.String(), Confirmation: confirmation, Risk: model.R1, AuthoritySource: "owner", CreatedAt: now, UpdatedAt: now, BudgetLimits: &model.BudgetLimit{MaxModelCalls: 5, MaxDuration: time.Minute}}
	if err := rt.Store().SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	return ws, rt, goal
}

func TestExtremeMenuCancelPendingGoalIsDestructiveAndCanonical(t *testing.T) {
	ws, rt, goal := menuGoalFixture(t, "SESSION-menu-cancel-goal", model.ConfirmationPending)
	source := ws.controlSource()
	b := source.Bindings()["CTUI-0048"]
	if !b.Bound() || b.Safety != SafetyDestructive {
		t.Fatal("pending goal cancellation unavailable")
	}
	c := &Confirmation{}
	if err := c.Begin(t.Context(), b, ActionRequest{Action: b.Action, ProjectID: source.ProjectID, SessionID: source.SessionID}); err != nil {
		t.Fatal(err)
	}
	c.MoveSelection(1)
	if _, err := c.Submit(t.Context()); err == nil {
		t.Fatal("no destructive acknowledgement required")
	}
	c.Acknowledge()
	out, err := c.Submit(t.Context())
	if err != nil || !out.Succeeded() || !strings.Contains(out.Detail, "pending goal") {
		t.Fatal(out, err)
	}
	proof, err := rt.Store().GetActiveGoalContract(t.Context(), goal.SessionID)
	if err != nil || proof.Confirmation != model.ConfirmationCancelled || proof.Revision != 2 || proof.OriginalRequest != goal.OriginalRequest {
		t.Fatal(proof, err)
	}
	replay, err := b.Execute(t.Context(), c.Request())
	if err != nil || !replay.Succeeded() {
		t.Fatal(replay, err)
	}
}

func TestExtremeMenuCancelConfirmedGoalRefusesBeforeConfirmation(t *testing.T) {
	ws, rt, goal := menuGoalFixture(t, "SESSION-menu-cancel-confirmed", model.ConfirmationApproved)
	c := &Confirmation{}
	source := ws.controlSource()
	b := source.Bindings()["CTUI-0048"]
	if err := c.Begin(t.Context(), b, ActionRequest{Action: b.Action, ProjectID: source.ProjectID, SessionID: source.SessionID}); err == nil {
		t.Fatal("confirmed goal offered as pending cancellation")
	}
	proof, _ := rt.Store().GetActiveGoalContract(t.Context(), goal.SessionID)
	if proof.Revision != 1 || proof.Confirmation != model.ConfirmationApproved {
		t.Fatal("confirmed goal mutated")
	}
}

func TestExtremeMenuGoalControlsRefuseForeignAndRevokedAuthority(t *testing.T) {
	const id = ActionID("CTUI-0048")
	for _, scenario := range []string{"foreign-project", "foreign-session", "revoked"} {
		t.Run(string(id)+scenario, func(t *testing.T) {
			ws, rt, goal := menuGoalFixture(t, "SESSION-menu-goal-denied", model.ConfirmationPending)
			source := ws.controlSource()
			b := source.Bindings()[id]
			c := &Confirmation{}
			req := ActionRequest{Action: id, ProjectID: source.ProjectID, SessionID: source.SessionID}
			if err := c.Begin(t.Context(), b, req); err != nil {
				t.Fatal(err)
			}
			request := c.Request()
			switch scenario {
			case "foreign-project":
				request.ProjectID = "PROJECT-foreign"
			case "foreign-session":
				request.SessionID = "SESSION-foreign"
			case "revoked":
				grants, err := rt.Store().ListCapabilityGrants(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, grant := range grants {
					for _, action := range grant.Scope.Actions {
						if action == "goal.edit" {
							if err := rt.Store().RevokeCapabilityGrant(t.Context(), grant.ID, time.Now().UTC()); err != nil {
								t.Fatal(err)
							}
							found = true
						}
					}
				}
				if !found {
					t.Fatal("no goal edit grant")
				}
			}
			out, err := b.Execute(t.Context(), request)
			if err == nil && out.Succeeded() {
				t.Fatal("unauthorized goal cancellation succeeded", out)
			}
			proof, _ := rt.Store().GetActiveGoalContract(t.Context(), goal.SessionID)
			if proof.Revision != 1 || proof.Confirmation != model.ConfirmationPending || proof.BudgetLimits.MaxModelCalls != 5 {
				t.Fatal("denial mutated goal", proof)
			}
		})
	}
}

func TestExtremeMenuGoalMutationRequiresDurableProof(t *testing.T) {
	const id = ActionID("CTUI-0048")
	source, base := testControl(t)
	base.goal.Confirmation = model.ConfirmationPending
	source.Authority = &unprovenGoalMenuAuthority{base}
	b := source.Bindings()[id]
	c := &Confirmation{}
	if err := c.Begin(t.Context(), b, ActionRequest{Action: id, ProjectID: source.ProjectID, SessionID: source.SessionID}); err != nil {
		t.Fatal(err)
	}
	c.MoveSelection(1)
	c.Acknowledge()
	out, err := c.Submit(t.Context())
	if err != nil || out.Verdict != VerdictUnknown || out.Succeeded() {
		t.Fatal("unproven accepted mutation reported success", out, err)
	}
}
