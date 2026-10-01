package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/policy"
)

func TestSummarizeGateDecisionsCountsWhatWasDecided(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/gate-summary.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	empty, err := s.SummarizeGateDecisions(ctx)
	if err != nil || empty.Allowed+empty.Denied != 0 || empty.LastAt != "" {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	digest := policy.PolicyDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	for i, allowed := range []bool{true, true, false} {
		d := gate.Decision{
			DecisionID: "d" + string(rune('a'+i)), Point: gate.GatePointPreExecution, Subject: "agent", Resource: "repo",
			Allowed: allowed, Checks: []gate.CheckResult{{CheckID: "c", Status: gate.CheckStatusPass, EvidenceID: "e", Reason: gate.CodeAllowed}},
			PolicyDigest: digest, CreatedAt: time.Date(2026, 9, 1, 12, i, 0, 0, time.UTC),
		}
		if err := s.PutGateDecision(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.SummarizeGateDecisions(ctx)
	if err != nil || got.Allowed != 2 || got.Denied != 1 || got.LastPoint != string(gate.GatePointPreExecution) || got.LastDigest != string(digest) {
		t.Fatalf("summary: %+v %v", got, err)
	}
}
