package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestEvidenceStateTransitionsAreExplicitAndIdempotent(t *testing.T) {
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), allowingAuthorizer{})
	node := testEvidenceNode("EVIDENCE-STATE", "claim", "state")
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateArchived}); !errors.Is(err, evidence.ErrInvalidTransition) {
		t.Fatalf("illegal transition error = %v, want %v", err, evidence.ErrInvalidTransition)
	}
	if err := st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked}); err != nil {
		t.Fatalf("linked transition: %v", err)
	}
	if err := st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked}); err != nil {
		t.Fatalf("idempotent linked transition: %v", err)
	}
	if got, err := st.Get(context.Background(), node.ID); err != nil || got.State != evidence.StateLinked {
		t.Fatalf("state = %q, err=%v", got.State, err)
	}
}
