package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestT81MemoryCASUpdateAndConflict(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	projID := "PROJ-T81"
	if err := st.InitProject(ctx, model.Project{
		ID: projID, Repository: "repo", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	rec := model.MemoryRecordV2{
		ID:         "MEM-CAS-01",
		ProjectID:  projID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryCandidate,
		Authority:  model.AuthorityAgent,
		Title:      "Initial title",
		Body:       "Initial body",
		Scope:      string(model.ScopeProject),
		ScopeID:    projID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Revision:   0,
		Source:     model.MemorySource{Kind: "runtime", Reference: "run-1"},
	}

	if err := st.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatalf("WriteMemoryV2: %v", err)
	}

	// 1. Successful CAS Update with expected revision 0 -> becomes 1
	updated, err := st.UpdateMemory(ctx, projID, "MEM-CAS-01", 0, func(m *model.MemoryRecordV2) error {
		m.Body = "Updated body v1"
		m.Lifecycle = model.MemoryVerified
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateMemory rev 0: %v", err)
	}
	if updated.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", updated.Revision)
	}
	if updated.Body != "Updated body v1" {
		t.Fatalf("expected updated body, got %q", updated.Body)
	}

	// 2. Conflicting CAS Update with stale expected revision 0 -> ErrConflict
	_, err = st.UpdateMemory(ctx, projID, "MEM-CAS-01", 0, func(m *model.MemoryRecordV2) error {
		m.Body = "Stale concurrent update"
		return nil
	})
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("expected ErrConflict on stale revision, got: %v", err)
	}

	// 3. SupersedeMemory CAS operation
	superseded, err := st.SupersedeMemory(ctx, projID, "MEM-CAS-01", 1, "MEM-CAS-02")
	if err != nil {
		t.Fatalf("SupersedeMemory: %v", err)
	}
	if superseded.Lifecycle != model.MemorySuperseded {
		t.Fatalf("expected lifecycle superseded, got: %s", superseded.Lifecycle)
	}
	if len(superseded.SupersededBy) == 0 || superseded.SupersededBy[0] != "MEM-CAS-02" {
		t.Fatalf("expected superseded_by to contain MEM-CAS-02, got: %+v", superseded.SupersededBy)
	}

	// 4. TombstoneMemory CAS operation
	rec2 := model.MemoryRecordV2{
		ID:         "MEM-CAS-02",
		ProjectID:  projID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryCandidate,
		Authority:  model.AuthorityAgent,
		Title:      "Second title",
		Body:       "Second body",
		Scope:      string(model.ScopeProject),
		ScopeID:    projID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Revision:   0,
		Source:     model.MemorySource{Kind: "runtime", Reference: "run-2"},
	}
	if err := st.WriteMemoryV2(ctx, rec2); err != nil {
		t.Fatal(err)
	}

	tombstoned, err := st.TombstoneMemory(ctx, projID, "MEM-CAS-02", 0, "Obsolete requirement")
	if err != nil {
		t.Fatalf("TombstoneMemory: %v", err)
	}
	if tombstoned.Lifecycle != model.MemoryTombstoned {
		t.Fatalf("expected lifecycle tombstoned, got: %s", tombstoned.Lifecycle)
	}
}

func TestT81ConcurrentCASContention(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	projID := "PROJ-T81-CONC"
	if err := st.InitProject(ctx, model.Project{
		ID: projID, Repository: "repo", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	rec := model.MemoryRecordV2{
		ID:         "MEM-CONC-01",
		ProjectID:  projID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryCandidate,
		Authority:  model.AuthorityAgent,
		Title:      "Concurrent title",
		Body:       "Concurrent initial body",
		Scope:      string(model.ScopeProject),
		ScopeID:    projID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Revision:   0,
		Source:     model.MemorySource{Kind: "runtime", Reference: "run-conc"},
	}
	if err := st.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}

	// 10 concurrent goroutines all trying to CAS update revision 0
	// Exactly ONE must succeed and 9 must receive ErrConflict.
	var wg sync.WaitGroup
	successCount := 0
	conflictCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := st.UpdateMemory(ctx, projID, "MEM-CONC-01", 0, func(m *model.MemoryRecordV2) error {
				m.Body = "Concurrent winner body"
				return nil
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else if errors.Is(err, model.ErrConflict) {
				conflictCount++
			}
		}(i)
	}
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", successCount)
	}
	if conflictCount != 9 {
		t.Fatalf("expected exactly 9 conflicts, got %d", conflictCount)
	}
}
