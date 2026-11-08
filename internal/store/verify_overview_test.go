package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestVerifyOverview(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()

	if _, err := s.VerifyOverview(ctx, "run-x"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing run: %v", err)
	}
	checks := []marshal.Check{{Command: "go test ./a", Criteria: []string{"a works"}}, {Command: "go test ./b", Criteria: []string{"b works"}}}
	run := marshal.Run{
		PlanID: "plan-1", PlanVersion: 1, ApprovalScopeDigest: "d", BaseCommit: "base", Tier: marshal.Ultra, Settings: marshal.DefaultSettings(), State: marshal.Reviewing,
		Tasks: []marshal.Task{
			{PlanTaskID: "T-1", Title: "first", Worker: "codex", State: marshal.HandedIn, Checks: checks, Criteria: []string{"a works", "b works"}},
			{PlanTaskID: "T-2", Title: "second", Worker: "claude", State: marshal.Merged},
			{PlanTaskID: "T-3", Title: "third", Worker: "agy", State: marshal.Queued, Criteria: []string{"c works"}},
		},
	}
	if err := s.SaveMarshalState(ctx, "p", "run-1", run, 0, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	h1 := marshal.HandIn{ResultCommit: "r1", Provider: "codex", Claims: []string{"old claim"}}
	h2 := marshal.HandIn{ResultCommit: "r2", Provider: "codex", Claims: []string{"restore keeps tags"},
		CheckResults: []marshal.CheckResult{{Command: "go test ./a", Criteria: []string{"a works"}, Passed: true, ResultCommit: "r2"}, {Command: "go test ./b", Criteria: []string{"b works"}, Passed: false, ResultCommit: "r2"}}}
	for i, h := range []marshal.HandIn{h1, h2} {
		if _, err := s.SetMarshalHandIn(ctx, "run-1", "T-1", i+1, h); err != nil {
			t.Fatal(err)
		}
	}
	review := marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal", Independent: &marshal.IndependentReview{Verdict: marshal.VerdictReturn, Reviewer: "claude-1", Provider: "claude", Reasons: []string{"no test for tags"}}}
	if _, err := s.SetMarshalReview(ctx, "run-1", "T-1", 2, review); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMarshalHandIn(ctx, "run-1", "T-2", 1, marshal.HandIn{ResultCommit: "r3", Provider: "claude", Claims: []string{"digest is stable"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMarshalReview(ctx, "run-1", "T-2", 1, marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal", EvidenceRefs: []string{"file:a.go"}, Independent: &marshal.IndependentReview{Verdict: marshal.VerdictAccept, Reviewer: "claude-2", Provider: "claude", EvidenceRefs: []string{"file:a.go", "diff"}}}); err != nil {
		t.Fatal(err)
	}
	for i, verdict := range []string{"fail", "pass"} {
		data := map[string]any{"verifier": marshal.VerifierEvidence{Reviewer: "agy-v", Provider: "agy", Verdict: verdict, Commit: "head"}}
		if _, err := s.AppendMarshalDecision(ctx, events.Event{ID: "ver-" + verdict, Type: events.EventTypeMarshalVerifierRecorded, Subject: "p", RunID: "run-1", At: time.Date(2026, 11, 3, 12, i, 0, 0, time.UTC), Data: data}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.VerifyOverview(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tasks) != 3 || got.Tasks[0].ID != "T-1" || got.Tasks[2].ID != "T-3" {
		t.Fatalf("tasks %+v", got.Tasks)
	}
	t1 := got.Tasks[0]
	if t1.Attempt != 2 || t1.Reviewer != "claude-1" || !t1.OtherProvider || t1.Verdict != marshal.VerdictReturn || len(t1.Claims) != 3 || t1.Supported() != 1 {
		t.Fatalf("T-1 %+v", t1)
	}
	want := []VerifyClaim{
		{Status: ClaimSupported, Text: "a works", Evidence: "check:go test ./a", CheckedBy: "runtime", Criterion: true},
		{Status: ClaimUnsupported, Text: "b works", Evidence: "check:go test ./b failed", CheckedBy: "runtime", Criterion: true},
		{Status: ClaimUnsupported, Text: "restore keeps tags", Evidence: "no test for tags", CheckedBy: "claude-1"},
	}
	for i, c := range want {
		if t1.Claims[i] != c {
			t.Errorf("claim %d = %+v, want %+v", i, t1.Claims[i], c)
		}
	}
	t2 := got.Tasks[1]
	if t2.OtherProvider || t2.Supported() != 1 || t2.Claims[0].Evidence != "file:a.go diff" {
		t.Fatalf("T-2 same-provider cross-review %+v", t2)
	}
	if t3 := got.Tasks[2]; t3.Attempt != 0 || len(t3.Claims) != 0 {
		t.Fatalf("T-3 before hand-in %+v", t3)
	}
	if got.Verifier == nil || got.Verifier.Verdict != "pass" || got.Verifier.Provider != "agy" {
		t.Fatalf("verifier %+v", got.Verifier)
	}
	if got.Session != nil {
		t.Fatalf("session %+v", got.Session)
	}
}

// The overview reads through keys and indexes, never by scanning a table.
func TestVerifyOverviewQueryPlans(t *testing.T) {
	s := openTestStore(t)
	for _, q := range []string{
		`SELECT project_id, data_json FROM marshal_runs WHERE run_id = 'r'`,
		`SELECT attempt, data_json FROM marshal_handins WHERE run_id = 'r' AND task_id = 't' ORDER BY attempt DESC LIMIT 1`,
		`SELECT revision, data_json FROM marshal_reviews WHERE run_id = 'r' AND task_id = 't' AND attempt = 1`,
		`SELECT at, data_json FROM structured_events INDEXED BY structured_events_by_run_sequence WHERE run_id = 'r' AND event_type = 'x' ORDER BY sequence DESC LIMIT 1`,
		`SELECT verification_id, state, updated_at FROM verification_sessions INDEXED BY idx_verification_sessions_binding WHERE project_id = 'p' AND run_id = 'r' ORDER BY rowid DESC LIMIT 1`,
	} {
		rows, err := s.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		text := strings.Join(plan, "; ")
		if !strings.Contains(text, "USING") || strings.Contains(text, "json_extract") {
			t.Errorf("%s: plan %q does not use an index", q, text)
		}
	}
}
