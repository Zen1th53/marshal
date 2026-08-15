package store

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
)

// TestA10PolicyLifecycleReleaseIntegration exercises the canonical persistence,
// authorized transition, durable event, and restart path as one release flow.
func TestA10PolicyLifecycleReleaseIntegration(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/policy-release.db"
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		t.Fatal(err)
	}

	record := testPolicyRecord(t, "a10-release")
	if err := st.PutPolicy(ctx, record); err != nil {
		st.Close()
		t.Fatal(err)
	}
	record, err = st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	authorizer := policyMutationAuthorizer(func(_ context.Context, request policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
		return allowedDecision(request), nil
	})

	for _, target := range []policy.State{policy.StateValidated, policy.StateActive, policy.StateSuperseded} {
		request := authorizedRequest(record)
		request.ExpectedState = record.State
		request.TargetState = target
		request.Binding = record.Binding
		switch target {
		case policy.StateValidated:
			request.Action = policy.ActionPolicyValidate
		case policy.StateActive:
			request.Action = policy.ActionPolicyActivate
		case policy.StateSuperseded:
			request.Action = policy.ActionPolicySupersede
		}
		next, err := st.TransitionPolicyStateAuthorized(ctx, request, authorizer)
		if err != nil {
			st.Close()
			t.Fatalf("transition %s -> %s: %v", record.State, target, err)
		}
		record = next
	}

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != policy.StateSuperseded {
		t.Fatalf("reopened state = %s, want %s", loaded.State, policy.StateSuperseded)
	}
	if _, err := reopened.GetActivePolicy(ctx); err == nil {
		t.Fatal("superseded-only policy unexpectedly selected as active")
	}
	if events := policyEventsOfType(t, reopened, policy.EventPolicyDecisionAllowed); len(events) != 2 {
		t.Fatalf("allowed lifecycle events = %d, want 2", len(events))
	}
	if events := policyEventsOfType(t, reopened, policy.EventPolicyActivated); len(events) != 1 {
		t.Fatalf("activation lifecycle events = %d, want 1", len(events))
	}
}
