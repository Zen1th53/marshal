package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

type policyTestAuthorizer func(context.Context, policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error)

func (f policyTestAuthorizer) AuthorizePolicyTestRun(ctx context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
	return f(ctx, req)
}

func a04Request(run policytest.TestRun) policytest.AuthorizationRequest {
	return policytest.AuthorizationRequest{
		SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1",
		RunID: run.ID, PolicyID: run.PolicyID, Binding: run.Binding, TestFileDigest: run.TestFileDigest,
		ExpectedState: policytest.StateLoaded, TargetState: policytest.StateValidated,
		Action: policytest.ActionTransition,
	}
}

func a04Allowed(req policytest.AuthorizationRequest) policytest.AuthorizationDecision {
	return policytest.AuthorizationDecision{
		Allowed: true, SubjectID: req.SubjectID, SessionID: req.SessionID, TaskID: req.TaskID, ChangeID: req.ChangeID,
		RunID: req.RunID, PolicyID: req.PolicyID, Binding: req.Binding, TestFileDigest: req.TestFileDigest,
		ExpectedState: req.ExpectedState, TargetState: req.TargetState, Action: req.Action,
	}
}

func TestPolicyTestRunTransitionRejectsMissingAuthority(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-auth-missing")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	_, err := st.TransitionPolicyTestRunStateAuthorized(ctx, a04Request(run), nil)
	if !errors.Is(err, policy.ErrAuthorizationUnavailable) {
		t.Fatalf("missing authorizer error = %v", err)
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil || got.State != policytest.StateLoaded {
		t.Fatalf("missing authority mutated run: state=%s err=%v", got.State, err)
	}
}

func TestPolicyTestRunTransitionAcceptsExactAuthority(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-auth-allow")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	req := a04Request(run)
	got, err := st.TransitionPolicyTestRunStateAuthorized(ctx, req, policyTestAuthorizer(func(_ context.Context, got policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(got), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != policytest.StateValidated || got.Binding != run.Binding || got.TestFileDigest != run.TestFileDigest {
		t.Fatalf("authorized transition result = %#v", got)
	}
}

func TestPolicyTestRunPutRejectsPrivilegedInitialState(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-auth-initial-state")
	run.State = policytest.StatePassed
	if err := st.PutPolicyTestRun(ctx, run); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("privileged initial state error = %v, want invalid", err)
	}
}

func TestPolicyTestRunTransitionRejectsDenialErrorsMalformedAndExpiredDecisions(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-auth-negative")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	req := a04Request(run)
	tests := map[string]func(policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error){
		"deny": func(policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
			return policytest.AuthorizationDecision{}, nil
		},
		"backend-secret": func(policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
			return policytest.AuthorizationDecision{}, errors.New("backend secret MARSHAL_TEST_SECRET_T49_A04_X1")
		},
		"wrong-run": func(r policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
			d := a04Allowed(r)
			d.RunID = "other-run"
			return d, nil
		},
		"expired": func(r policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
			d := a04Allowed(r)
			d.FreshUntil = time.Now().UTC().Add(-time.Second)
			return d, nil
		},
	}
	for name, makeDecision := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := st.TransitionPolicyTestRunStateAuthorized(ctx, req, policyTestAuthorizer(func(_ context.Context, r policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
				return makeDecision(r)
			}))
			if err == nil || name == "deny" && !errors.Is(err, policy.ErrAuthorizationDenied) || name == "backend-secret" && !errors.Is(err, policy.ErrAuthorizationUnavailable) || name != "deny" && name != "backend-secret" && !errors.Is(err, policy.ErrAuthorizationStale) {
				t.Fatalf("authorization error = %v", err)
			}
			if strings.Contains(err.Error(), "MARSHAL_TEST_SECRET_T49_A04_X1") {
				t.Fatal("authorizer error leaked secret marker")
			}
		})
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil || got.State != policytest.StateLoaded {
		t.Fatalf("negative authorization changed state=%s err=%v", got.State, err)
	}
}

func TestPolicyTestRunTransitionRejectsBindingAndEdgeReplay(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-auth-replay")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	base := a04Request(run)
	tests := map[string]func(*policytest.AuthorizationRequest, *policytest.AuthorizationDecision){
		"policy": func(r *policytest.AuthorizationRequest, d *policytest.AuthorizationDecision) {
			r.PolicyID = "other-policy"
			d.PolicyID = r.PolicyID
		},
		"digest": func(r *policytest.AuthorizationRequest, d *policytest.AuthorizationDecision) {
			r.Binding.Digest = policy.PolicyDigest("sha256:" + strings.Repeat("0", 64))
			d.Binding = r.Binding
		},
		"file-digest": func(r *policytest.AuthorizationRequest, d *policytest.AuthorizationDecision) {
			r.TestFileDigest = policy.PolicyDigest("sha256:" + strings.Repeat("1", 64))
			d.TestFileDigest = r.TestFileDigest
		},
		"edge": func(r *policytest.AuthorizationRequest, d *policytest.AuthorizationDecision) {
			r.TargetState = policytest.StateExecuted
			d.TargetState = r.TargetState
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			req := base
			decision := a04Allowed(req)
			mutate(&req, &decision)
			_, err := st.TransitionPolicyTestRunStateAuthorized(ctx, req, policyTestAuthorizer(func(context.Context, policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
				return decision, nil
			}))
			if err == nil {
				t.Fatal("replayed authorization unexpectedly succeeded")
			}
		})
	}
}

func TestPolicyTestRunTransitionRejectsStaleAuthorizedCAS(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-auth-stale")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	req := a04Request(run)
	_, err := st.TransitionPolicyTestRunStateAuthorized(ctx, req, policyTestAuthorizer(func(_ context.Context, r policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		if _, err := st.transitionPolicyTestRunState(ctx, r.RunID, r.ExpectedState, r.TargetState); err != nil {
			return policytest.AuthorizationDecision{}, err
		}
		return a04Allowed(r), nil
	}))
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale authorized CAS error = %v", err)
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil || got.State != policytest.StateValidated {
		t.Fatalf("stale authorized CAS state=%s err=%v", got.State, err)
	}
}

func TestPolicyTestRunTransitionCancellationDoesNotMutate(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-auth-cancel")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	req := a04Request(run)
	_, err := st.TransitionPolicyTestRunStateAuthorized(cancelled, req, policyTestAuthorizer(func(context.Context, policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	}))
	if err == nil {
		t.Fatal("cancelled authorized transition unexpectedly succeeded")
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil || got.State != policytest.StateLoaded {
		t.Fatalf("cancelled transition changed state=%s err=%v", got.State, err)
	}
}
