//go:build !governed

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// This is an actual canonical Engine/Marshal lifecycle regression with a mock
// provider, not evidence of native model decisions or paid-provider execution.
// A serial (Standard) Process05 return still requires a fresh plan; an ULTRA
// task lane return reworks inside the same run up to the rework limit.
func TestProcess05ReturnedTaskRequiresFreshPlanWithoutReplayingLane(t *testing.T) {
	for _, mode := range []plan.Mode{plan.ModeStandard, plan.ModeUltra} {
		t.Run(string(mode), func(t *testing.T) {
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
			goal.ID, goal.SessionID, goal.ProjectID = "GOAL-return", "SESSION-return", requiredProcess05TestProjectID(runtime, projectRecord.ID)
			if err = runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
				t.Fatal(err)
			}
			request := requiredProcess05PlanRequest(planCreateRequest())
			request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID))
			p, err := runtime.Plans().Create(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			p.Mode = mode
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
				// Like the observed real worker, finish a read-only turn without creating
				// the required output. Independent shell checking must reject this result.
				return execution.TaskResult{TaskID: task.TaskID, Success: true}, nil
			})
			s, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator-fixture", nil }})
			if err != nil {
				t.Fatal(err)
			}
			s.Gate = marshalTestGate(true)
			s.GovernedCheck = fixtureCheckRunner
			s.Model = marshalFakeModel{review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "fixture"}}
			s.InstalledVersion = func(context.Context, string) string { return "1.0" }
			if err = s.Store.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: worker, InstalledVersion: "1.0", ProbeEvidenceID: "fixture-probe", ProbedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			s.GovernedDrivers[worker] = driver.Governed{Provider: worker, Run: runtime.marshalProcess05Run(s)}
			bound, err := s.BindApprovedPlan(ctx, "return-run")
			if err != nil {
				t.Fatal(err)
			}
			if !bound.Process05Bound || bound.Process05Parallel != (mode == plan.ModeUltra) {
				t.Fatalf("wrong canonical binding: %+v", bound)
			}
			brief := func(marshal.Task, BriefContext) string { return "fix the typo" }
			waiting, err := s.Execute(ctx, "return-run", brief, nil)
			if err != nil {
				t.Fatal(err)
			}
			if waiting.State != marshal.AwaitingUser || calls.Load() != 0 {
				t.Fatalf("provider reached before approval: %+v", waiting)
			}
			runs, err := service.ListRuns(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var approvalID string
			for _, run := range runs {
				if task, ok := run.Tasks["fix"]; ok && task.State == execution.TaskNeedsApproval {
					approvalID = task.ApprovalID
				}
			}
			if approvalID == "" {
				t.Fatal("missing exact canonical task approval")
			}
			if err = service.Approve(ctx, approvalID, "operator-fixture", "approve exact fixture task"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Resume(ctx, "return-run"); err != nil {
				t.Fatal(err)
			}
			returned, err := s.Execute(ctx, "return-run", brief, nil)
			if err != nil {
				t.Fatalf("returned result must pause honestly, not fail redispatch: %v", err)
			}
			if mode == plan.ModeUltra {
				assertUltraReturnReworksInRun(ctx, t, s, service, returned, worker, brief, &calls)
				return
			}
			if returned.State != marshal.AwaitingUser || returned.Pause == nil || returned.Pause.ResumeState != "" || !strings.Contains(returned.Pause.Reason, "new approved Process 04 plan") || returned.Tasks[0].State != marshal.Returned || returned.Tasks[0].ReturnsByAgent[worker] != 1 || calls.Load() != 1 {
				t.Fatalf("returned=%+v calls=%d", returned, calls.Load())
			}
			review, err := s.Store.GetMarshalReview(ctx, "return-run", "fix", 1)
			if err != nil {
				t.Fatal(err)
			}
			if review.Value.Verdict != marshal.VerdictReturn || len(review.Value.Reasons) == 0 {
				t.Fatalf("lost failed review: %+v", review.Value)
			}
			handin, err := s.Store.GetMarshalHandIn(ctx, "return-run", "fix", 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(handin.Value.CheckResults) != 1 || handin.Value.CheckResults[0].Passed {
				t.Fatalf("failed shell check lost: %+v", handin.Value)
			}
			beforeRuns, err := service.ListRuns(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// ListRuns is a map-backed set; compare full canonical bodies in stable ID order.
			sort.Slice(beforeRuns, func(i, j int) bool { return beforeRuns[i].RunID < beforeRuns[j].RunID })
			before, _ := json.Marshal(beforeRuns)
			approvalBefore, err := service.Engine().ApprovalManager().GetApproval(approvalID)
			if err != nil {
				t.Fatal(err)
			}
			approvalBytes, _ := json.Marshal(approvalBefore)
			if _, err = s.Resume(ctx, "return-run"); err == nil || !strings.Contains(err.Error(), "new approved Process 04 plan") {
				t.Fatalf("spent domain resumed: %v", err)
			}
			if _, err = s.Dispatch(ctx, "return-run", "fix", "retry"); err == nil {
				t.Fatal("returned task dispatched without new plan")
			}
			if _, err = s.Execute(ctx, "return-run", brief, nil); err != nil {
				t.Fatal(err)
			}
			afterRuns, err := service.ListRuns(ctx)
			if err != nil {
				t.Fatal(err)
			}
			sort.Slice(afterRuns, func(i, j int) bool { return afterRuns[i].RunID < afterRuns[j].RunID })
			after, _ := json.Marshal(afterRuns)
			approvalAfter, err := service.Engine().ApprovalManager().GetApproval(approvalID)
			if err != nil {
				t.Fatal(err)
			}
			afterApproval, _ := json.Marshal(approvalAfter)
			if !bytes.Equal(before, after) || !bytes.Equal(approvalBytes, afterApproval) || calls.Load() != 1 {
				t.Fatal("refused retry altered canonical domain or replayed provider")
			}
			for _, run := range afterRuns {
				if run.State != execution.RunDonePendingVerification {
					t.Fatalf("completed canonical run changed: %+v", run)
				}
			}
		})
	}
}

