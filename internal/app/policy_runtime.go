package app

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

// RuntimePolicyConfig identifies the single canonical policy snapshot that
// may govern protected runtime work. Selection never comes from a request.
type RuntimePolicyConfig struct {
	PolicyID      policy.PolicyID
	PolicyVersion policy.PolicyVersion
}

// executeWithPolicy evaluates and validates a decision before invoking the
// side effect. Requirements are not treated as an implicit allow.
func executeWithPolicy(ctx context.Context, evaluator *policy.Evaluator, current policy.PolicyBinding, request policy.EvaluationRequest, operation func() error) error {
	if evaluator == nil || ctx.Err() != nil {
		return fmt.Errorf("%w: policy evaluator unavailable", model.ErrPolicyDenied)
	}
	decision, err := evaluator.Evaluate(ctx, request)
	if err != nil {
		return fmt.Errorf("%w: policy evaluation failed", model.ErrPolicyDenied)
	}
	decision.Binding.Generation = current.Generation
	if err := decision.Validate(); err != nil || !decision.Binding.FreshAgainst(current) {
		return fmt.Errorf("%w: invalid or stale policy decision", model.ErrPolicyDenied)
	}
	if !decision.Allowed || decision.Effect != policy.EffectAllow || len(decision.Requirements) != 0 {
		return fmt.Errorf("%w: policy denied operation", model.ErrPolicyDenied)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: policy context cancelled", model.ErrPolicyDenied)
	}
	return operation()
}

func (r *Runtime) authorizeRuntime(ctx context.Context, subject, task, provider string, action policy.Action, resource policy.Resource) error {
	if !r.policyConfigured {
		return nil
	}
	record, err := r.store.GetPolicy(ctx, r.runtimePolicy.PolicyID, r.runtimePolicy.PolicyVersion)
	if err != nil || record.State != policy.StateActive {
		return fmt.Errorf("%w: active runtime policy unavailable", model.ErrPolicyDenied)
	}
	evaluator, err := policy.NewEvaluator(record.Policy)
	if err != nil {
		return fmt.Errorf("%w: active runtime policy invalid", model.ErrPolicyDenied)
	}
	return executeWithPolicy(ctx, evaluator, record.Binding, policy.EvaluationRequest{
		SubjectID: subject, TaskID: task, Action: action, Resource: resource, Provider: provider,
	}, func() error { return nil })
}
