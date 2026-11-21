package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestT84TransactionalMemoryOutbox(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	projID := "PROJ-T84"
	if err := st.InitProject(ctx, model.Project{
		ID: projID, Repository: "repo", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// 1. Write Memory creates a 'memory.created' outbox event atomically
	rec := model.MemoryRecordV2{
		ID:         "MEM-OUTBOX-01",
		ProjectID:  projID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryCandidate,
		Authority:  model.AuthorityAgent,
		Title:      "Outbox Test Record",
		Body:       "Outbox test payload",
		Scope:      string(model.ScopeProject),
		ScopeID:    projID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "runtime", Reference: "run-1"},
	}

	if err := st.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatalf("WriteMemoryV2: %v", err)
	}

	// 2. Query unprocessed outbox events
	events, err := st.FetchUnprocessedMemoryOutbox(ctx, projID, 10)
	if err != nil {
		t.Fatalf("FetchUnprocessedMemoryOutbox: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(events))
	}
	if events[0].EventType != "memory.created" {
		t.Fatalf("expected eventType 'memory.created', got: %s", events[0].EventType)
	}
	if events[0].MemoryID != "MEM-OUTBOX-01" {
		t.Fatalf("expected memoryID 'MEM-OUTBOX-01', got: %s", events[0].MemoryID)
	}
	var pointer MemoryOutboxPointer
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &pointer); err != nil {
		t.Fatalf("decode bounded outbox pointer: %v", err)
	}
	if pointer.MemoryID != rec.ID || pointer.Lifecycle != rec.Lifecycle || len(events[0].PayloadJSON) > 512 {
		t.Fatalf("unexpected outbox pointer: payload=%s decoded=%+v", events[0].PayloadJSON, pointer)
	}

	// 3. UpdateMemory creates a 'memory.updated' outbox event atomically
	_, err = st.UpdateMemory(ctx, projID, "MEM-OUTBOX-01", 0, func(m *model.MemoryRecordV2) error {
		m.Body = "Updated body via CAS"
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	eventsUpdate, err := st.FetchUnprocessedMemoryOutbox(ctx, projID, 10)
	if err != nil {
		t.Fatalf("FetchUnprocessedMemoryOutbox after update: %v", err)
	}
	updates := 0
	for _, e := range eventsUpdate {
		if e.EventType == "memory.updated" && e.MemoryID == "MEM-OUTBOX-01" {
			updates++
		}
	}
	if len(eventsUpdate) != 2 || updates != 1 {
		t.Fatalf("expected the created event and 1 update event, got %d events (%d updates)", len(eventsUpdate), updates)
	}
}
