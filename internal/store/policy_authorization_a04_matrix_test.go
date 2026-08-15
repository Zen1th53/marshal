package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

type policyMutationAuthorizer func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error)

func (f policyMutationAuthorizer) AuthorizePolicyMutation(ctx context.Context, request policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
	return f(ctx, request)
}

func authorizedRequest(record PolicyRecord) policy.PolicyMutationRequest {
	return policy.PolicyMutationRequest{
		SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1",
		PolicyID: record.Policy.ID, PolicyVersion: record.Policy.Version,
		ExpectedState: policy.StateLoaded, TargetState: policy.StateValidated,
		Binding: record.Binding, Action: policy.ActionPolicyValidate,
	}
}

func allowedDecision(request policy.PolicyMutationRequest) policy.PolicyMutationDecision {
	return policy.PolicyMutationDecision{
		Allowed: true, SubjectID: request.SubjectID, SessionID: request.SessionID,
		TaskID: request.TaskID, ChangeID: request.ChangeID, PolicyID: request.PolicyID,
		PolicyVersion: request.PolicyVersion, ExpectedState: request.ExpectedState,
		TargetState: request.TargetState, Binding: request.Binding, Action: request.Action,
	}
}

func TestAuthorizedPolicyTransitionRequiresExactBoundDecision(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "auth-allow")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	request := authorizedRequest(record)
	got, err := st.TransitionPolicyStateAuthorized(ctx, request, policyMutationAuthorizer(func(_ context.Context, got policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
		return allowedDecision(got), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != policy.StateValidated || got.Binding != record.Binding {
		t.Fatalf("authorized result = %#v", got)
	}
}

func TestAuthorizedPolicyTransitionDeniesAndContainsAuthorizerErrors(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "auth-deny")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	request := authorizedRequest(record)
	for name, authorizer := range map[string]policy.ManagementAuthorizer{
		"deny": policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
			return policy.PolicyMutationDecision{}, nil
		}),
		"backend-secret": policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
			return policy.PolicyMutationDecision{}, errors.New("backend secret MARSHAL_TEST_SECRET_T48_A04_X9")
		}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := st.TransitionPolicyStateAuthorized(ctx, request, authorizer)
			if err == nil || !errors.Is(err, policy.ErrAuthorizationDenied) && !errors.Is(err, policy.ErrAuthorizationUnavailable) {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "MARSHAL_TEST_SECRET_T48_A04_X9") {
				t.Fatal("secret marker leaked in public error")
			}
		})
	}
	loaded, err := st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != policy.StateLoaded {
		t.Fatalf("denied mutation changed state to %q", loaded.State)
	}
}

func TestAuthorizedPolicyTransitionRejectsReplayAndExpiry(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "auth-replay")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	request := authorizedRequest(record)
	tests := map[string]func(policy.PolicyMutationRequest) policy.PolicyMutationDecision{
		"wrong-policy": func(r policy.PolicyMutationRequest) policy.PolicyMutationDecision {
			d := allowedDecision(r)
			d.PolicyID = "other-policy"
			return d
		},
		"wrong-edge": func(r policy.PolicyMutationRequest) policy.PolicyMutationDecision {
			d := allowedDecision(r)
			d.TargetState = policy.StateActive
			return d
		},
		"expired": func(r policy.PolicyMutationRequest) policy.PolicyMutationDecision {
			d := allowedDecision(r)
			d.ExpiresAt = time.Now().Add(-time.Second)
			return d
		},
	}
	for name, makeDecision := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := st.TransitionPolicyStateAuthorized(ctx, request, policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
				return makeDecision(request), nil
			}))
			if !errors.Is(err, policy.ErrAuthorizationStale) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestAuthorizedPolicyTransitionRejectsStaleBindingAfterCanonicalChange(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "auth-stale")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	request := authorizedRequest(record)
	if _, err := st.transitionPolicyState(ctx, record.Policy.ID, record.Policy.Version, policy.StateLoaded, policy.StateValidated, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.transitionPolicyState(ctx, record.Policy.ID, record.Policy.Version, policy.StateValidated, policy.StateActive, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.transitionPolicyState(ctx, record.Policy.ID, record.Policy.Version, policy.StateActive, policy.StateSuperseded, nil); err != nil {
		t.Fatal(err)
	}
	_, err := st.TransitionPolicyStateAuthorized(ctx, request, policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
		return allowedDecision(request), nil
	}))
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale transition error = %v", err)
	}
	loaded, err := st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != policy.StateSuperseded {
		t.Fatalf("stale authorization changed state to %q", loaded.State)
	}
}
