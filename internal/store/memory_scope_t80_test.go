package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestT80StoreScopeAndACLIsolation(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	projA := "PROJ-T80-A"
	projB := "PROJ-T80-B"

	if err := st.InitProject(ctx, model.Project{
		ID: projA, Repository: "repoA", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID: projB, Repository: "repoB", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// 1. Write Project-scoped record in ProjA
	recA := model.MemoryRecordV2{
		ID:         "MEM-SCOPED-A",
		ProjectID:  projA,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryDurable,
		Authority:  model.AuthorityVerified,
		Title:      "Project A Architecture",
		Body:       "Confidential internal design for Project A",
		Scope:      string(model.ScopeProject),
		ScopeID:    projA,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "runtime", Reference: "task-1"},
	}
	if err := st.WriteMemoryV2(ctx, recA); err != nil {
		t.Fatalf("WriteMemoryV2 recA: %v", err)
	}

	// 2. Write OperatorPrivate-scoped record in ProjA
	recPrivate := model.MemoryRecordV2{
		ID:         "MEM-PRIVATE-OP",
		ProjectID:  projA,
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Authority:  model.AuthorityOperator,
		Title:      "Operator Secret Notes",
		Body:       "Sensitive operator instructions",
		Scope:      string(model.ScopeOperatorPrivate),
		ScopeID:    "admin-root",
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "user", Reference: "admin-root"},
	}
	if err := st.WriteMemoryV2(ctx, recPrivate); err != nil {
		t.Fatalf("WriteMemoryV2 recPrivate: %v", err)
	}

	// Test 1: Cross-Project Isolation (ProjB cannot see ProjA records)
	resB, err := st.ListMemoryV2(ctx, MemoryQueryFilter{
		ProjectID: projB,
	})
	if err != nil {
		t.Fatalf("ListMemoryV2 ProjB: %v", err)
	}
	if len(resB) != 0 {
		t.Fatalf("cross-project leakage: ProjB saw %d records from ProjA", len(resB))
	}

	// Test 2: Standard agent query on ProjA does NOT receive operator-private memory
	resAgent, err := st.ListMemoryV2(ctx, MemoryQueryFilter{
		ProjectID: projA,
		ActorID:   "agent-dev-1",
	})
	if err != nil {
		t.Fatalf("ListMemoryV2 agent: %v", err)
	}
	for _, r := range resAgent {
		if r.Scope == string(model.ScopeOperatorPrivate) {
			t.Fatalf("operator-private memory leaked to unauthorized agent: %+v", r)
		}
	}

	// Test 3: Authorized operator query on ProjA receives operator-private memory
	resOp, err := st.ListMemoryV2(ctx, MemoryQueryFilter{
		ProjectID: projA,
		ActorID:   "admin-root",
	})
	if err != nil {
		t.Fatalf("ListMemoryV2 operator: %v", err)
	}
	foundPrivate := false
	for _, r := range resOp {
		if r.ID == "MEM-PRIVATE-OP" {
			foundPrivate = true
			break
		}
	}
	if !foundPrivate {
		t.Fatal("authorized operator was denied access to operator-private memory")
	}

	// Test 4: Adversarial ID guessing across projects
	_, err = st.GetMemoryV2(ctx, projB, "MEM-SCOPED-A")
	if err == nil {
		t.Fatal("expected GetMemoryV2 to fail when accessing ProjA record from ProjB context")
	}
}
