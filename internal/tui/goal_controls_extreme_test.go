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

func TestExtremeMenuBudgetCreatesPendingRevisionAndPreservesOtherLimit(t *testing.T) {
	for _, step := range []struct {
		id       ActionID
		value    string
		calls    int
		duration time.Duration
	}{{"CTUI-0079", "2m", 5, 2 * time.Minute}, {"CTUI-0080", "8", 8, time.Minute}, {"CTUI-0079", "0", 5, 0}} {
		t.Run(string(step.id)+step.value, func(t *testing.T) {
			ws, rt, goal := menuGoalFixture(t, "SESSION-menu-budget", model.ConfirmationApproved)
			source := ws.controlSource()
			binding := source.Bindings()[step.id]
			if !binding.Bound() || len(binding.Inputs) != 1 {
				t.Fatalf("no typed budget binding: %+v", binding)
			}
			confirmation := &Confirmation{}
			req := ActionRequest{Action: step.id, ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"limit": step.value}}
			if err := confirmation.Begin(t.Context(), binding, req); err != nil {
				t.Fatal(err)
			}
			before, _ := rt.Store().GetActiveGoalContract(t.Context(), goal.SessionID)
			if before.Revision != 1 {
				t.Fatal("preparation revised goal")
			}
			confirmation.MoveSelection(1)
			out, err := confirmation.Submit(t.Context())
			if err != nil || !out.Succeeded() {
				t.Fatal(out, err)
			}
			proof, err := rt.Store().GetActiveGoalContract(t.Context(), goal.SessionID)
			if err != nil || proof.Revision != 2 || proof.Confirmation != model.ConfirmationPending || proof.BudgetLimits == nil || proof.BudgetLimits.MaxModelCalls != step.calls || proof.BudgetLimits.MaxDuration != step.duration || proof.OriginalRequest != goal.OriginalRequest {
				t.Fatal(proof, err)
			}
			replay, err := binding.Execute(t.Context(), confirmation.Request())
			if err != nil || !replay.Succeeded() {
				t.Fatal("replay", replay, err)
			}
			after, _ := rt.Store().GetActiveGoalContract(t.Context(), goal.SessionID)
			if after.Revision != 2 {
				t.Fatal("duplicate revision")
			}
			reopened, err := app.Open(t.Context(), rt.ProjectRoot())
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			persisted, err := reopened.Store().GetActiveGoalContract(t.Context(), goal.SessionID)
			if err != nil || persisted.Revision != 2 || *persisted.BudgetLimits != *proof.BudgetLimits {
				t.Fatal("not durable", persisted, err)
			}
		})
	}
}

