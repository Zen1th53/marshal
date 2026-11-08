package tui

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

type goalBudgetAuthority interface {
	PatchGoalBudget(context.Context, app.CommandEnvelope, model.BudgetPatch, string) (model.GoalContract, error)
	GoalRevision(context.Context, string, int64) (model.GoalContract, error)
}

func (a *runtimeControlAuthority) PatchGoalBudget(ctx context.Context, e app.CommandEnvelope, p model.BudgetPatch, reason string) (model.GoalContract, error) {
	if a == nil || a.runtime == nil {
		return model.GoalContract{}, errNoRuntime
	}
	if a.localControlErr != nil {
		return model.GoalContract{}, a.localControlErr
	}
	if a.localControl == nil {
		return model.GoalContract{}, authz.ErrDenied
	}
	return a.runtime.CommandPatchGoalBudget(a.localControl.Context(ctx), e, p, reason)
}

func budgetActionBindings(s *ControlSource) map[ActionID]Binding {
	bindings := map[ActionID]Binding{}
	for _, spec := range []struct {
		id        ActionID
		title     string
		dimension model.BudgetDimension
	}{
		{"CTUI-0077", "Token limit", model.BudgetTokens}, {"CTUI-0078", "Cost limit", model.BudgetCost}, {"CTUI-0079", "Time limit", model.BudgetDuration}, {"CTUI-0080", "Model-call limit", model.BudgetCalls}, {"CTUI-0081", "Handoff limit", model.BudgetHandoffs}, {"CTUI-0082", "Verification reserve (model calls)", model.BudgetReserve},
	} {
		spec := spec
		binding := Binding{Action: spec.id, Title: spec.title, Safety: SafetyGoverned, Prepare: s.prepareGoal, Inputs: []InputSpec{
			{Key: "value", Label: "New limit (or clear)", Required: true, Validate: func(v string) error { _, err := parseBudgetPatch(spec.dimension, v); return err }},
			{Key: "reason", Label: "Reason for new pending goal revision", Required: true},
		}}
		authority, ok := s.Authority.(goalBudgetAuthority)
		if !ok {
			binding.Requires = "goal budget authority is unavailable"
		}
		binding.Execute = func(ctx context.Context, req ActionRequest) (Outcome, error) {
			if !ok {
				return refusal("goal budget authority is unavailable", model.ErrUnavailable, req.Target, "internal/app/goal_budget.go")
			}
			if err := validateGoalActionTarget(ctx, s, req); err != nil {
				return refusal("goal target changed", err, req.Target, "internal/store/goal.go")
			}
			patch, err := parseBudgetPatch(spec.dimension, req.Inputs["value"])
			if err != nil {
				return refusal("invalid budget limit", err, req.Target, "internal/model/budget_patch.go")
			}
			reason := strings.TrimSpace(req.Inputs["reason"])
			if reason == "" {
				return refusal("revision reason is required", model.ErrInvalid, req.Target, "internal/app/goal_budget.go")
			}
			envelope := app.CommandEnvelope{ProjectID: req.ProjectID, SessionID: req.SessionID, TargetID: req.Target.ID, ExpectedVersion: req.Target.Revision, IdempotencyKey: req.IdempotencyKey}
			revised, err := authority.PatchGoalBudget(ctx, envelope, patch, reason)
			if err != nil {
				return refusal("budget revision refused", err, req.Target, "internal/app/goal_budget.go")
			}
			proof, err := authority.GoalRevision(ctx, revised.ID, revised.Revision)
			if err != nil {
				return unproven(req.Target, revised.ID, err)
			}
			if proof.ID != req.Target.ID || proof.ProjectID != req.ProjectID || proof.SessionID != req.SessionID || proof.Revision != req.Target.Revision+1 || proof.Confirmation != model.ConfirmationPending {
				return unproven(req.Target, revised.ID, fmt.Errorf("new pending revision not proven"))
			}
			// Verify the exact requested limit against the prior immutable revision.
			previous, err := authority.GoalRevision(ctx, req.Target.ID, req.Target.Revision)
			if err != nil {
				return unproven(req.Target, revised.ID, err)
			}
			expected, err := patch.Apply(previous.BudgetLimits)
			if err != nil {
				return unproven(req.Target, revised.ID, err)
			}
			if !sameGoalLimits(expected, proof.BudgetLimits) {
				return unproven(req.Target, revised.ID, fmt.Errorf("durable limits differ from reviewed patch"))
			}
			return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("goal %s revision %d is PENDING; confirm and create a new plan before dispatch", proof.ID, proof.Revision), Target: Target{Kind: "goal", ID: proof.ID, Revision: proof.Revision, Digest: proof.RequestDigest}, Proof: Known("PENDING budget revision", "internal/store/goal.go")}, nil
		}
		bindings[spec.id] = binding
	}
	return bindings
}

func parseBudgetPatch(d model.BudgetDimension, input string) (model.BudgetPatch, error) {
	p := model.BudgetPatch{Dimension: d}
	value := strings.TrimSpace(input)
	if value == "clear" {
		p.Clear = true
		return p, nil
	}
	var err error
	switch d {
	case model.BudgetTokens:
		p.Tokens, err = strconv.ParseInt(value, 10, 64)
		if err == nil && p.Tokens < 0 {
			err = model.ErrInvalid
		}
	case model.BudgetCost:
		p.CostUSD, err = strconv.ParseFloat(value, 64)
		if err == nil && (p.CostUSD < 0 || math.IsNaN(p.CostUSD) || math.IsInf(p.CostUSD, 0)) {
			err = model.ErrInvalid
		}
	case model.BudgetDuration:
		p.Duration, err = time.ParseDuration(value)
		if err == nil && p.Duration <= 0 {
			err = model.ErrInvalid
		}
	default:
		p.Count, err = strconv.Atoi(value)
		if err == nil && p.Count <= 0 {
			err = model.ErrInvalid
		}
	}
	if err != nil {
		return p, fmt.Errorf("invalid %s limit: %w", d, err)
	}
	return p, nil
}
