package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
)

func TestPolicyTransitionRejectsMissingAuthority(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "auth-boundary")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	_, err := st.TransitionPolicyStateAuthorized(ctx, policy.PolicyMutationRequest{
		SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1",
		PolicyID: record.Policy.ID, PolicyVersion: record.Policy.Version,
		ExpectedState: policy.StateLoaded, TargetState: policy.StateValidated,
		Binding: record.Binding, Action: policy.ActionPolicyValidate,
	}, nil)
	if !errors.Is(err, policy.ErrAuthorizationUnavailable) {
		t.Fatalf("missing authority error = %v", err)
	}
	loaded, err := st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != policy.StateLoaded {
		t.Fatalf("state mutated to %q", loaded.State)
	}
}
