package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

type goalCancellationAuthority interface {
	CancelGoal(context.Context, app.CommandEnvelope, string) (app.GoalCancellation, error)
	GoalRevision(context.Context, string, int64) (model.GoalContract, error)
}

func (a *runtimeControlAuthority) CancelGoal(ctx context.Context, e app.CommandEnvelope, reason string) (app.GoalCancellation, error) {
	if a == nil || a.runtime == nil {
		return app.GoalCancellation{}, errNoRuntime
	}
	if a.localControlErr != nil {
		return app.GoalCancellation{}, a.localControlErr
	}
	if a.localControl == nil {
		return app.GoalCancellation{}, authz.ErrDenied
	}
	return a.runtime.CommandCancelGoal(a.localControl.Context(ctx), e, reason)
}

// goalCancelBinding builds the one goal action the composer runs through a
// binding: CTUI-0048, goal cancellation. The shipped build rewrites its
// Prepare/Execute to the pending-goal-only variant in goal_controls_extreme.go.
func goalCancelBinding(s *ControlSource) Binding {
	a, ok := s.Authority.(goalCancellationAuthority)
	b := Binding{Action: "CTUI-0048", Title: "Cancel goal", Safety: SafetyDestructive, Prepare: s.prepareGoal, Inputs: []InputSpec{{Key: "reason", Label: "Reason for cancellation", Required: true}}}
	if !ok {
		b.Requires = "goal cancellation authority is unavailable"
	}
	b.Execute = func(ctx context.Context, req ActionRequest) (Outcome, error) {
		if !ok {
			return refusal("goal cancellation authority is unavailable", model.ErrUnavailable, req.Target, "internal/app/goal_cancel.go")
		}
		if err := validateGoalActionTarget(ctx, s, req); err != nil {
			return refusal("goal target changed", err, req.Target, "internal/store/goal.go")
		}
		reason := strings.TrimSpace(req.Inputs["reason"])
		if reason == "" {
			return refusal("cancellation reason is required", model.ErrInvalid, req.Target, "internal/app/goal_cancel.go")
		}
		result, err := a.CancelGoal(ctx, app.CommandEnvelope{ProjectID: req.ProjectID, SessionID: req.SessionID, TargetID: req.Target.ID, ExpectedVersion: req.Target.Revision, IdempotencyKey: req.IdempotencyKey}, reason)
		if err != nil {
			return refusal("goal cancellation refused", err, req.Target, "internal/app/goal_cancel.go")
		}
		g, err := a.GoalRevision(ctx, result.Goal.ID, result.Goal.Revision)
		if err != nil {
			return unproven(req.Target, result.Goal.ID, err)
		}
		term, err := s.Authority.GoalTermination(ctx, g.ID, g.Revision)
		if err != nil {
			return unproven(req.Target, g.ID, err)
		}
		if g.ID != req.Target.ID || g.ProjectID != req.ProjectID || g.SessionID != req.SessionID || g.Revision != req.Target.Revision+1 || g.Confirmation != model.ConfirmationCancelled || term.GoalID != g.ID || term.SessionID != g.SessionID || term.GoalRevision != g.Revision || term.State != model.StateCancelled || term.ReasonCode != model.ReasonUserCancelled {
			return unproven(req.Target, g.ID, fmt.Errorf("atomic cancellation not proven"))
		}
		detail := fmt.Sprintf("goal %s cancelled at revision %d", g.ID, g.Revision)
		if result.Settling {
			detail += "; active work is settling"
		}
		if result.StopError != "" {
			detail += "; execution stop requires reconciliation"
		}
		return Outcome{Verdict: VerdictPass, Detail: detail, Target: Target{Kind: "goal", ID: g.ID, Revision: g.Revision, Digest: g.RequestDigest}, Proof: Known("CANCELLED / USER_CANCELLED", "internal/store/goal_cancel.go")}, nil
	}
	return b
}

func validateGoalActionTarget(ctx context.Context, s *ControlSource, req ActionRequest) error {
	if err := s.available(); err != nil {
		return err
	}
	if req.Target.Kind != "goal" || req.ProjectID != s.ProjectID || req.SessionID != s.SessionID || req.Target.ID == "" || req.Target.Revision < 1 {
		return model.ErrInvalid
	}
	g, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return err
	}
	if g.ProjectID != req.ProjectID || g.SessionID != req.SessionID || g.ID != req.Target.ID || g.Revision != req.Target.Revision || g.RequestDigest != req.Target.Digest || g.Confirmation == model.ConfirmationCancelled {
		return model.ErrGoalConflict
	}
	return nil
}
