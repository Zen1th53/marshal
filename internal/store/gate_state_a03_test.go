package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

func TestGateDecisionStateTransitionsUseExplicitCAS(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	decision := gate.Decision{
		DecisionID: "decision-a03", Point: gate.GatePointPreExecution, Subject: "agent-a03", Resource: "repo:a03",
		State: gate.DecisionStateRequested, Checks: []gate.CheckResult{{CheckID: "check", Status: gate.CheckStatusPass, Reason: gate.CodeAllowed}},
		PolicyDigest: policy.PolicyDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		CreatedAt:    time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
	}
	if err := st.PutGateDecision(ctx, decision); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionGateDecision(ctx, decision.DecisionID, "agent-a03", gate.DecisionStateRequested, gate.DecisionStateEvaluating); err != nil {
		t.Fatalf("requested to evaluating: %v", err)
	}
	if _, err := st.TransitionGateDecision(ctx, decision.DecisionID, "agent-a03", gate.DecisionStateEvaluating, gate.DecisionStateAllowed); err != nil {
		t.Fatalf("evaluating to allowed: %v", err)
	}
	if _, err := st.TransitionGateDecision(ctx, decision.DecisionID, "agent-a03", gate.DecisionStateRequested, gate.DecisionStateDenied); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale transition err=%v want conflict", err)
	}
	if _, err := st.TransitionGateDecision(ctx, decision.DecisionID, "", gate.DecisionStateAllowed, gate.DecisionStateConsumed); err == nil {
		t.Fatal("empty actor unexpectedly transitioned decision")
	}
	got, err := st.GetGateDecision(ctx, decision.DecisionID)
	if err != nil || got.State != gate.DecisionStateAllowed {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}
