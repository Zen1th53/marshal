package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestM14_ConcurrentTaskSlotWritesAndCAS(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	const taskID = "TASK-BLACKBOARD-10"
	p1 := testPrincipal("agent-alpha")
	p2 := testPrincipal("agent-beta")
	grantTaskMemoryAccess(t, rt, taskID, p1, p2)

	// 1. Initial slot creation
	slot1, err := svc.SetTaskSlot(ctx, p1, projectID, taskID, working.SlotPlanState, "initial hypothesis: DB deadlocks", true)
	if err != nil {
		t.Fatalf("set task slot: %v", err)
	}
	if slot1.Revision != 1 {
		t.Fatalf("expected initial revision 1, got %d", slot1.Revision)
	}

	// 2. Parallel distinct slot writes
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := svc.SetTaskSlot(ctx, p1, projectID, taskID, working.SlotActiveSymbols, "internal/store/memory.go:ListMemoryV2", false)
		if err != nil {
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		_, err := svc.SetTaskSlot(ctx, p2, projectID, taskID, working.SlotBlockers, "waiting on authz lock", false)
		if err != nil {
			errs <- err
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent slot write error: %v", err)
	}

	// 3. Verify all slots present
	slots, err := svc.ListTaskSlots(ctx, p1, projectID, taskID)
	if err != nil {
		t.Fatalf("list slots: %v", err)
	}
	if len(slots) != 3 {
		t.Fatalf("expected 3 slots, got %d", len(slots))
	}
	sharedSlots, err := svc.ListTaskSlots(ctx, p2, projectID, taskID)
	if err != nil || len(sharedSlots) != 3 {
		t.Fatalf("agent 2 did not receive shared task slots: slots=%+v err=%v", sharedSlots, err)
	}

	// 4. Test CAS Conflict on same slot
	// Agent 1 succeeds updating revision 1 -> 2
	slot2, err := svc.UpdateTaskSlotCAS(ctx, p1, projectID, taskID, working.SlotPlanState, 1, "revised plan: lock ordering fix")
	if err != nil {
		t.Fatalf("agent 1 CAS update: %v", err)
	}
	if slot2.Revision != 2 {
		t.Fatalf("expected revision 2, got %d", slot2.Revision)
	}

	// Agent 2 attempts CAS update still expecting revision 1 -> must return ErrCASConflict
	_, err = svc.UpdateTaskSlotCAS(ctx, p2, projectID, taskID, working.SlotPlanState, 1, "agent 2 stale update")
	if err == nil || !errors.Is(err, working.ErrCASConflict) {
		t.Fatalf("expected ErrCASConflict for stale CAS update, got %v", err)
	}
}

func TestM14_PrivateSlotIsolation(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	const taskID = "TASK-PRIVATE-20"
	p1 := testPrincipal("agent-alice")
	p2 := testPrincipal("agent-bob")
	grantTaskMemoryAccess(t, rt, taskID, p1, p2)

	// Alice sets private scratchpad slot
	if err := svc.SetPrivateTaskSlot(ctx, p1, projectID, taskID, "scratch_step", "analyzing memory.go"); err != nil {
		t.Fatalf("set private slot: %v", err)
	}

	// Alice reads her private slot
	val, ok, err := svc.GetPrivateTaskSlot(ctx, p1, projectID, taskID, "scratch_step")
	if err != nil || !ok || val != "analyzing memory.go" {
		t.Fatalf("alice failed to read private slot: val=%s ok=%v err=%v", val, ok, err)
	}

	// Bob cannot read Alice's private slot
	bobVal, bobOk, bobErr := svc.GetPrivateTaskSlot(ctx, p2, projectID, taskID, "scratch_step")
	if bobErr != nil || bobOk || bobVal != "" {
		t.Fatalf("bob accessed alice private slot: val=%s ok=%v err=%v", bobVal, bobOk, bobErr)
	}
}

func TestM14_TaskSlotsRequireExplicitGrant(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	owner := testPrincipal("agent-task-owner")
	outsider := testPrincipal("agent-task-outsider")
	const taskID = "TASK-GRANT-BOUNDARY"
	grantTaskMemoryAccess(t, rt, taskID, owner)
	if _, err := svc.SetTaskSlot(ctx, owner, "PROJECT-local", taskID, working.SlotFinding, "authorized finding", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListTaskSlots(ctx, outsider, "PROJECT-local", taskID); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("outsider listed task slots: %v", err)
	}
	if _, err := svc.SetTaskSlot(ctx, outsider, "PROJECT-local", taskID, working.SlotOpenQuestion, "unauthorized proposal", false); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("outsider wrote task slot: %v", err)
	}
}

func TestM14_PromoteTaskSlotToCandidate(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	const taskID = "TASK-PROMOTE-30"
	p := testPrincipal("agent-charlie")
	grantTaskMemoryAccess(t, rt, taskID, p)

	// Set slot
	_, err := svc.SetTaskSlot(ctx, p, projectID, taskID, working.SlotHypothesis, "Root cause is race in cache invalidation", true)
	if err != nil {
		t.Fatalf("set slot: %v", err)
	}

	// Promote slot to candidate finding
	cand, err := svc.PromoteTaskSlot(ctx, p, projectID, taskID, working.SlotHypothesis, model.MemoryKindFinding, "Cache Invalidation Race")
	if err != nil {
		t.Fatalf("promote slot: %v", err)
	}

	if cand.Lifecycle != model.MemoryCandidate || cand.Kind != model.MemoryKindFinding || cand.Body != "Root cause is race in cache invalidation" {
		t.Fatalf("unexpected candidate promoted from slot: %+v", cand)
	}
}

func TestM14_TaskAndPrivateSlotsSurviveRestart(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	principal := testPrincipal("agent-restart")
	const projectID = "PROJECT-local"
	const taskID = "TASK-RESTART-SLOTS"
	func() {
		runtime, err := Open(ctx, repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		grantTaskMemoryAccess(t, runtime, taskID, principal)
		if _, err := runtime.Memory().SetTaskSlotWithProvenance(ctx, principal, projectID, taskID, working.SlotPlanState, "durable shared state", true, WorkingProvenance{Provider: "codex", SessionID: "SESSION-1", RunID: "RUN-1"}); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Memory().SetPrivateTaskSlot(ctx, principal, projectID, taskID, "scratch", "durable private state"); err != nil {
			t.Fatal(err)
		}
	}()
	func() {
		runtime, err := Open(ctx, repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		slots, err := runtime.Memory().ListTaskSlots(ctx, principal, projectID, taskID)
		if err != nil || len(slots) != 1 || slots[0].Value != "durable shared state" {
			t.Fatalf("shared slot after restart: slots=%+v err=%v", slots, err)
		}
		if slots[0].Provider != "codex" || slots[0].SessionID != "SESSION-1" || slots[0].RunID != "RUN-1" || slots[0].LastAgentID != principal.ID {
			t.Fatalf("working provenance after restart: %+v", slots[0])
		}
		value, ok, err := runtime.Memory().GetPrivateTaskSlot(ctx, principal, projectID, taskID, "scratch")
		if err != nil || !ok || value != "durable private state" {
			t.Fatalf("private slot after restart: value=%q ok=%v err=%v", value, ok, err)
		}
	}()
}
