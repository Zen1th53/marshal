//go:build !governed

package tui

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

type goalBudgetControlAuthority interface {
	SetGoalBudget(context.Context, app.CommandEnvelope, model.BudgetLimit) (model.GoalContract, error)
}

type pendingGoalControlAuthority interface {
	CancelPendingGoal(context.Context, app.CommandEnvelope) (model.GoalContract, error)
}

func (a *runtimeControlAuthority) SetGoalBudget(ctx context.Context, envelope app.CommandEnvelope, limits model.BudgetLimit) (model.GoalContract, error) {
	if a.runtime == nil || a.localControl == nil {
		return model.GoalContract{}, errNoRuntime
	}
	return a.runtime.CommandSetGoalBudget(a.localControl.Context(ctx), envelope, limits)
}

func (a *runtimeControlAuthority) CancelPendingGoal(ctx context.Context, envelope app.CommandEnvelope) (model.GoalContract, error) {
	if a.runtime == nil || a.localControl == nil {
		return model.GoalContract{}, errNoRuntime
	}
	return a.runtime.CommandCancelPendingGoal(a.localControl.Context(ctx), envelope)
}

func configureGoalControlBindings(source *ControlSource, bindings map[ActionID]Binding) {
	if _, supported := source.Authority.(goalBudgetControlAuthority); supported {
		for _, id := range []ActionID{"CTUI-0079", "CTUI-0080"} {
			binding := bindings[id]
			label := "New limit (or clear): maximum duration (for example 30m; 0 or clear removes this limit)"
			if id == "CTUI-0080" {
				label = "New limit (or clear): maximum model calls (whole number; 0 or clear removes this limit)"
			}
			binding.Inputs = []InputSpec{{Key: "limit", Label: label, Required: true, Validate: func(value string) error { _, err := parseGoalBudgetInput(id, value); return err }}}
			// Prepare is only the pre-form check: with no goal the form must
			// not open to be refused after it was filled in, as the other
			// four budget limits already do. The target itself is read from
			// the typed limit by PrepareRequest.
			binding.Prepare = source.prepareGoal
			binding.PrepareRequest = func(ctx context.Context, req ActionRequest) (Target, error) {
				if _, err := parseGoalBudgetInput(id, req.Inputs["limit"]); err != nil {
					return Target{}, err
				}
				target, err := source.prepareGoal(ctx)
				if err == nil {
					target.Summary += "; revise budget to " + strings.TrimSpace(req.Inputs["limit"]) + "; fresh goal confirmation required"
				}
				return target, err
			}
			binding.Execute = func(ctx context.Context, req ActionRequest) (Outcome, error) {
				return source.executeGoalBudget(ctx, id, req)
			}
			bindings[id] = binding
		}
	}
	if _, supported := source.Authority.(pendingGoalControlAuthority); supported {
		binding := bindings["CTUI-0048"]
		binding.Gap = ""
		binding.Inputs = []InputSpec{{Key: "reason", Label: "Reason for cancellation"}}
		binding.Prepare = func(ctx context.Context) (Target, error) {
			target, err := source.prepareGoal(ctx)
			if err != nil {
				return Target{}, err
			}
			goal, err := source.Authority.CurrentGoal(ctx)
			if err != nil {
				return Target{}, err
			}
			if goal.ID != target.ID || goal.Revision != target.Revision {
				return Target{}, ErrStaleTarget
			}
			if goal.Confirmation != model.ConfirmationPending {
				return Target{}, fmt.Errorf("only pending goals can be cancelled here; use canonical run control for confirmed execution")
			}
			target.Summary += "; cancel pending intake only"
			return target, nil
		}
		binding.Execute = source.executeCancelPendingGoal
		bindings[binding.Action] = binding
	}
}

func parseGoalBudgetInput(id ActionID, raw string) (model.BudgetLimit, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return model.BudgetLimit{}, fmt.Errorf("a budget limit is required")
	}
	if strings.EqualFold(value, "clear") {
		value = "0"
	}
	switch id {
	case "CTUI-0079":
		d, err := time.ParseDuration(value)
		if value == "0" {
			d, err = 0, nil
		}
		if err != nil || d < 0 {
			return model.BudgetLimit{}, fmt.Errorf("duration must be non-negative and bounded (for example 30m or 0)")
		}
		return model.BudgetLimit{MaxDuration: d}, nil
	case "CTUI-0080":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return model.BudgetLimit{}, fmt.Errorf("model-call limit must be a non-negative whole number")
		}
		return model.BudgetLimit{MaxModelCalls: n}, nil
	}
	return model.BudgetLimit{}, ErrNoBinding
}

