package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
)

func TestGateDecisionPolicyMutationInvalidatesPriorDecision(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	decision := a05GateDecision()
	decision.DecisionID = "decision-a10-policy"
	if err := st.PutGateDecision(ctx, decision); err != nil {
		t.Fatal(err)
	}
	changed := policy.PolicyDigest("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if _, err := st.GetGateDecisionForPolicy(ctx, decision.DecisionID, changed); !errors.Is(err, ErrStaleGateDecision) {
		t.Fatalf("policy mutation error=%v want=%v", err, ErrStaleGateDecision)
	}
	if got, err := st.GetGateDecisionForPolicy(ctx, decision.DecisionID, decision.PolicyDigest); err != nil || got.DecisionID != decision.DecisionID {
		t.Fatalf("matching policy got=%#v err=%v", got, err)
	}
}