// assertUltraReturnReworksInRun drives a returned ULTRA lane task through its
// in-run rework: a fresh lane from the same base, the same checks and review,
// and an escalation once the worker's rework limit is reached.
func assertUltraReturnReworksInRun(ctx context.Context, t *testing.T, s *MarshalService, service *ExecutionService, returned marshal.Run, worker string, brief MarshalBrief, calls *atomic.Int32) {
	t.Helper()
	task := returned.Tasks[0]
	if returned.Pause != nil && strings.Contains(returned.Pause.Reason, "new approved Process 04 plan") {
		t.Fatalf("ULTRA return still demands a fresh plan: %+v", returned.Pause)
	}
	if task.ReturnsByAgent[worker] != 1 || task.Process05WaitApprovalID == "" || calls.Load() != 1 {
		t.Fatalf("returned task was not redispatched to a new lane: %+v calls=%d", task, calls.Load())
	}
	first := taskLaneIDForTest(t, service, "fix", 0)
	if task.Process05WaitRunID == first || task.Process05WaitRunID != taskLaneIDForTest(t, service, "fix", 1) {
		t.Fatalf("rework reused the spent lane %s", task.Process05WaitRunID)
	}
	spent, err := service.GetRun(ctx, first)
	if err != nil || spent.State != execution.RunDonePendingVerification || spent.Tasks["fix"].ResultCommit == "" {
		t.Fatalf("first attempt evidence changed: %v %+v", err, spent.State)
	}
	if err = service.Approve(ctx, task.Process05WaitApprovalID, "operator-fixture", "approve exact rework lane"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resume(ctx, "return-run"); err != nil {
		t.Fatal(err)
	}
	limited, err := s.Execute(ctx, "return-run", brief, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("rework lane did not run the provider once: calls=%d", calls.Load())
	}
	if limited.State != marshal.AwaitingUser || limited.Pause == nil || !strings.Contains(limited.Pause.Reason, "rework limit") || limited.Tasks[0].ReturnsByAgent[worker] != 2 {
		t.Fatalf("rework limit did not escalate: %+v", limited)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		review, err := s.Store.GetMarshalReview(ctx, "return-run", "fix", attempt)
		if err != nil || review.Value.Verdict != marshal.VerdictReturn {
			t.Fatalf("attempt %d review lost: %v", attempt, err)
		}
		handin, err := s.Store.GetMarshalHandIn(ctx, "return-run", "fix", attempt)
		if err != nil || len(handin.Value.CheckResults) != 1 || handin.Value.CheckResults[0].Passed {
			t.Fatalf("attempt %d failed check lost: %v", attempt, err)
		}
	}
}

func taskLaneIDForTest(t *testing.T, service *ExecutionService, taskID string, rework int) string {
	t.Helper()
	runs, err := service.ListRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.ParentRunID == "" && len(run.TaskLanes) > 0 {
			return execution.TaskLaneID(run.RunID, taskID, rework)
		}
	}
	t.Fatal("no task lane parent")
	return ""
}
