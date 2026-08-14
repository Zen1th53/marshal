package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestEvidenceStateTransitionsAreExplicitAndIdempotent(t *testing.T) {
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	node := testEvidenceNode("EVIDENCE-STATE", "claim", "state")
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := st.TransitionNode(context.Background(), node.ID, evidence.StateArchived); !errors.Is(err, evidence.ErrInvalidTransition) {
		t.Fatalf("illegal transition error = %v, want %v", err, evidence.ErrInvalidTransition)
	}
	if err := st.TransitionNode(context.Background(), node.ID, evidence.StateLinked); err != nil {
		t.Fatalf("linked transition: %v", err)
	}
	if err := st.TransitionNode(context.Background(), node.ID, evidence.StateLinked); err != nil {
		t.Fatalf("idempotent linked transition: %v", err)
	}
	if got, err := st.Get(context.Background(), node.ID); err != nil || got.State != evidence.StateLinked {
		t.Fatalf("state = %q, err=%v", got.State, err)
	}
}
