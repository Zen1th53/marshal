package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestM15_ProviderNeutralHandoffCompilation(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	const taskID = "TASK-HANDOFF-10"
	pDev := testPrincipal("agent-claude")
	pOther := testPrincipal("agent-secretive")
	grantTaskMemoryAccess(t, rt, taskID, pDev)

	// 1. Create task in store
	task := model.Task{
		ID:     taskID,
		Title:  "Refactor Memory Pipeline",
		Status: model.TaskReady,
		Risk:   model.R1,
	}
	if _, err := rt.Store().ImportTasks(ctx, []model.Task{task}); err != nil {
		t.Fatalf("import task: %v", err)
	}

	// 2. Set task working memory slots
	_, err := svc.SetTaskSlot(ctx, pDev, projectID, taskID, working.SlotPlanState, "Step 2 of 4: updating handoff surfaces", true)
	if err != nil {
		t.Fatalf("set task slot: %v", err)
	}

	// 3. Write task-scoped verified finding with evidence
	now := time.Now().UTC()
	taskFinding := model.MemoryRecordV2{
		ID:          "MEM-TASK-FINDING-1",
		ProjectID:   projectID,
		Kind:        model.MemoryKindFinding,
		Lifecycle:   model.MemoryVerified,
		Confidence:  model.ConfidenceVerified,
		Authority:   model.AuthorityVerified,
		Title:       "Memory Pipeline Benchmark Baseline",
		Body:        "Baseline p95 retrieval latency is 14ms",
		Scope:       string(model.ScopeTask),
		ScopeID:     taskID,
		EvidenceIDs: []string{"EVID-BENCH-99"},
		ObservedAt:  now,
		IngestedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		Source:      model.MemorySource{Kind: "agent", Reference: "agent-claude"},
	}
	if err := rt.Store().WriteMemoryV2(ctx, taskFinding); err != nil {
		t.Fatalf("write task finding: %v", err)
	}
	svc.IndexRecord(ctx, taskFinding)

	// 4. Other agent writes private memory
	otherPriv := model.MemoryRecordV2{
		ID:         "MEM-PRIV-OTHER",
		ProjectID:  projectID,
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Confidence: model.ConfidenceVerified,
		Authority:  model.AuthorityAgent,
		Title:      "Other Agent Hidden Scratch",
		Body:       "Secret temporary calculation",
		Scope:      string(model.ScopeOperatorPrivate),
		ScopeID:    "agent-secretive",
		ACLScope:   "agent-secretive",
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "agent", Reference: "agent-secretive"},
	}
	if err := rt.Store().WriteMemoryV2(ctx, otherPriv); err != nil {
		t.Fatalf("write other private: %v", err)
	}
	svc.IndexRecord(ctx, otherPriv)

	// 5. Compile handoff bundle for handoff to Codex
	bundle, err := svc.CompileHandoff(ctx, pDev, HandoffCompileRequest{
		ProjectID:     projectID,
		TaskID:        taskID,
		SourceAgentID: "agent-claude",
		TargetRole:    "developer",
		CurrentHead:   "commit-abc123",
		CurrentBranch: "main",
		ChangedFiles:  []string{"internal/app/memory_runtime.go"},
		DiffHash:      "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("CompileHandoff: %v", err)
	}

	if bundle.BundleID == "" || bundle.TaskID != taskID {
		t.Fatalf("unexpected bundle header: %+v", bundle)
	}
	if len(bundle.WorkingSlots) == 0 || bundle.WorkingSlots[0].Type != working.SlotPlanState {
		t.Fatalf("working slots not packaged: %+v", bundle.WorkingSlots)
	}
	if len(bundle.EvidenceIDs) == 0 || bundle.EvidenceIDs[0] != "EVID-BENCH-99" {
		t.Fatalf("evidence IDs not packaged: %+v", bundle.EvidenceIDs)
	}
	if strings.Contains(bundle.MemoryContext, "Secret temporary calculation") {
		t.Fatalf("private memory from other agent leaked into compiled handoff: %s", bundle.MemoryContext)
	}
	_ = pOther
}
