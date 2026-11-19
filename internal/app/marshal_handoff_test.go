package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestMarshalGoalHandoffAbsentOrTerminal(t *testing.T) {
	rt := browserContextRuntime(t)
	ctx := t.Context()

	if _, ok, err := rt.MarshalGoalHandoff(ctx, "session-empty"); err != nil || ok {
		t.Fatalf("empty session: ok=%v err=%v", ok, err)
	}

	now := time.Now().UTC()
	cancelled := model.GoalContract{
		ID:              "GOAL-cancelled",
		SessionID:       "session-cancelled",
		Revision:        1,
		DesiredOutcome:  "Aborted work",
		Risk:            model.R1,
		AuthoritySource: "operator:test",
		Confirmation:    model.ConfirmationCancelled,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := rt.Store().SaveGoalContract(ctx, cancelled, 0); err != nil {
		t.Fatalf("save cancelled goal: %v", err)
	}
	if _, ok, err := rt.MarshalGoalHandoff(ctx, cancelled.SessionID); err != nil || ok {
		t.Fatalf("cancelled goal must not produce handoff: ok=%v err=%v", ok, err)
	}

	finished := model.GoalContract{
		ID:              "GOAL-done",
		SessionID:       "session-done",
		Revision:        1,
		DesiredOutcome:  "Already shipped",
		Risk:            model.R1,
		AuthoritySource: "operator:test",
		Confirmation:    model.ConfirmationApproved,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := rt.Store().SaveGoalContract(ctx, finished, 0); err != nil {
		t.Fatalf("save finished goal: %v", err)
	}
	if err := rt.Store().SaveGoalTermination(ctx, model.GoalTermination{
		SessionID:    finished.SessionID,
		GoalID:       finished.ID,
		GoalRevision: finished.Revision,
		State:        model.StateSuccess,
		ReasonCode:   "GOAL_COMPLETE",
		ReasonDetail: "verified and merged",
		CompletedAt:  now,
	}); err != nil {
		t.Fatalf("record termination: %v", err)
	}
	if _, ok, err := rt.MarshalGoalHandoff(ctx, finished.SessionID); err != nil || ok {
		t.Fatalf("succeeded goal must not produce handoff: ok=%v err=%v", ok, err)
	}
}

func TestMarshalGoalHandoffBuildsFromRecordsAndStaysBounded(t *testing.T) {
	rt := browserContextRuntime(t)
	ctx := t.Context()

	base := time.Now().UTC().Add(-time.Hour)
	goal := model.GoalContract{
		ID:                 "GOAL-handoff-1",
		SessionID:          "session-handoff",
		Revision:           1,
		OriginalRequest:    "Build a clean CLI report generator",
		DesiredOutcome:     "Add a CLI report generator with JSON and Markdown outputs",
		Scope:              []string{"cmd/report", "internal/report"},
		Constraints:        []model.Constraint{{ID: "c1", Text: "No new third-party dependencies", Source: "operator", IsHard: true, Scope: "repo"}},
		DoNotDo:            []string{"Do not modify existing database schema"},
		SuccessCriteria:    []string{"go test ./internal/report passes"},
		Risk:               model.R1,
		AuthoritySource:    "operator:test",
		Confirmation:       model.ConfirmationApproved,
		UnderstandingState: model.GoalReady,
		UnresolvedDecisions: []model.UnresolvedDecision{
			{ID: "d1", Question: "Should Markdown output include timestamps?", Options: []string{"yes", "no"}, RequiresUser: true},
		},
		CreatedAt: base,
		UpdatedAt: base,
	}
	if err := rt.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("save goal: %v", err)
	}

	writeDesign := func(id, stage, provider, path, content string, created time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"provenance": map[string]any{"stage": stage, "provider": provider, "egm_version": "v0.1.0", "path": path},
			"content":    content,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := rt.Store().WriteMemoryV2(ctx, model.MemoryRecordV2{
			ID:         id,
			ProjectID:  rt.ProjectID(),
			Kind:       model.MemoryKindDecision,
			Lifecycle:  model.MemoryCandidate,
			Title:      "EGM " + stage,
			Body:       string(body),
			Scope:      "project",
			ScopeID:    rt.ProjectID(),
			Source:     model.MemorySource{Kind: "egm", Reference: "egm:" + stage},
			ObservedAt: created,
			ValidFrom:  created,
			CreatedAt:  created,
			UpdatedAt:  created,
		}); err != nil {
			t.Fatalf("write design %s: %v", id, err)
		}
	}
	// Earlier stale design from before this goal must be ignored.
	writeDesign("MEM-stale", "approved_design", "agy", "design/old.md", "Old unrelated design", base.Add(-time.Hour))
	writeDesign("MEM-draft", "design_draft", "agy", "design/draft.md", "Initial draft", base.Add(5*time.Minute))
	writeDesign("MEM-approved", "approved_design", "agy", "design/report.md", "Streaming renderer with JSON and Markdown formatters", base.Add(15*time.Minute))

	h, ok, err := rt.MarshalGoalHandoff(ctx, goal.SessionID)
	if err != nil || !ok {
		t.Fatalf("MarshalGoalHandoff: ok=%v err=%v", ok, err)
	}
	h.SwitchedFrom = "Antigravity"
	h.SwitchedTo = "Codex"

	formatted := FormatMarshalHandoff(h)
	for _, want := range []string{
		MarshalGoalHandoffHeader,
		"Antigravity to Codex",
		"do not record a new Goal",
		goal.ID,
		goal.DesiredOutcome,
		"design approved",
		"go test ./internal/report passes",
		"No new third-party dependencies",
		"design/report.md",
		"Streaming renderer with JSON and Markdown formatters",
		"Should Markdown output include timestamps? (options: yes, no)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted handoff missing %q:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "Old unrelated design") {
		t.Fatalf("stale pre-goal design leaked into handoff:\n%s", formatted)
	}
	if len(formatted) > marshalGoalHandoffMaxBytes {
		t.Fatalf("formatted handoff exceeded byte budget: %d > %d", len(formatted), marshalGoalHandoffMaxBytes)
	}

	// An active run updates the derived phase.
	run := marshal.Run{
		PlanID:      "PLAN-handoff",
		PlanVersion: 1,
		Tier:        marshal.Standard,
		Settings:    marshal.DefaultSettings(),
		State:       marshal.Dispatching,
		Tasks: []marshal.Task{
			{PlanTaskID: "T-1", Title: "first", Worker: "codex", State: marshal.Merged, Files: []string{"a.go"}, Criteria: []string{"go test passes"}},
			{PlanTaskID: "T-2", Title: "second", Worker: "claude", State: marshal.Dispatched, Files: []string{"b.go"}, Criteria: []string{"go test passes"}},
		},
	}
	if _, err := rt.Store().SetMarshalRun(ctx, rt.ProjectID(), "RUN-active", run, 0); err != nil {
		t.Fatalf("save marshal run: %v", err)
	}
	h2, ok, err := rt.MarshalGoalHandoff(ctx, goal.SessionID)
	if err != nil || !ok {
		t.Fatalf("MarshalGoalHandoff with active run: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(h2.Phase, "dispatching") || !strings.Contains(h2.Phase, "1 of 2 tasks merged") {
		t.Fatalf("active run phase = %q", h2.Phase)
	}
}
