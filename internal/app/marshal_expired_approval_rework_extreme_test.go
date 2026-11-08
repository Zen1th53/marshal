//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestExpiredApprovalReworksRequeuesTaskWithoutPausingRun(t *testing.T) {
	ctx := t.Context()
	s, pending, mu := parallelWaitFixture(t, 2, "a")
	brief := func(marshal.Task, BriefContext) string { return "brief" }

	expiryTime := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Second)
	expiryStr := expiryTime.UTC().Format("2006-01-02T15:04:05Z07:00")

	var launches sync.Map
	var approvalState atomic.Value // "pending", "expired", "fresh_pending", "fresh_approved"
	approvalState.Store("pending")

	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		id := req.Task.PlanTaskID
		val, _ := launches.LoadOrStore(id, new(atomic.Int32))
		count := val.(*atomic.Int32).Add(1)

		if id == "a" {
			switch count {
			case 1:
				return nil, process05ApprovalRequired("lane-a-0", "a", "approval-a-0")
			case 2:
				// On redispatch after expiry, the task requests a fresh approval
				approvalState.Store("fresh_pending")
				return nil, process05ApprovalRequired("lane-a-1", "a", "approval-a-1")
			case 3:
				// After fresh approval is decided, task completes
				state := approvalState.Load().(string)
				if state != "fresh_approved" {
					t.Errorf("task a relaunched attempt 3 before fresh approval was decided: state=%s", state)
				}
				return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
			default:
				t.Fatalf("unexpected launch count %d for task a", count)
			}
		}
		// Sibling task b succeeds immediately on first launch
		return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
	}}

	s.process05WaitReceipt = func(_ context.Context, _ marshal.Run, task marshal.Task, runID, approvalID string) (bool, error) {
		if task.PlanTaskID != "a" {
			t.Errorf("unexpected wait receipt check for task %s", task.PlanTaskID)
		}
		state := approvalState.Load().(string)
		switch state {
		case "pending":
			if approvalID != "approval-a-0" {
				t.Errorf("expected approval-a-0, got %s", approvalID)
			}
			return true, nil
		case "expired":
			if approvalID != "approval-a-0" {
				t.Errorf("expected approval-a-0 for expired receipt check, got %s", approvalID)
			}
			return false, &process05ApprovalExpired{approvalID: "approval-a-0", expiresAt: expiryTime}
		case "fresh_pending":
			if approvalID != "approval-a-1" {
				t.Errorf("expected fresh approval-a-1, got %s", approvalID)
			}
			return true, nil
		case "fresh_approved":
			if approvalID != "approval-a-1" {
				t.Errorf("expected fresh approval-a-1, got %s", approvalID)
			}
			return false, nil
		default:
			t.Fatalf("unknown state %s", state)
			return false, nil
		}
	}

	// 1. Initial execution: task a pauses waiting for approval-a-0; sibling b finishes and is accepted
	run, err := s.Execute(ctx, "par", brief, nil)
	if err != nil {
		t.Fatalf("execute round 1: %v", err)
	}
	if run.State != marshal.AwaitingUser || run.Pause == nil || !strings.Contains(run.Pause.Reason, "approval-a-0") {
		t.Fatalf("run did not pause on task a pending approval: %+v", run)
	}
	taskA := run.Tasks[0]
	taskB := run.Tasks[1]
	if taskA.State != marshal.Queued || taskA.Process05WaitApprovalID != "approval-a-0" || taskA.Process05WaitRunID != "lane-a-0" {
		t.Fatalf("task a not waiting on approval-a-0: %+v", taskA)
	}
	if taskB.State != marshal.Accepted {
		t.Fatalf("sibling task b was not accepted: state=%s", taskB.State)
	}

	// 2. Mark approval-a-0 as expired unanswered and switch next wait to fresh_pending
	approvalState.Store("expired")
	mu.Lock()
	pending["a"] = false
	mu.Unlock()

	// Resume the run: checkPrivateProcess05Approval sees expired parallel task is not blocking resume
	run, err = s.Resume(ctx, "par")
	if err != nil {
		t.Fatalf("resume after expiry failed: %v", err)
	}
	if run.State != marshal.Dispatching {
		t.Fatalf("run state after resume = %s, want %s", run.State, marshal.Dispatching)
	}

	// Execute round 2: task a's approval expired, it reworks and requests fresh approval-a-1
	run, err = s.Execute(ctx, "par", brief, nil)
	if err != nil {
		t.Fatalf("execute round 2 after expiry: %v", err)
	}

	// Verify task a rework state:
	// - attempt 1 review recorded in store naming expired approval id and expiry time
	review, err := s.Store.GetMarshalReview(ctx, "par", "a", 1)
	if err != nil {
		t.Fatalf("missing attempt 1 review: %v", err)
	}
	if review.Value.Verdict != marshal.VerdictReturn {
		t.Fatalf("attempt 1 review verdict = %s, want return", review.Value.Verdict)
	}
	expectedReason := fmt.Sprintf("approval approval-a-0 expired at %s", expiryStr)
	if len(review.Value.Reasons) == 0 || review.Value.Reasons[0] != expectedReason {
		t.Fatalf("attempt 1 review reason = %v, want %s", review.Value.Reasons, expectedReason)
	}

	// - event recorded for return
	eventsList, err := s.Store.MarshalDecisions(ctx, "par")
	if err != nil {
		t.Fatalf("events error: %v", err)
	}
	var returnEventFound bool
	for _, ev := range eventsList {
		if ev.Type == events.EventTypeMarshalTaskReturned && ev.TaskID == "a" {
			returnEventFound = true
			if ev.Data["expired_approval"] != "approval-a-0" {
				t.Fatalf("event expired_approval = %v, want approval-a-0", ev.Data["expired_approval"])
			}
			if ev.Data["reason"] != expectedReason {
				t.Fatalf("event reason = %v, want %s", ev.Data["reason"], expectedReason)
			}
		}
	}
	if !returnEventFound {
		t.Fatal("no EventTypeMarshalTaskReturned event recorded for task a expiry rework")
	}

	// - verify task a was re-queued and redispatched for fresh approval-a-1
	taskAAfter := run.Tasks[0]
	if taskAAfter.Process05WaitApprovalID != "approval-a-1" || taskAAfter.Process05WaitRunID != "lane-a-1" {
		t.Fatalf("task a did not request fresh approval on next dispatch: %+v", taskAAfter)
	}
	// - verify expiry did not count toward the worker rework limit
	if taskAAfter.ReturnsByAgent != nil && taskAAfter.ReturnsByAgent["test"] != 0 {
		t.Fatalf("expiry counted toward worker rework limit: returns=%v", taskAAfter.ReturnsByAgent)
	}
	// - verify LaneReworkBase was incremented so attempt/lane is fresh
	if taskAAfter.LaneReworkBase != 1 {
		t.Fatalf("task a LaneReworkBase = %d, want 1", taskAAfter.LaneReworkBase)
	}

	// - verify the run was not paused for the expired task itself (now waiting on the fresh approval)
	if run.Pause == nil || !strings.Contains(run.Pause.Reason, "approval-a-1") {
		t.Fatalf("run pause reason = %v, want fresh approval-a-1", run.Pause)
	}

	// 3. Decide the fresh approval and complete the task
	approvalState.Store("fresh_approved")
	if _, err := s.Resume(ctx, "par"); err != nil {
		t.Fatalf("resume fresh approval: %v", err)
	}
	final, err := s.Execute(ctx, "par", brief, nil)
	if err != nil {
		t.Fatalf("execute round 3: %v", err)
	}

	for _, task := range final.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s state = %s, want Merged", task.PlanTaskID, task.State)
		}
	}
}

