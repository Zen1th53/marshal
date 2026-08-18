package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/gate"
)

type gateEventRecorder struct{ events []events.Event }

func (r *gateEventRecorder) Append(_ context.Context, event events.Event) (events.Event, error) {
	r.events = append(r.events, event)
	return event, nil
}
func (r *gateEventRecorder) Since(context.Context, events.Sequence) ([]events.Event, error) {
	return append([]events.Event(nil), r.events...), nil
}

type gateEventFailure struct{}

func (gateEventFailure) Append(context.Context, events.Event) (events.Event, error) {
	return events.Event{}, errors.New("downstream event unavailable")
}
func (gateEventFailure) Since(context.Context, events.Sequence) ([]events.Event, error) {
	return nil, nil
}

func TestGateDecisionDurableStatePrecedesBoundedAuditEvent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	decision := a05GateDecision()
	recorder := &gateEventRecorder{}
	if err := st.PutGateDecisionWithAudit(ctx, decision, recorder); err != nil {
		t.Fatal(err)
	}
	if len(recorder.events) != 1 || recorder.events[0].Type != events.EventTypeGateAllowed {
		t.Fatalf("events=%#v", recorder.events)
	}
	if recorder.events[0].Data["policy_digest"] != string(decision.PolicyDigest) || recorder.events[0].Data["resource"] != nil {
		t.Fatalf("event data=%#v", recorder.events[0].Data)
	}
	if got, err := st.GetGateDecision(ctx, decision.DecisionID); err != nil || got.DecisionID != decision.DecisionID {
		t.Fatalf("durable decision=%#v err=%v", got, err)
	}
}

func TestGateDecisionAuditDoesNotPersistRawResourceOrSecret(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	decision := a05GateDecision()
	decision.DecisionID = "decision-a05-secret"
	decision.Resource = "repo:a05/MARSHAL_TEST_SECRET_T20_A05"
	recorder := &gateEventRecorder{}
	if err := st.PutGateDecisionWithAudit(ctx, decision, recorder); err != nil {
		t.Fatal(err)
	}
	for _, event := range recorder.events {
		for key, value := range event.Data {
			valStr := fmt.Sprintf("%v", value)
			if strings.Contains(key, "secret") || strings.Contains(valStr, "MARSHAL_TEST_SECRET_T20_A05") {
				t.Fatalf("secret in event %s=%q", key, valStr)
			}
		}
		if strings.Contains(string(event.ResourceID), "MARSHAL_TEST_SECRET_T20_A05") {
			t.Fatal("raw secret resource in event")
		}
	}
}

func TestGateDecisionRetryReconcilesAfterEventFailure(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	decision := a05GateDecision()
	decision.DecisionID = "decision-a05-retry"
	if err := st.PutGateDecisionWithAudit(ctx, decision, gateEventFailure{}); err == nil {
		t.Fatal("event failure unexpectedly succeeded")
	}
	recorder := &gateEventRecorder{}
	if err := st.PutGateDecisionWithAudit(ctx, decision, recorder); err != nil {
		t.Fatal(err)
	}
	if len(recorder.events) != 1 || queryInt(t, st.db, "SELECT count(*) FROM gate_decisions WHERE decision_id = ?", decision.DecisionID) != 1 {
		t.Fatalf("events=%d durable=%d", len(recorder.events), queryInt(t, st.db, "SELECT count(*) FROM gate_decisions WHERE decision_id = ?", decision.DecisionID))
	}
}

func a05GateDecision() gate.Decision {
	return gate.Decision{
		DecisionID: "decision-a05", Point: gate.GatePointPrePush, Subject: "agent-a05", Resource: "repo:a05",
		Allowed: true, State: gate.DecisionStateAllowed,
		Checks:       []gate.CheckResult{{CheckID: "secret-scan", Status: gate.CheckStatusPass, EvidenceID: "evidence-a05", Reason: gate.CodeAllowed}},
		PolicyDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAt:    fixedGateTime(),
	}
}

func fixedGateTime() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) }