func TestExtremeMenuBudgetRejectsInvalidAndStaleIntent(t *testing.T) {
	ws, rt, goal := menuGoalFixture(t, "SESSION-menu-budget-invalid", model.ConfirmationPending)
	source := ws.controlSource()
	for _, step := range []struct {
		id    ActionID
		value string
	}{{"CTUI-0079", "-1s"}, {"CTUI-0079", "9223372036854775807h"}, {"CTUI-0080", "-1"}, {"CTUI-0080", "1.5"}, {"CTUI-0080", ""}} {
		b := source.Bindings()[step.id]
		c := &Confirmation{}
		if err := c.Begin(t.Context(), b, ActionRequest{Action: step.id, ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"limit": step.value}}); err == nil {
			t.Fatal("invalid intent entered confirmation", step)
		}
	}
	b := source.Bindings()["CTUI-0080"]
	c := &Confirmation{}
	if err := c.Begin(t.Context(), b, ActionRequest{Action: b.Action, ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"limit": "9"}}); err != nil {
		t.Fatal(err)
	}
	goal.Revision = 2
	goal.BudgetLimits = &model.BudgetLimit{MaxModelCalls: 3}
	if err := rt.Store().SaveGoalContract(t.Context(), goal, 1); err != nil {
		t.Fatal(err)
	}
	c.MoveSelection(1)
	out, _ := c.Submit(t.Context())
	if out.Succeeded() {
		t.Fatal("stale confirmation succeeded")
	}
	proof, _ := rt.Store().GetActiveGoalContract(t.Context(), goal.SessionID)
	if proof.Revision != 2 || proof.BudgetLimits.MaxModelCalls != 3 {
		t.Fatal("overwrote newer goal")
	}
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
	for _, id := range []ActionID{"CTUI-0048", "CTUI-0079", "CTUI-0080"} {
		for _, scenario := range []string{"foreign-project", "foreign-session", "revoked"} {
			t.Run(string(id)+scenario, func(t *testing.T) {
				ws, rt, goal := menuGoalFixture(t, "SESSION-menu-goal-denied", model.ConfirmationPending)
				source := ws.controlSource()
				b := source.Bindings()[id]
				c := &Confirmation{}
				req := ActionRequest{Action: id, ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"limit": "0"}}
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
					t.Fatal("unauthorized menu goal control succeeded", out)
				}
				proof, _ := rt.Store().GetActiveGoalContract(t.Context(), goal.SessionID)
				if proof.Revision != 1 || proof.Confirmation != model.ConfirmationPending || proof.BudgetLimits.MaxModelCalls != 5 {
					t.Fatal("denial mutated goal", proof)
				}
			})
		}
	}
}

type unprovenGoalMenuAuthority struct{ *fakeAuthority }

func (a *unprovenGoalMenuAuthority) SetGoalBudget(ctx context.Context, e app.CommandEnvelope, limits model.BudgetLimit) (model.GoalContract, error) {
	result, err := a.CurrentGoal(ctx)
	if err != nil {
		return result, err
	}
	result.Revision = e.ExpectedVersion + 1
	result.Confirmation = model.ConfirmationPending
	result.BudgetLimits = &limits
	return result, nil
}
func (a *unprovenGoalMenuAuthority) CancelPendingGoal(ctx context.Context, e app.CommandEnvelope) (model.GoalContract, error) {
	result, err := a.CurrentGoal(ctx)
	if err != nil {
		return result, err
	}
	result.Revision = e.ExpectedVersion + 1
	result.Confirmation = model.ConfirmationCancelled
	return result, nil
}
func TestExtremeMenuGoalMutationRequiresDurableProof(t *testing.T) {
	for _, id := range []ActionID{"CTUI-0079", "CTUI-0080", "CTUI-0048"} {
		t.Run(string(id), func(t *testing.T) {
			source, base := testControl(t)
			base.goal.Confirmation = model.ConfirmationPending
			source.Authority = &unprovenGoalMenuAuthority{base}
			b := source.Bindings()[id]
			c := &Confirmation{}
			if err := c.Begin(t.Context(), b, ActionRequest{Action: id, ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"limit": "0"}}); err != nil {
				t.Fatal(err)
			}
			c.MoveSelection(1)
			c.Acknowledge()
			out, err := c.Submit(t.Context())
			if err != nil || out.Verdict != VerdictUnknown || out.Succeeded() {
				t.Fatal("unproven accepted mutation reported success", out, err)
			}
		})
	}
}

// With no goal, the time and model-call limits refuse before their form opens,
// as the other four budget limits do (e2e 2026-11-06: they opened a form that
// could only be refused after it was filled in).
func TestExtremeMenuBudgetWithNoGoalRefusesBeforeTheForm(t *testing.T) {
	ws, _ := realControlWorkspace(t, "SESSION-menu-budget-nogoal")
	source := ws.controlSource()
	for _, id := range []ActionID{"CTUI-0077", "CTUI-0079", "CTUI-0080", "CTUI-0081"} {
		b := source.Bindings()[id]
		if b.Prepare == nil {
			t.Fatalf("%s has no pre-form check", id)
		}
		if _, err := b.Prepare(t.Context()); err == nil {
			t.Fatalf("%s: pre-form check passed with no goal", id)
		}
	}
}
