package conflict_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/conflict"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT88StructuredContradictionDetection(t *testing.T) {
	detector := conflict.NewDetector()
	ctx := context.Background()

	now := time.Now().UTC()

	// 1. Version contradiction on same dependency/subject in same scope
	recA := model.MemoryRecordV2{
		ID:        "MEM-DEP-V1",
		ProjectID: "PROJ-1",
		Kind:      model.MemoryKindDecision,
		Lifecycle: model.MemoryDurable,
		Title:     "Go Runtime Dependency",
		Body:      "Runtime requires Go version 1.22",
		Scope:     string(model.ScopeProject),
		ScopeID:   "PROJ-1",
		ObservedAt: now.Add(-time.Hour),
		ValidFrom:  now.Add(-time.Hour),
	}

	recB := model.MemoryRecordV2{
		ID:        "MEM-DEP-V2",
		ProjectID: "PROJ-1",
		Kind:      model.MemoryKindDecision,
		Lifecycle: model.MemoryCandidate,
		Title:     "Go Runtime Dependency",
		Body:      "Runtime requires Go version 1.24", // Contradiction
		Scope:     string(model.ScopeProject),
		ScopeID:   "PROJ-1",
		ObservedAt: now,
		ValidFrom:  now,
	}

	isConflict, reason := detector.DetectConflict(ctx, recA, recB)
	if !isConflict {
		t.Fatal("expected conflict detection for contradictory version requirement")
	}
	if reason == "" {
		t.Fatal("expected explanation reason for conflict")
	}

	// 2. Mark conflicted updates both records with mutual ConflictIDs
	confA, confB := detector.LinkConflict(recA, recB, reason)
	if confA.Lifecycle != model.MemoryConflicted || confB.Lifecycle != model.MemoryConflicted {
		t.Fatalf("expected both records to have lifecycle Conflicted, got: %s and %s", confA.Lifecycle, confB.Lifecycle)
	}
	if len(confA.ConflictIDs) == 0 || confA.ConflictIDs[0] != recB.ID {
		t.Fatalf("expected confA to link to %s, got: %+v", recB.ID, confA.ConflictIDs)
	}
	if len(confB.ConflictIDs) == 0 || confB.ConflictIDs[0] != recA.ID {
		t.Fatalf("expected confB to link to %s, got: %+v", recA.ID, confB.ConflictIDs)
	}
}

func TestT88DifferentScopesDoNotConflict(t *testing.T) {
	detector := conflict.NewDetector()
	ctx := context.Background()

	now := time.Now().UTC()

	// Task 1 decision vs Task 2 decision on task-scope should not conflict across tasks
	recTask1 := model.MemoryRecordV2{
		ID:        "MEM-TASK-1",
		ProjectID: "PROJ-1",
		Kind:      model.MemoryKindDecision,
		Title:     "Working Directory",
		Body:      "Directory is /tmp/task1",
		Scope:     string(model.ScopeTask),
		ScopeID:   "TASK-1",
		ObservedAt: now,
		ValidFrom:  now,
	}

	recTask2 := model.MemoryRecordV2{
		ID:        "MEM-TASK-2",
		ProjectID: "PROJ-1",
		Kind:      model.MemoryKindDecision,
		Title:     "Working Directory",
		Body:      "Directory is /tmp/task2",
		Scope:     string(model.ScopeTask),
		ScopeID:   "TASK-2",
		ObservedAt: now,
		ValidFrom:  now,
	}

	isConflict, _ := detector.DetectConflict(ctx, recTask1, recTask2)
	if isConflict {
		t.Fatal("records in different task scopes must not be flagged as contradictory")
	}
}
