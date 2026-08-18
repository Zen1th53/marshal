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
	got, err := st.Since(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	var requested, allowed int
	for _, event := range got {
		if event.TaskID != record.Request.TaskID || event.RunID != "" {
			continue
		}
		switch event.Type {
		case events.EventTypeNetworkEgressRequested:
			requested++
		case events.EventTypeNetworkEgressAllowed:
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
	record := netpolicy.DecisionRecord{
		ID:             "decision-a02",
		IdempotencyKey: "request-a02",
		CreatedAt:      time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC),
		Request: netpolicy.Request{
			SubjectID: "agent-a02",
			TaskID:    "TASK-A02",
			ChangeID:  "change-a02",
			Host:      "api.github.com",
			IP:        "140.82.112.4",
			Protocol:  netpolicy.ProtocolTCP,
			Port:      443,
		},
		Decision: netpolicy.Decision{
			Allowed: true,
			RuleID:  "rule-github-api",
			Reason:  netpolicy.ReasonAllowed,
			Host:    "api.github.com",
			IP:      "140.82.112.4",
			Port:    443,
		},
	}
	if err := st.PutEgressDecision(ctx, record); err != nil {
		t.Fatalf("first PutEgressDecision: %v", err)
	}
	if err := st.PutEgressDecision(ctx, record); err != nil {
		t.Fatalf("idempotent PutEgressDecision: %v", err)
	}
	reloaded, err := st.GetEgressDecision(ctx, record.ID)
	if err != nil {
		t.Fatalf("GetEgressDecision: %v", err)
	}
	if reloaded.ID != record.ID || reloaded.IdempotencyKey != record.IdempotencyKey || !reloaded.Decision.Allowed || reloaded.Decision.RuleID != "rule-github-api" {
		t.Fatalf("reloaded=%+v, want original projection", reloaded)
	}
}
