package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/netpolicy"
)

func TestA02EgressDecisionProjectionPersistsAndReconstructsIdempotently(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='egress_decisions'"); got != 1 {
		t.Fatalf("egress_decisions table=%d, want 1", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='index' AND name='egress_decisions_by_created'"); got != 1 {
		t.Fatalf("egress_decisions index=%d, want 1", got)
	}
	record := netpolicy.DecisionRecord{
		ID: "decision-a02", IdempotencyKey: "request-a02", CreatedAt: time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC),
		Request:  netpolicy.Request{Host: "github.com", IP: "140.82.112.3", Protocol: netpolicy.ProtocolTCP, Port: 443},
		Decision: netpolicy.Decision{Allowed: true, RuleID: "rule-github-443", Reason: netpolicy.ReasonAllowed, Host: "github.com", IP: "140.82.112.3", Port: 443},
	}
	if err := st.PutEgressDecision(ctx, record); err != nil {
		t.Fatalf("PutEgressDecision: %v", err)
	}
	if err := st.PutEgressDecision(ctx, record); err != nil {
		t.Fatalf("idempotent PutEgressDecision: %v", err)
	}
	got, err := st.GetEgressDecision(ctx, record.ID)
	if err != nil {
		t.Fatalf("GetEgressDecision: %v", err)
	}
	if got != record {
		t.Fatalf("reconstructed record=%+v, want=%+v", got, record)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM egress_decisions"); got != 1 {
		t.Fatalf("projection rows=%d, want 1", got)
	}
}

func TestA02InvalidEgressDecisionIsRejectedBeforePersistence(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := netpolicy.DecisionRecord{
		ID: "decision-invalid", IdempotencyKey: "request-invalid", CreatedAt: time.Now().UTC(),
		Request:  netpolicy.Request{Host: "github.com", Protocol: netpolicy.ProtocolTCP, Port: 443},
		Decision: netpolicy.Decision{Allowed: true, RuleID: "rule-foreign", Reason: netpolicy.ReasonAllowed, Host: "evilgithub.com", Port: 443},
	}
	if err := st.PutEgressDecision(ctx, record); err == nil {
		t.Fatal("invalid decision unexpectedly persisted")
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM egress_decisions"); got != 0 {
		t.Fatalf("invalid projection rows=%d, want 0", got)
	}
}
