package working_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/working"
)

func TestT137WorkingMemoryAndScratchpad(t *testing.T) {
	ctx := context.Background()
	wm := working.NewManager(working.Config{
		MaxEntriesPerScope: 3,
		MaxBytesPerScope:   1024,
	})

	// 1. Set and get slot with CAS revision
	slot, err := wm.SetSlot(ctx, "TASK-1", "AGENT-A", working.SlotHypothesis, "Hypothesis: WAL mode causes deadlock under concurrent checkpoints", false)
	if err != nil {
		t.Fatalf("SetSlot: %v", err)
	}
	if slot.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", slot.Revision)
	}

	// 2. CAS update success
	updated, err := wm.UpdateSlotCAS(ctx, "TASK-1", "AGENT-A", working.SlotHypothesis, 1, "Hypothesis confirmed by SQLite pragma check")
	if err != nil {
		t.Fatalf("UpdateSlotCAS: %v", err)
	}
	if updated.Revision != 2 {
		t.Fatalf("expected revision 2, got %d", updated.Revision)
	}

	// 3. CAS update failure on stale revision
	_, err = wm.UpdateSlotCAS(ctx, "TASK-1", "AGENT-A", working.SlotHypothesis, 1, "Stale update")
	if !errors.Is(err, working.ErrCASConflict) {
		t.Fatalf("expected ErrCASConflict on stale revision 1 against current 2, got: %v", err)
	}

	// 4. Agent-private vs shared-task isolation
	// AGENT-A private slot
	_, _ = wm.SetPrivateSlot(ctx, "TASK-1", "AGENT-A", "private_notes", "secret internal thinking")
	// AGENT-B cannot read AGENT-A private slot
	_, found := wm.GetPrivateSlot(ctx, "TASK-1", "AGENT-B", "private_notes")
	if found {
		t.Fatal("AGENT-B was able to read AGENT-A's private scratchpad slot")
	}

	// 5. Ceiling / eviction test (MaxEntriesPerScope = 3)
	_, _ = wm.SetSlot(ctx, "TASK-1", "AGENT-A", working.SlotPlanState, "Step 1: Check code", false)
	_, _ = wm.SetSlot(ctx, "TASK-1", "AGENT-A", working.SlotBlockers, "No blocker", false)
	// 4th slot causes oldest unpinned slot to be evicted
	_, _ = wm.SetSlot(ctx, "TASK-1", "AGENT-A", working.SlotActiveSymbols, "Symbol: OpenDB", false)

	entries := wm.ListSlots(ctx, "TASK-1")
	if len(entries) > 3 {
		t.Fatalf("expected max 3 slots after eviction, got %d", len(entries))
	}

	// 6. Concurrent CAS race test
	var wg sync.WaitGroup
	conflicts := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(val string) {
			defer wg.Done()
			_, err := wm.UpdateSlotCAS(ctx, "TASK-1", "AGENT-A", working.SlotBlockers, 1, val)
			if err != nil {
				mu.Lock()
				conflicts++
				mu.Unlock()
			}
		}("Blocker update")
	}
	wg.Wait()

	if conflicts == 0 {
		t.Fatal("expected concurrent CAS conflicts under race condition")
	}
}
