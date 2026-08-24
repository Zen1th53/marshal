package app

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestTaskMemoryLiveCursorConcurrentBidirectionalCASAndRevocation(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	const projectID, taskID = "PROJECT-local", "TASK-LIVE-CURSOR"
	agentA, agentB := testPrincipal("agent-live-a"), testPrincipal("agent-live-b")
	grantTaskMemoryAccess(t, rt, taskID, agentA, agentB)

	start := make(chan struct{})
	type writeResult struct {
		agent string
		err   error
	}
	results := make(chan writeResult, 2)
	var wg sync.WaitGroup
	for _, write := range []struct {
		principal authz.Principal
		slot      working.SlotType
		value     string
	}{
		{agentA, working.SlotFinding, "config is in config/foo.yaml"},
		{agentB, working.SlotToolResults, "go test ./internal/app passes"},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.SetTaskSlot(ctx, write.principal, projectID, taskID, write.slot, write.value, false)
			results <- writeResult{agent: write.principal.ID, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent write by %s: %v", result.agent, result.err)
		}
	}

	// Both still-running agents refresh at an explicit turn/tool boundary and
	// observe the same canonical shared mutations.
	pageA, err := svc.RefreshTaskMemory(ctx, agentA, projectID, taskID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	pageB, err := svc.RefreshTaskMemory(ctx, agentB, projectID, taskID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	for name, page := range map[string]TaskMemoryChanges{"A": pageA, "B": pageB} {
		if len(page.Changes) != 2 || page.NextCursor != 2 || page.HasMore {
			t.Fatalf("agent %s page = %+v", name, page)
		}
		values := []string{page.Changes[0].Slot.Value, page.Changes[1].Slot.Value}
		sort.Strings(values)
		if values[0] != "config is in config/foo.yaml" || values[1] != "go test ./internal/app passes" {
			t.Fatalf("agent %s did not observe bidirectional writes: %v", name, values)
		}
	}

	// Private writes are not events and do not create task-revision inference.
	if err := svc.SetPrivateTaskSlot(ctx, agentA, projectID, taskID, "secret-plan", "agent A scratch"); err != nil {
		t.Fatal(err)
	}
	afterPrivate, err := svc.RefreshTaskMemory(ctx, agentB, projectID, taskID, pageB.NextCursor, 20)
	if err != nil || len(afterPrivate.Changes) != 0 || afterPrivate.NextCursor != pageB.NextCursor {
		t.Fatalf("private write affected shared cursor: %+v err=%v", afterPrivate, err)
	}

	plan, err := svc.SetTaskSlot(ctx, agentA, projectID, taskID, working.SlotPlanState, "revision one", false)
	if err != nil {
		t.Fatal(err)
	}
	casStart := make(chan struct{})
	casErrors := make(chan error, 2)
	for _, principal := range []authz.Principal{agentA, agentB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-casStart
			_, err := svc.UpdateTaskSlotCAS(ctx, principal, projectID, taskID, working.SlotPlanState, plan.Revision, principal.ID+" proposal")
			casErrors <- err
		}()
	}
	close(casStart)
	wg.Wait()
	close(casErrors)
	successes, conflicts := 0, 0
	for err := range casErrors {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, working.ErrCASConflict):
			conflicts++
		default:
			t.Fatalf("unexpected CAS error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS winners=%d conflicts=%d", successes, conflicts)
	}

	// A bounded page provides backpressure without a queue.
	one, err := svc.RefreshTaskMemory(ctx, agentA, projectID, taskID, 2, 1)
	if err != nil || len(one.Changes) != 1 || !one.HasMore || one.NextCursor <= 2 {
		t.Fatalf("bounded cursor page: %+v err=%v", one, err)
	}

	// Revocation is checked before cursor metadata is queried. The remaining
	// authorized agent sees only the generic revocation notification.
	if err := rt.Store().RevokeRoleBinding(ctx, "memory-"+taskID+"-"+agentB.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RefreshTaskMemory(ctx, agentB, projectID, taskID, pageB.NextCursor, 20); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("revoked agent refreshed events: %v", err)
	}
	remaining, err := svc.RefreshTaskMemory(ctx, agentA, projectID, taskID, one.NextCursor, 20)
	if err != nil {
		t.Fatal(err)
	}
	foundRevoke := false
	for _, change := range remaining.Changes {
		if change.Type == TaskEventGrantRevoked {
			foundRevoke = true
			if change.MemoryID != "" || change.Slot != nil || change.Priority != TaskEventCritical {
				t.Fatalf("revocation leaked metadata: %+v", change)
			}
		}
	}
	if !foundRevoke {
		t.Fatalf("authorized peer did not receive revocation notification: %+v", remaining)
	}
}

func TestTaskMemoryCursorCanonicalTombstoneAndRestart(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	principal := testPrincipal("agent-live-restart")
	const projectID, taskID = "PROJECT-local", "TASK-LIVE-RESTART"

	var beforeRestart int64
	func() {
		runtime, err := Open(ctx, repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		grantTaskMemoryAccess(t, runtime, taskID, principal)
		if _, err := runtime.Memory().SetTaskSlot(ctx, principal, projectID, taskID, working.SlotFinding, "temporary canonical fact", false); err != nil {
			t.Fatal(err)
		}
		created, err := runtime.Memory().RefreshTaskMemory(ctx, principal, projectID, taskID, 0, 20)
		if err != nil || len(created.Changes) != 1 || created.Changes[0].Slot == nil {
			t.Fatalf("created event: %+v err=%v", created, err)
		}
		beforeRestart = created.NextCursor
		record, err := runtime.Store().GetMemoryV2(ctx, projectID, taskSlotID(projectID, taskID, string(working.SlotFinding)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.Store().TombstoneMemory(ctx, projectID, record.ID, record.Revision, "test revocation"); err != nil {
			t.Fatal(err)
		}
	}()

	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	page, err := runtime.Memory().RefreshTaskMemory(ctx, principal, projectID, taskID, beforeRestart, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Changes) != 1 || page.Changes[0].Type != TaskEventSlotTombstoned || page.Changes[0].Slot != nil || page.Changes[0].CanonicalState != model.MemoryTombstoned {
		t.Fatalf("tombstoned canonical reload after restart: %+v", page)
	}
	if page.NextCursor <= beforeRestart {
		t.Fatalf("cursor did not survive restart: before=%d after=%d", beforeRestart, page.NextCursor)
	}
}
