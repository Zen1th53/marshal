//go:build !governed

package store

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// The Extreme overview carries the newest lane that ran unconfined on the
// host, read through the (event_type, sequence) index.
func TestSecurityOverviewUnconfined(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()
	run := marshal.Run{PlanID: "plan-1", PlanVersion: 1, ApprovalScopeDigest: "d", Tier: marshal.Ultra, Settings: marshal.DefaultSettings(), State: marshal.Dispatching}
	if err := s.SaveMarshalState(ctx, "p", "run-1", run, 0, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.SecurityOverview(ctx, "p", "run-1")
	if err != nil || got.Unconfined != nil {
		t.Fatalf("before an unconfined lane: %+v %v", got.Unconfined, err)
	}
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for i, data := range []map[string]any{
		{"decision": securityUnconfinedDecision, "confinement_cause": "bwrap unavailable"},
		{"decision": "allowed by policy"},
	} {
		if _, err := s.Append(ctx, events.Event{ID: "EVENT-P-" + string(rune('a'+i)), Type: events.EventTypePolicyAllowed, Subject: "codex-1", TaskID: "TASK-1", At: at, Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	got, err = s.SecurityOverview(ctx, "p", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if u := got.Unconfined; u == nil || u.Worker != "codex-1" || u.Task != "TASK-1" || u.Cause != "bwrap unavailable" || !u.At.Equal(at) {
		t.Fatalf("unconfined %+v", got.Unconfined)
	}
}
