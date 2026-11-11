//go:build !governed

package tui

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

type pendingGoalControlAuthority interface {
	CancelPendingGoal(context.Context, app.CommandEnvelope) (model.GoalContract, error)
}

func (a *runtimeControlAuthority) CancelPendingGoal(ctx context.Context, envelope app.CommandEnvelope) (model.GoalContract, error) {
	if a.runtime == nil || a.localControl == nil {
		return model.GoalContract{}, errNoRuntime
	}
	return a.runtime.CommandCancelPendingGoal(a.localControl.Context(ctx), envelope)
}

func configureGoalControlBindings(source *ControlSource, bindings map[ActionID]Binding) {
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
