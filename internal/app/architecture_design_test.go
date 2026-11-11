package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestArchitectureDesignReadsCurrentGoalRecords(t *testing.T) {
	runtime := browserContextRuntime(t)
	ctx := t.Context()

	if runtime.architectureDesign("") != nil {
		t.Fatal("an empty session must have no design reader")
	}

	base := time.Now().UTC().Add(-time.Hour)
	goal := model.GoalContract{
		ID:                  "goal-design",
		SessionID:           "session-design",
		Revision:            1,
		DesiredOutcome:      "Ship the browser design overlay",
		ExpectedArtifact:    "architecture review with design",
		Scope:               []string{"internal/architecturereview"},
		Constraints:         []model.Constraint{{ID: "c-1", Text: "No fake completion", Source: "operator", IsHard: true, Scope: "global"}},
		DoNotDo:             []string{"Do not bypass approval gates"},
		SuccessCriteria:     []string{"Design records are shown"},
		Risk:                model.R2,
		AuthoritySource:     "operator:zen1th",
		BudgetRef:           "budget-design",
		UnderstandingState:  model.GoalReady,
		UnresolvedDecisions: nil,
		Assumptions:         []model.Assumption{{ID: "a-1", Text: "Go toolchain present", Risk: "low", IsReversible: true, CreatedBy: "operator"}},
		RepoCommit:          "deadbee",
		CreatedAt:           base,
		UpdatedAt:           base,
	}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("save goal: %v", err)
	}

	write := func(id, stage, provider, content string, kind model.MemoryKind, created time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"provenance": map[string]any{"stage": stage, "provider": provider, "egm_version": "1", "path": "design/x.md"},
			"content":    content,
		})
		if err != nil {
			t.Fatal(err)
		}
		record := model.MemoryRecordV2{
			ID:         id,
			ProjectID:  runtime.ProjectID(),
			Kind:       kind,
			Lifecycle:  model.MemoryCandidate,
			Title:      "EGM " + stage,
			Body:       string(body),
			Scope:      "project",
			ScopeID:    runtime.ProjectID(),
			Source:     model.MemorySource{Kind: "egm", Reference: "egm:" + stage},
			ObservedAt: created,
			ValidFrom:  created,
			CreatedAt:  created,
			UpdatedAt:  created,
		}
		if err := runtime.Store().WriteMemoryV2(ctx, record); err != nil {
			t.Fatalf("write memory %s: %v", id, err)
		}
	}

	// One finding before the goal must be ignored; a brief and a later finding
	// after the goal must be returned.
	write("MEM-old-finding", "review_findings", "codex", "Stale finding", model.MemoryKindFinding, base.Add(-time.Hour))
	write("MEM-brief", "concepts_brief", "codex", "The brief", model.MemoryKindDecision, base.Add(time.Minute))
	write("MEM-new-finding", "review_findings", "codex", "Fresh finding", model.MemoryKindFinding, base.Add(time.Hour))
	write("MEM-resolution", "resolution", "codex", "How it was resolved", model.MemoryKindDecision, base.Add(2*time.Hour))

	reader := runtime.architectureDesign(goal.SessionID)
	if reader == nil {
		t.Fatal("expected a design reader for a real session")
	}
	raw, err := reader(ctx)
	if err != nil {
		t.Fatalf("read design: %v", err)
	}
	var out struct {
		Brief *struct {
			Provider string `json:"provider"`
			Text     string `json:"text"`
		} `json:"brief"`
		Findings []struct {
			Provider string `json:"provider"`
			Text     string `json:"text"`
		} `json:"findings"`
		Resolutions []struct {
			Text string `json:"text"`
		} `json:"resolutions"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode design: %v", err)
	}
	if out.Brief == nil || out.Brief.Text != "The brief" {
		t.Fatalf("brief = %+v, want the goal's brief", out.Brief)
	}
	if len(out.Findings) != 1 || out.Findings[0].Text != "Fresh finding" {
		t.Fatalf("findings = %+v, want only the fresh finding", out.Findings)
	}
	if len(out.Resolutions) != 1 || out.Resolutions[0].Text != "How it was resolved" {
		t.Fatalf("resolutions = %+v, want the one resolution", out.Resolutions)
	}
}
