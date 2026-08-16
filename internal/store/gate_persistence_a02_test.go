package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/policy"
)

func TestGateDecisionPersistenceIsIdempotentAndReopens(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/gate-a02.db"
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	decision := gate.Decision{
		DecisionID: "decision-a02", Point: gate.GatePointPrePush, Subject: "agent-a02", Resource: "repo:a02",
		Allowed: true, Checks: []gate.CheckResult{{CheckID: "secret-scan", Status: gate.CheckStatusPass, EvidenceID: "evidence-a02", Reason: gate.CodeAllowed}},
		PolicyDigest: policy.PolicyDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		ChangeDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CreatedAt:    time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
	}
	if err := first.PutGateDecision(ctx, decision); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := first.PutGateDecision(ctx, decision); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, err := second.GetGateDecision(ctx, decision.DecisionID)
	if err != nil || got.DecisionID != decision.DecisionID || got.Subject != decision.Subject || !got.Allowed {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	if got := queryInt(t, second.db, "SELECT count(*) FROM gate_decisions"); got != 1 {
		t.Fatalf("rows=%d want=1", got)
	}
}