func (s *ControlSource) executeGoalBudget(ctx context.Context, id ActionID, req ActionRequest) (Outcome, error) {
	authority, ok := s.Authority.(goalBudgetControlAuthority)
	if !ok {
		return Outcome{}, ErrNoBinding
	}
	limits, err := parseGoalBudgetInput(id, req.Inputs["limit"])
	if err != nil {
		return Outcome{}, err
	}
	goal, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return Outcome{}, err
	}
	// The backend handles exact command replay, including a stale source revision.
	// Obtain the original revision there rather than copying a later revision's
	// other budget dimension into the replayed request.
	original, err := s.goalBudgetRevision(ctx, req.Target, goal)
	if err != nil {
		return refusal("the exact goal budget could not be read", err, req.Target, "internal/app/goal_editing.go")
	}
	if original.BudgetLimits != nil {
		parsed := limits
		limits = *original.BudgetLimits
		if id == "CTUI-0079" {
			limits.MaxDuration = parsed.MaxDuration
		} else {
			limits.MaxModelCalls = parsed.MaxModelCalls
		}
	}
	result, err := authority.SetGoalBudget(ctx, app.CommandEnvelope{ProjectID: req.ProjectID, SessionID: req.SessionID, TargetID: req.Target.ID, ExpectedVersion: req.Target.Revision, IdempotencyKey: req.IdempotencyKey}, limits)
	if err != nil {
		return refusal("the goal budget could not be revised", err, req.Target, "internal/app/goal_editing.go")
	}
	proof, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return unproven(req.Target, result.ID, err)
	}
	expected := &limits
	if limits.MaxModelCalls == 0 && limits.MaxDuration == 0 {
		expected = nil
	}
	if proof.ID != req.Target.ID || result.ID != req.Target.ID || proof.Revision != result.Revision || proof.Revision != req.Target.Revision+1 || proof.Confirmation != model.ConfirmationPending || !reflect.DeepEqual(proof.BudgetLimits, expected) {
		return Outcome{Verdict: VerdictUnknown, Detail: "goal budget returned without exact durable revision proof", Target: req.Target, Proof: Unknown("refresh the exact goal before assuming its budget changed", "internal/app/goal_editing.go")}, nil
	}
	detail := fmt.Sprintf("goal %s budget revised at revision %d; revision is PENDING; confirm and create a new plan; fresh confirmation and plan approval required; existing run budgets and consumption are unchanged", proof.ID, proof.Revision)
	return Outcome{Verdict: VerdictPass, Detail: detail, Target: Target{Kind: "goal", ID: proof.ID, Revision: proof.Revision, Digest: proof.RequestDigest}, Proof: Known(detail, "internal/app/goal_editing.go")}, nil
}

type goalRevisionReader interface {
	GoalRevision(context.Context, string, int64) (model.GoalContract, error)
}

func (s *ControlSource) goalBudgetRevision(ctx context.Context, target Target, current model.GoalContract) (model.GoalContract, error) {
	if target.Kind != "goal" || current.ID != target.ID {
		return model.GoalContract{}, ErrStaleTarget
	}
	if current.Revision == target.Revision {
		return current, nil
	}
	if reader, ok := s.Authority.(goalRevisionReader); ok {
		original, err := reader.GoalRevision(ctx, target.ID, target.Revision)
		if err == nil && (original.ID != target.ID || original.Revision != target.Revision || original.ProjectID != s.ProjectID || original.SessionID != s.SessionID) {
			return model.GoalContract{}, ErrStaleTarget
		}
		return original, err
	}
	return model.GoalContract{}, ErrStaleTarget
}

func (s *ControlSource) executeCancelPendingGoal(ctx context.Context, req ActionRequest) (Outcome, error) {
	authority, ok := s.Authority.(pendingGoalControlAuthority)
	if !ok {
		return Outcome{}, ErrNoBinding
	}
	if req.Target.Kind != "goal" {
		return Outcome{}, ErrStaleTarget
	}
	result, err := authority.CancelPendingGoal(ctx, app.CommandEnvelope{ProjectID: req.ProjectID, SessionID: req.SessionID, TargetID: req.Target.ID, ExpectedVersion: req.Target.Revision, IdempotencyKey: req.IdempotencyKey})
	if err != nil {
		return refusal("pending goal cancellation refused", err, req.Target, "internal/app/goal_cancel_extreme.go")
	}
	proof, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return unproven(req.Target, result.ID, err)
	}
	if result.ID != req.Target.ID || proof.ID != req.Target.ID || proof.Revision != result.Revision || proof.Revision != req.Target.Revision+1 || proof.Confirmation != model.ConfirmationCancelled {
		return Outcome{Verdict: VerdictUnknown, Detail: "pending goal cancellation lacks exact durable proof", Target: req.Target, Proof: Unknown("refresh the goal", "internal/app/goal_cancel_extreme.go")}, nil
	}
	detail := fmt.Sprintf("pending goal %s cancelled at revision %d", proof.ID, proof.Revision)
	return Outcome{Verdict: VerdictPass, Detail: detail, Target: Target{Kind: "goal", ID: proof.ID, Revision: proof.Revision, Digest: proof.RequestDigest}, Proof: Known(detail, "internal/app/goal_cancel_extreme.go")}, nil
}