func TestExpiredApprovalReworksDeniedApprovalStillRefuses(t *testing.T) {
	ctx := t.Context()
	s, _, _ := parallelWaitFixture(t, 2, "a")
	brief := func(marshal.Task, BriefContext) string { return "brief" }

	var launches atomic.Int32
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		id := req.Task.PlanTaskID
		if id == "a" {
			launches.Add(1)
			return nil, process05ApprovalRequired("lane-a-0", "a", "approval-a-denied")
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, id+".txt"), []byte("done\n"), 0600)
	}}

	s.process05WaitReceipt = func(_ context.Context, _ marshal.Run, task marshal.Task, runID, approvalID string) (bool, error) {
		if approvalID == "approval-a-denied" {
			return false, fmt.Errorf("process 05: exact continuation approval denied, expired or invalidated")
		}
		return false, nil
	}

	// Initial execution launches task a, which gets parked waiting on approval-a-denied
	// Then pausePrivateProcess05Waits checks receipt, sees denial error, and fails!
	_, err := s.Execute(ctx, "par", brief, nil)
	if err == nil {
		t.Fatal("expected execution error on denied approval, got nil")
	}
	if !strings.Contains(err.Error(), "denied, expired or invalidated") {
		t.Fatalf("expected denial error, got: %v", err)
	}

	// Verify task a was NOT reworked (no review in store for return)
	if _, err := s.Store.GetMarshalReview(ctx, "par", "a", 1); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for review, got: %v", err)
	}
	run, _, err := s.load(ctx, "par")
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].LaneReworkBase != 0 {
		t.Fatalf("LaneReworkBase = %d, want 0", run.Tasks[0].LaneReworkBase)
	}
}

