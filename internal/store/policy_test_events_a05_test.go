package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

func TestPolicyTestRunAuthorizedTransitionCommitsBoundEvent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-events-success")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	req := a04Request(run)
	if _, err := st.TransitionPolicyTestRunStateAuthorized(ctx, req, policyTestAuthorizer(func(_ context.Context, got policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(got), nil
	})); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found []map[string]any
	for _, event := range events {
		if event.Type == string(policytest.EventStarted) {
			found = append(found, event.Data)
		}
	}
	if len(found) != 1 {
		t.Fatalf("started events = %d, want 1", len(found))
	}
	data := found[0]
	for key, want := range map[string]string{
		"run_id": string(req.RunID), "policy_id": string(req.PolicyID), "policy_digest": string(req.Binding.Digest),
		"test_file_digest": string(req.TestFileDigest), "previous_state": string(req.ExpectedState),
		"target_state": string(req.TargetState), "action": string(req.Action), "subject_id": req.SubjectID,
		"session_id": req.SessionID, "task_id": req.TaskID, "change_id": req.ChangeID,
		"result": "allowed", "reason_code": "POLICY_AUTHORIZATION_ALLOWED",
	} {
		if got := data[key]; got != want {
			t.Fatalf("event %s = %#v, want %q", key, got, want)
		}
	}
	if got := data["policy_version"]; got != float64(req.Binding.Version) {
		t.Fatalf("policy_version = %#v", got)
	}
	if got := data["generation"]; got != float64(req.Binding.Generation) {
		t.Fatalf("generation = %#v", got)
	}
}

func TestPolicyTestRunEventFailureRollsBackMutation(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-events-rollback")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "DROP TABLE audit_events"); err != nil {
		t.Fatal(err)
	}
	req := a04Request(run)
	if _, err := st.TransitionPolicyTestRunStateAuthorized(ctx, req, policyTestAuthorizer(func(_ context.Context, got policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(got), nil
	})); err == nil {
		t.Fatal("transition succeeded without audit_events")
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil || got.State != policytest.StateLoaded {
		t.Fatalf("rollback state=%s err=%v", got.State, err)
	}
}

func TestPolicyTestRunDeniedTransitionCreatesNoSuccessEvent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-events-denied")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	req := a04Request(run)
	_, err := st.TransitionPolicyTestRunStateAuthorized(ctx, req, policyTestAuthorizer(func(context.Context, policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return policytest.AuthorizationDecision{}, nil
	}))
	if !errors.Is(err, policy.ErrAuthorizationDenied) {
		t.Fatalf("deny error=%v", err)
	}
	events, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.HasPrefix(event.Type, "policytest.") {
			t.Fatalf("denial created T49 event: %#v", event)
		}
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil || got.State != policytest.StateLoaded {
		t.Fatalf("denied state=%s err=%v", got.State, err)
	}
}

func TestPolicyTestRunEventRetryReopensWithOneSemanticSuccess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "policy-test-events.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-events-retry")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	req := a04Request(run)
	authorizer := policyTestAuthorizer(func(_ context.Context, got policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(got), nil
	})
	if _, err := st.TransitionPolicyTestRunStateAuthorized(ctx, req, authorizer); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionPolicyTestRunStateAuthorized(ctx, req, authorizer); err == nil {
		t.Fatal("same-state retry unexpectedly succeeded")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == string(policytest.EventStarted) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("started events after retry/reopen=%d", count)
	}
}

func TestPolicyTestRunEvidenceCannotAuthorizeNextTransition(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-events-authority")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	first := a04Request(run)
	if _, err := st.TransitionPolicyTestRunStateAuthorized(ctx, first, policyTestAuthorizer(func(_ context.Context, got policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(got), nil
	})); err != nil {
		t.Fatal(err)
	}
	next := first
	next.ExpectedState, next.TargetState = policytest.StateValidated, policytest.StateExecuted
	if _, err := st.TransitionPolicyTestRunStateAuthorized(ctx, next, nil); !errors.Is(err, policy.ErrAuthorizationUnavailable) {
		t.Fatalf("historical evidence supplied authority: %v", err)
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil || got.State != policytest.StateValidated {
		t.Fatalf("evidence-as-authority state=%s err=%v", got.State, err)
	}
}

func TestPolicyTestRunEventAuthorizerSecretNeverPersists(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T49_A05_BACKEND_7f2a"
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-events-secret")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	_, err := st.TransitionPolicyTestRunStateAuthorized(ctx, a04Request(run), policyTestAuthorizer(func(context.Context, policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return policytest.AuthorizationDecision{}, errors.New(marker)
	}))
	if !errors.Is(err, policy.ErrAuthorizationUnavailable) || strings.Contains(err.Error(), marker) {
		t.Fatalf("secret-bearing authorizer error=%v", err)
	}
	var raw string
	if err := st.db.QueryRowContext(ctx, "SELECT COALESCE(group_concat(data_json), '') FROM audit_events").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, marker) {
		t.Fatal("secret marker persisted in audit events")
	}
}

func TestPolicyTestRunMultiStoreLoserHasNoSuccessEvent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "policy-test-events-multistore.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-events-multistore")
	if err := first.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	req := a04Request(run)
	authorizer := policyTestAuthorizer(func(_ context.Context, got policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(got), nil
	})
	if _, err := first.TransitionPolicyTestRunStateAuthorized(ctx, req, authorizer); err != nil {
		t.Fatal(err)
	}
	if _, err := second.TransitionPolicyTestRunStateAuthorized(ctx, req, authorizer); err == nil {
		t.Fatal("stale second store unexpectedly succeeded")
	}
	events, err := first.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == string(policytest.EventStarted) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("success events after multi-store race=%d", count)
	}
}

func TestPolicyTestRunTerminalEventUsesLifecycleOnly(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-events-terminal")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	transition := func(req policytest.AuthorizationRequest) {
		t.Helper()
		if _, err := st.TransitionPolicyTestRunStateAuthorized(ctx, req, policyTestAuthorizer(func(_ context.Context, got policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
			return a04Allowed(got), nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	req := a04Request(run)
	transition(req)
	req.ExpectedState, req.TargetState = policytest.StateValidated, policytest.StateExecuted
	transition(req)
	req.ExpectedState, req.TargetState = policytest.StateExecuted, policytest.StatePassed
	transition(req)
	events, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finished := 0
	started := 0
	for _, event := range events {
		if event.Type == string(policytest.EventStarted) {
			started++
		}
		if event.Type == string(policytest.EventFinished) {
			finished++
			if event.Data["result"] != "passed" {
				t.Fatalf("terminal event result=%#v", event.Data["result"])
			}
		}
	}
	if finished != 1 {
		t.Fatalf("finished events=%d", finished)
	}
	if started != 2 {
		t.Fatalf("started events=%d", started)
	}
	if _, err := st.GetPolicyTestRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
}
