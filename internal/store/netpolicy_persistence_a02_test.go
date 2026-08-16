package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

func TestA05EgressDecisionEmitsIdempotentSecurityEvents(t *testing.T) {
	ctx := context.Background()
	st := projectStore(t)
	importTasks(t, st, model.Task{ID: "TASK-A05", Title: "network event", Status: model.TaskReady, Risk: model.R1})
	record := netpolicy.DecisionRecord{
		ID: "decision-a05", IdempotencyKey: "request-a05", CreatedAt: time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC),
		Request:  netpolicy.Request{SubjectID: "agent-a05", TaskID: "TASK-A05", ChangeID: "change-a05", Host: "github.com", IP: "140.82.112.3", Protocol: netpolicy.ProtocolTCP, Port: 443},
		Decision: netpolicy.Decision{Allowed: true, RuleID: "rule-github-443", Reason: netpolicy.ReasonAllowed, Host: "github.com", IP: "140.82.112.3", Port: 443},
	}
	if err := st.PutEgressDecision(ctx, record); err != nil {
		t.Fatalf("first PutEgressDecision: %v", err)
	}
	if err := st.PutEgressDecision(ctx, record); err != nil {
		t.Fatalf("retry PutEgressDecision: %v", err)
	}
	got, err := st.Since(ctx, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var requested, allowed int
	for _, event := range got {
		if event.TaskID != events.TaskID(record.Request.TaskID) || event.RunID != "" {
			continue
		}
		switch event.Type {
		case events.Type("network.egress.requested"):
			requested++
		case events.Type("network.egress.allowed"):
			allowed++
		}
	}
	if requested != 1 || allowed != 1 {
		t.Fatalf("requested=%d allowed=%d, want one of each", requested, allowed)
	}
}

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
