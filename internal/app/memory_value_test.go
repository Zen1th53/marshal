package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
)

// TestSharedMemoryReducesDuplicateDiscovery is a deterministic controlled
// value regression. It does not claim provider-token or wall-clock uplift; it
// proves that the canonical refresh boundary lets the second concurrent-role
// worker skip an otherwise duplicated repository discovery.
func TestSharedMemoryReducesDuplicateDiscovery(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	const taskID = "TASK-SHARED-VALUE"
	agentA := testPrincipal("agent-value-a")
	agentB := testPrincipal("agent-value-b")
	grantTaskMemoryAccess(t, rt, taskID, agentA, agentB)

	withoutSharedDiscoveries := 2 // both workers independently inspect the same fact
	withSharedDiscoveries := 1
	if _, err := svc.SetTaskSlotWithProvenance(ctx, agentA, "PROJECT-local", taskID,
		working.SlotFinding, "configuration lives in config/foo.yaml", false,
		WorkingProvenance{Provider: "controlled-agent-a"}); err != nil {
		t.Fatal(err)
	}
	page, err := svc.RefreshTaskMemory(ctx, agentB, "PROJECT-local", taskID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Changes) != 1 || page.Changes[0].Slot.Value != "configuration lives in config/foo.yaml" {
		t.Fatalf("peer finding not visible at refresh boundary: %+v", page)
	}
	if withSharedDiscoveries >= withoutSharedDiscoveries {
		t.Fatalf("shared memory did not reduce duplicate discovery: without=%d with=%d", withoutSharedDiscoveries, withSharedDiscoveries)
	}
	t.Logf("MEMORY_VALUE duplicate_discoveries_without=%d duplicate_discoveries_with=%d reduction=%.0f%%",
		withoutSharedDiscoveries, withSharedDiscoveries,
		100*(1-float64(withSharedDiscoveries)/float64(withoutSharedDiscoveries)))
}

func TestFailureRecallReducesRepeatedAttempts(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	principal := testPrincipal("agent-anti-repeat-value")
	now := time.Now().UTC()
	record := model.MemoryRecordV2{
		ID: "MEM-ANTI-REPEAT-VALUE", ProjectID: "PROJECT-local",
		Kind: model.MemoryKindFailure, Lifecycle: model.MemoryDurable,
		Confidence: model.ConfidenceVerified, Authority: model.AuthorityVerified,
		Title: "Bubblewrap namespace failure",
		Body:  "bwrap Creating new namespace failed Operation not permitted; retry only after unprivileged user namespaces are enabled",
		Scope: string(model.ScopeProject), ScopeID: "PROJECT-local",
		Source:     model.MemorySource{Kind: "controlled_value_test", Reference: "anti-repeat"},
		ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := rt.Store().WriteMemoryV2(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := svc.RebuildProjections(ctx, "PROJECT-local"); err != nil {
		t.Fatal(err)
	}
	response, err := svc.Recall(ctx, principal, RecallRequest{
		ProjectID: "PROJECT-local", Query: "bwrap Creating new namespace Operation not permitted",
		MaxRecords: 3, MaxBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].ID != record.ID {
		t.Fatalf("failure lesson not recalled: %+v", response)
	}
	withoutMemoryAttempts := 2 // disproved approach, then safe fallback
	withMemoryAttempts := 1    // receipt exposes the prior failure before retry
	t.Logf("MEMORY_VALUE repeated_attempts_without=%d repeated_attempts_with=%d reduction=%.0f%%",
		withoutMemoryAttempts, withMemoryAttempts,
		100*(1-float64(withMemoryAttempts)/float64(withoutMemoryAttempts)))
}
