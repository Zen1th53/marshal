package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

func policyEventsOfType(t *testing.T, st *Store, typ policy.PolicyEventType) []model.Event {
	t.Helper()
	events, err := st.ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var matched []model.Event
	for _, event := range events {
		if event.Type == string(typ) {
			matched = append(matched, event)
		}
	}
	return matched
}

func TestAuthorizedPolicyTransitionCommitsBoundEventAtomically(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "events-success")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	request := authorizedRequest(record)
	if _, err := st.TransitionPolicyStateAuthorized(ctx, request, policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
		return allowedDecision(request), nil
	})); err != nil {
		t.Fatal(err)
	}
	events := policyEventsOfType(t, st, policy.EventPolicyDecisionAllowed)
	if len(events) != 1 {
		t.Fatalf("allowed events = %d, want 1", len(events))
	}
	data := events[0].Data
	for key, want := range map[string]string{
		"policy_id": string(record.Policy.ID), "policy_digest": string(record.Binding.Digest),
		"previous_state": string(policy.StateLoaded), "target_state": string(policy.StateValidated),
		"action": string(policy.ActionPolicyValidate), "subject_id": request.SubjectID,
		"session_id": request.SessionID, "task_id": request.TaskID, "change_id": request.ChangeID,
		"result": "allowed", "reason_code": string(policy.CodeAuthorizationAllowed),
	} {
		if got := data[key]; got != want {
			t.Fatalf("event %s = %#v, want %q", key, got, want)
		}
	}
	if got := data["policy_version"]; got != float64(record.Policy.Version) {
		t.Fatalf("policy_version = %#v", got)
	}
	if got := data["generation"]; got != float64(record.Binding.Generation) {
		t.Fatalf("generation = %#v", got)
	}
}

func TestPolicyEventFailureRollsBackPolicyMutation(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "events-rollback")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "DROP TABLE audit_events"); err != nil {
		t.Fatal(err)
	}
	request := authorizedRequest(record)
	if _, err := st.TransitionPolicyStateAuthorized(ctx, request, policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
		return allowedDecision(request), nil
	})); err == nil {
		t.Fatal("transition succeeded without event table")
	}
	loaded, err := st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != policy.StateLoaded {
		t.Fatalf("state after event failure = %q", loaded.State)
	}
}

func TestDeniedPolicyTransitionEmitsNoSuccessAndNoMutation(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "events-denied")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	request := authorizedRequest(record)
	_, err := st.TransitionPolicyStateAuthorized(ctx, request, policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
		return policy.PolicyMutationDecision{}, nil
	}))
	if !errors.Is(err, policy.ErrAuthorizationDenied) {
		t.Fatalf("deny error = %v", err)
	}
	if len(policyEventsOfType(t, st, policy.EventPolicyDecisionAllowed)) != 0 {
		t.Fatal("denial created success event")
	}
	if len(policyEventsOfType(t, st, policy.EventPolicyDecisionDenied)) != 1 {
		t.Fatal("denial event missing")
	}
	loaded, err := st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil || loaded.State != policy.StateLoaded {
		t.Fatalf("denied state = %q, err=%v", loaded.State, err)
	}
}

func TestPolicyTransitionRetryReconcilesOneEventAfterReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "policy-events.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "events-retry")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	request := authorizedRequest(record)
	authorizer := policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
		return allowedDecision(request), nil
	})
	if _, err := st.TransitionPolicyStateAuthorized(ctx, request, authorizer); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionPolicyStateAuthorized(ctx, request, authorizer); err != nil {
		t.Fatal("exact retry: ", err)
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
	if got := len(policyEventsOfType(t, st, policy.EventPolicyDecisionAllowed)); got != 1 {
		t.Fatalf("allowed events after reopen = %d, want 1", got)
	}
}

func TestPolicyEventVocabularyRejectsUnknownType(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "events-vocabulary")
	request := authorizedRequest(record)
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = st.appendPolicyEvent(ctx, tx, policy.PolicyEventType("policy.unknown"), request, "allowed", string(policy.CodeAuthorizationAllowed))
	_ = tx.Rollback()
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("unknown event error = %v", err)
	}
}

func TestPolicyEventRejectsUnknownReason(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "events-reason")
	request := authorizedRequest(record)
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = st.appendPolicyEvent(ctx, tx, policy.EventPolicyDecisionAllowed, request, "allowed", "POLICY_UNKNOWN_REASON")
	_ = tx.Rollback()
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("unknown reason error = %v", err)
	}
}

func TestPolicyEventSecretMarkerNeverUsesAuthorizerError(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T48_A05_9f2a"
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "events-secret")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	request := authorizedRequest(record)
	_, err := st.TransitionPolicyStateAuthorized(ctx, request, policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
		return policy.PolicyMutationDecision{}, errors.New(marker)
	}))
	if !errors.Is(err, policy.ErrAuthorizationUnavailable) || strings.Contains(err.Error(), marker) {
		t.Fatalf("authorizer error = %v", err)
	}
	var raw string
	if err := st.db.QueryRowContext(ctx, "SELECT COALESCE(group_concat(data_json), '') FROM audit_events").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, marker) {
		t.Fatal("secret marker persisted in policy events")
	}
}