func TestExpiredApprovalReworksEngineProcess05RequestedExpiry(t *testing.T) {
	ctx := context.Background()
	fixture, _ := marshalUserCloseFixture(t, 1)
	runtime := requiredProcess05TestRuntime(t, fixture)
	ctx = requiredTestOwnerContext(t, runtime, ctx)
	worker := requiredProcess05TestHarnessName("test-harness")
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-ultra-expiry", "SESSION-ultra-expiry", requiredProcess05TestProjectID(runtime, projectRecord.ID)
	if err = runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID))
	p, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	p.Mode = plan.ModeUltra
	p.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
	if err = runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Plans().Approve(ctx, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID))); err != nil {
		t.Fatal(err)
	}
	service := runtime.Execution()
	var calls atomic.Int32
	requiredRegisterProcess05TestHarness(t, runtime, worker, func(_ context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		calls.Add(1)
		trap, err := runtime.armHoneypot(worktree)
		if err != nil {
			return execution.TaskResult{}, err
		}
		t.Cleanup(func() { _ = trap.Close() })
		if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixed\n"), 0600); err != nil {
			return execution.TaskResult{}, err
		}
		if _, err := gitMarshal(ctx, worktree, "add", "README.md"); err != nil {
			return execution.TaskResult{}, err
		}
		if _, err := gitMarshal(ctx, worktree, "commit", "-am", "fixed"); err != nil {
			return execution.TaskResult{}, err
		}
		return execution.TaskResult{TaskID: task.TaskID, Success: true}, nil
	})
	wiring := MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "fixture-operator", nil }}
	s, err := runtime.MarshalWired(wiring)
	if err != nil {
		t.Fatal(err)
	}
	s.Gate = marshalTestGate(true)
	s.GovernedCheck = fixtureCheckRunner
	s.Model = marshalFakeModel{review: marshal.Review{Reviewer: "marshal-model", Verdict: marshal.VerdictAccept}}
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{"check:grep -q fixed README.md"}}, "review-test", nil
	}
	runFunc := runtime.marshalProcess05Run(s)
	s.GovernedDrivers[worker] = driver.Governed{Provider: worker, Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		records, err := runFunc(ctx, req)
		t.Logf("marshalProcess05Run returned: req.Task.ReworkBase=%d err=%v", req.Task.LaneReworkBase, err)
		return records, err
	}}
	s.InstalledVersion = func(context.Context, string) string { return "1.0" }
	if err := fixture.Store.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: worker, InstalledVersion: "1.0", ProbeEvidenceID: "ultra-expiry-probe", ProbedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BindApprovedPlan(ctx, "ultra-expiry-run"); err != nil {
		t.Fatal(err)
	}
	brief := func(marshal.Task, BriefContext) string { return "fix the typo" }

	// 1. First execution: task launches and pauses on initial approval
	waiting, err := s.Execute(ctx, "ultra-expiry-run", brief, nil)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.State != marshal.AwaitingUser || calls.Load() != 0 {
		t.Fatalf("provider reached before approval: %+v", waiting)
	}
	task := waiting.Tasks[0]
	firstApprovalID := task.Process05WaitApprovalID
	if firstApprovalID == "" {
		t.Fatal("missing initial approval ID")
	}

	appReceipt, err := service.Engine().ApprovalManager().GetApproval(firstApprovalID)
	if err != nil || appReceipt.ExpiresAt == nil {
		t.Fatalf("approval receipt: %+v %v", appReceipt, err)
	}

	// 2. Advance s.clock() past ExpiresAt for the first approval so wired process05WaitReceipt classifies it as expired
	s.now = func() time.Time {
		if currentRun, _, err := s.load(ctx, "ultra-expiry-run"); err == nil && len(currentRun.Tasks) > 0 && currentRun.Tasks[0].LaneReworkBase >= 1 {
			return time.Now().UTC()
		}
		return appReceipt.ExpiresAt.Add(time.Second)
	}

	// Resume the run: checkPrivateProcess05Approval sees expired parallel task is not blocking resume
	resumed, err := s.Resume(ctx, "ultra-expiry-run")
	if err != nil {
		t.Fatalf("resume after expiry: %v", err)
	}
	if resumed.State != marshal.Dispatching {
		t.Fatalf("resumed state = %s, want %s", resumed.State, marshal.Dispatching)
	}

	// 3. Execute: dispatchReady sees expired approval, reworks task fix, clears wait, records review and event,
	// and dispatches task to lane 1 where it requests a fresh approval!
	reworked, err := s.Execute(ctx, "ultra-expiry-run", brief, nil)
	t.Logf("reworked: state=%s pause=%+v task=%+v err=%v calls=%d", reworked.State, reworked.Pause, reworked.Tasks[0], err, calls.Load())
	if err != nil {
		t.Fatalf("execute after expiry: %v", err)
	}

	// Task fix is waiting on fresh approval
	freshTask := reworked.Tasks[0]
	secondApprovalID := freshTask.Process05WaitApprovalID
	if secondApprovalID == "" || secondApprovalID == firstApprovalID {
		t.Fatalf("task fix did not request fresh approval: first=%s, got=%s", firstApprovalID, secondApprovalID)
	}
	if freshTask.LaneReworkBase != 1 {
		t.Fatalf("LaneReworkBase = %d, want 1", freshTask.LaneReworkBase)
	}
	if freshTask.ReturnsByAgent != nil && freshTask.ReturnsByAgent[worker] != 0 {
		t.Fatalf("worker returns penalized: %v", freshTask.ReturnsByAgent)
	}

	// Verify attempt 1 review recorded with expired approval reason
	review, err := s.Store.GetMarshalReview(ctx, "ultra-expiry-run", "fix", 1)
	if err != nil {
		t.Fatalf("missing attempt 1 review: %v", err)
	}
	if !strings.Contains(review.Value.Reasons[0], firstApprovalID) {
		t.Fatalf("review reason %v does not name %s", review.Value.Reasons, firstApprovalID)
	}

	// 4. Reset clock for fresh approval, approve freshApprovalID, resume and execute to completion!
	s.now = func() time.Time { return time.Now().UTC() }
	if allRuns, err := service.Engine().RunStore().ListRuns(ctx); err == nil {
		for _, r := range allRuns {
			for tid, tsk := range r.Tasks {
				t.Logf("BEFORE STEP 4: run=%s state=%s task=%s tstate=%s reason=%s", r.RunID, r.State, tid, tsk.State, tsk.LastFailureReason)
			}
		}
	}
	if err = service.Approve(ctx, secondApprovalID, "operator-fixture", "approve fresh rework lane"); err != nil {
		t.Fatal(err)
	}
	if allRuns, err := service.Engine().RunStore().ListRuns(ctx); err == nil {
		for _, r := range allRuns {
			for tid, tsk := range r.Tasks {
				t.Logf("AFTER APPROVE: run=%s state=%s task=%s tstate=%s reason=%s", r.RunID, r.State, tid, tsk.State, tsk.LastFailureReason)
			}
		}
	}
	if _, err = s.Resume(ctx, "ultra-expiry-run"); err != nil {
		t.Fatal(err)
	}
	final, err := s.Execute(ctx, "ultra-expiry-run", brief, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("harness calls = %d, want 1", calls.Load())
	}
	if final.Tasks[0].State != marshal.Merged {
		runs, listErr := service.Engine().RunStore().ListRuns(ctx)
		t.Fatalf("task fix state = %s, want Merged; harness calls=%d; execution runs=%+v (list error: %v)", final.Tasks[0].State, calls.Load(), runs, listErr)
	}
}
