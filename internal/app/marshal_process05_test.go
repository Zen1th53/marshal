package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	"github.com/Zen1th53/marshal/internal/statepath"
)

func TestMarshalGovernedTaskUsesProcess05AndImportsExactCommit(t *testing.T) {
	testMarshalProcess05AndImportsExactCommit(t, "codex")
}

func TestBindApprovedPlanRefusesHistoricalRouteProducerDrift(t *testing.T) {
	fixture, _ := marshalUserCloseFixture(t, 1)
	runtime := requiredProcess05TestRuntime(t, fixture)
	ctx := requiredTestOwnerContext(t, runtime, t.Context())
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-route-drift", "SESSION-route-drift", requiredProcess05TestProjectID(runtime, projectRecord.ID)
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(goal.ProjectID)
	created, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	created.Checks = map[string][]string{"fix": {"true"}}
	if err := runtime.Store().SavePlan(ctx, created, created.Version); err != nil {
		t.Fatal(err)
	}
	approved, err := runtime.Plans().Approve(ctx, request.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	changed := approved.Routes["fix"]
	changed.Provider = "other-provider"
	approved.Routes["fix"] = changed
	if err := runtime.Store().SavePlan(ctx, approved, approved.Version); err != nil {
		t.Fatal(err)
	}
	s, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindApprovedPlan(ctx, "route-drift"); err == nil || !strings.Contains(err.Error(), "route and governed producer differ") {
		t.Fatalf("mismatched approved plan bind = %v", err)
	}
	if _, err := s.Snapshot(ctx, "route-drift"); err == nil {
		t.Fatal("refused bind persisted a Marshal run")
	}
}

func TestMarshalSameProviderUsesProcess05AndImportsExactCommit(t *testing.T) {
	testMarshalProcess05AndImportsExactCommit(t, "test-harness")
}

func TestMarshalProcess05AdmitsStoredRevisedTaskWorktree(t *testing.T) {
	testMarshalProcess05AndImportsExactCommit(t, "codex", true)
}

func testMarshalProcess05AndImportsExactCommit(t *testing.T, modelProvider string, revised ...bool) {
	t.Helper()
	ctx := context.Background()
	fixture, _ := marshalUserCloseFixture(t, 1)
	runtime := requiredProcess05TestRuntime(t, fixture)
	ctx = requiredTestOwnerContext(t, runtime, ctx)
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID)).Valid() {
		t.Fatalf("fixture project ID %q is invalid", requiredProcess05TestProjectID(runtime, projectRecord.ID))
	}
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-marshal", "SESSION-marshal", requiredProcess05TestProjectID(runtime, projectRecord.ID)
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	worker := requiredProcess05TestHarnessName("test-harness")
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID))
	p, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
	if err := runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	p, err = runtime.Plans().Approve(ctx, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID)))
	if err != nil || p.State != plan.StateApproved {
		t.Fatalf("approve canonical plan: %+v %v", p, err)
	}
	execService := runtime.Execution()
	var executions atomic.Int32
	requiredRegisterProcess05TestHarness(t, runtime, worker, func(_ context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		executions.Add(1)
		trap, err := runtime.armHoneypot(worktree)
		if err != nil {
			return execution.TaskResult{}, err
		}
		t.Cleanup(func() { _ = trap.Close() })
		if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixed\n"), 0600); err != nil {
			return execution.TaskResult{}, err
		}
		return execution.TaskResult{TaskID: task.TaskID, Success: true, EvidenceList: process05ObservedTestEvidence(task)}, nil
	})
	s, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	s.GovernedCheck = fixtureCheckRunner
	s.Model = marshalFakeModel{review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	s.InstalledVersion = func(context.Context, string) string { return "1.0" }
	if err := s.Store.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: worker, InstalledVersion: "1.0", ProbeEvidenceID: "process05-probe", ProbedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers[worker] = driver.Governed{Provider: worker, Run: runtime.marshalProcess05Run(s)}
	s.ModelProvider = requiredProcess05TestModelProvider(modelProvider)
	if same := s.ModelProvider == worker; same != (modelProvider == "test-harness") {
		t.Fatalf("fixture reviewer/provider branch lost: same=%v", same)
	}
	run, err := s.BindApprovedPlan(ctx, "run")
	if err != nil || run.Tasks[0].Mode != marshal.Governed {
		t.Fatal(err)
	}
	if run.Repository == "" || run.TargetRef != "refs/heads/main" || run.BaseCommit == "" {
		t.Fatalf("Process 05 destination not bound: %+v", run)
	}
	var oldMarker string
	if len(revised) > 0 && revised[0] {
		// Start at the persisted amendment boundary. The production dispatcher
		// and Process05 adapter must agree about the revised task identity.
		oldDir := filepath.Join(s.Worktrees, taskArtifactID("run", run.Tasks[0]))
		if err := os.MkdirAll(oldDir, 0700); err != nil {
			t.Fatal(err)
		}
		oldMarker = filepath.Join(oldDir, "retained-attempt")
		if err := os.WriteFile(oldMarker, []byte("old evidence"), 0600); err != nil {
			t.Fatal(err)
		}
		stored, rev, err := s.load(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		stored.ArtifactRevision = 2
		stored.Tasks[0].Branch = statepath.RefName + "/run/r2/fix"
		p, err := s.Store.GetPlan(ctx, stored.PlanID, stored.PlanVersion)
		if err != nil {
			t.Fatal(err)
		}
		stored.ApprovalScopeDigest = marshalApprovalDigest(p.ApprovalScopeDigest, stored)
		if err := s.save(ctx, "run", stored, rev); err != nil {
			t.Fatal(err)
		}
		run = stored
	}
	changed := run
	changed.TargetRef = "refs/heads/other"
	approvedPlan, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	if marshalApprovalDigest(approvedPlan.ApprovalScopeDigest, changed) == run.ApprovalScopeDigest {
		t.Fatal("Process 05 digest omits destination")
	}
	tampered := run.Tasks[0]
	tampered.Checks[0].Command = "true"
	if _, err := runtime.marshalProcess05Run(s)(ctx, driver.Request{RunID: "run", Task: tampered}); !errors.Is(err, ErrProcess05Admission) || !strings.Contains(err.Error(), "checks differ") {
		t.Fatalf("changed check reached Process 05: %v", err)
	}
	waiting, pauseErr := s.Execute(ctx, "run", func(marshal.Task, BriefContext) string { return "fix the typo" }, nil)
	paused, err := execService.ListRuns(ctx)
	if err != nil || len(paused) != 1 || paused[0].State != execution.RunNeedsApproval {
		t.Fatalf("Process 05 did not pause for its own approval: %+v %v", paused, err)
	}
	assertMarshalProcess05Pause(t, s, "run", "fix", waiting, pauseErr, paused[0])
	if executions.Load() != 0 {
		t.Fatal("provider executed before its exact approval")
	}
	approvalID := paused[0].Tasks["fix"].ApprovalID
	if approvalID == "" {
		t.Fatal("Process 05 approval has no ID")
	}
	if err := execService.Approve(ctx, approvalID, "operator", "approve governed task"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	finished, err := s.Execute(ctx, "run", func(marshal.Task, BriefContext) string { return "fix the typo" }, nil)
	if err != nil {
		t.Fatalf("resume governed task: %v (Marshal state %s)", err, finished.State)
	}
	if finished.State != marshal.Verifying || finished.Tasks[0].State != marshal.Merged {
		handin, _ := s.Store.GetMarshalHandIn(ctx, "run", "fix", 1)
		t.Fatalf("Marshal run did not merge Process 05 result: %+v handin=%+v", finished, handin.Value.RuntimeObserved)
	}
	if executions.Load() != 1 {
		t.Fatalf("provider executed %d times, want one", executions.Load())
	}
	runs, err := execService.ListRuns(ctx)
	if err != nil || len(runs) != 1 || runs[0].State != execution.RunDonePendingVerification || runs[0].Tasks["fix"].ResultCommit != finished.Tasks[0].ResultCommit {
		t.Fatalf("Process 05 result does not match Marshal hand-in: %+v %v", runs, err)
	}
	if oldMarker != "" {
		if raw, err := os.ReadFile(oldMarker); err != nil || string(raw) != "old evidence" {
			t.Fatalf("revised dispatch damaged old artifacts: %q %v", raw, err)
		}
		if _, err := os.Stat(filepath.Join(s.Worktrees, taskArtifactID("run", finished.Tasks[0]), "README.md")); err != nil {
			t.Fatalf("revised worktree was not used: %v", err)
		}
	}
	assertProcess05ObservedTestHandIn(t, s, finished)
}

func TestMarshalGovernedMultiTaskPlanRunsInOrder(t *testing.T) {
	ctx := context.Background()
	fixture, _ := marshalUserCloseFixture(t, 2)
	runtime := requiredProcess05TestRuntime(t, fixture)
	ctx = requiredTestOwnerContext(t, runtime, ctx)
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-multi", "SESSION-multi", requiredProcess05TestProjectID(runtime, projectRecord.ID)
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	worker := requiredProcess05TestHarnessName("test-harness")
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID))
	request.Tasks = append(request.Tasks, plan.Task{ID: "follow", Title: "write follow-up", Mutating: true, Weight: 1, Paths: []string{"FOLLOW.txt"}, Criteria: []string{"follow-up exists"}, DependsOn: []string{"fix"}})
	p, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}, "follow": {"test -f FOLLOW.txt"}}
	if err := runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Plans().Approve(ctx, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID))); err != nil {
		t.Fatal(err)
	}
	execService := runtime.Execution()
	var executions atomic.Int32
	requiredRegisterProcess05TestHarness(t, runtime, worker, func(_ context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		executions.Add(1)
		trap, err := runtime.armHoneypot(worktree)
		if err != nil {
			return execution.TaskResult{}, err
		}
		t.Cleanup(func() { _ = trap.Close() })
		switch task.TaskID {
		case "fix":
			if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixed\n"), 0600); err != nil {
				return execution.TaskResult{}, err
			}
		case "follow":
			data, err := os.ReadFile(filepath.Join(worktree, "README.md"))
			if err != nil || string(data) != "fixed\n" {
				return execution.TaskResult{}, errors.New("dependent task did not receive the merged base")
			}
			if err := os.WriteFile(filepath.Join(worktree, "FOLLOW.txt"), []byte("done\n"), 0600); err != nil {
				return execution.TaskResult{}, err
			}
		}
		return execution.TaskResult{TaskID: task.TaskID, Success: true}, nil
	})
	s, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	s.GovernedCheck = fixtureCheckRunner
	s.Model = marshalFakeModel{review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	s.InstalledVersion = func(context.Context, string) string { return "1.0" }
	if err := s.Store.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: worker, InstalledVersion: "1.0", ProbeEvidenceID: "multi-probe", ProbedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers[worker] = driver.Governed{Provider: worker, Run: runtime.marshalProcess05Run(s)}
	run, err := s.BindApprovedPlan(ctx, "run")
	if err != nil || len(run.Tasks) != 2 || run.Tasks[0].PlanTaskID != "fix" || run.Tasks[1].PlanTaskID != "follow" {
		t.Fatalf("multi-task plan binding: %+v %v", run.Tasks, err)
	}
	for i := 0; i < 2; i++ {
		waiting, pauseErr := s.Execute(ctx, "run", func(marshal.Task, BriefContext) string { return "execute approved task" }, nil)
		runs, err := execService.ListRuns(ctx)
		if err != nil || len(runs) != 1 {
			t.Fatalf("Process 05 run: %+v %v", runs, err)
		}
		id := run.Tasks[i].PlanTaskID
		assertMarshalProcess05Pause(t, s, "run", id, waiting, pauseErr, runs[0])
		if executions.Load() != int32(i) {
			t.Fatalf("approval pause duplicated provider execution: got %d, want %d", executions.Load(), i)
		}
		approvalID := runs[0].Tasks[id].ApprovalID
		if approvalID == "" {
			t.Fatalf("task %s has no approval", id)
		}
		if err := execService.Approve(ctx, approvalID, "operator", "approve governed task"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Resume(ctx, "run"); err != nil {
			t.Fatal(err)
		}
	}
	finished, err := s.Execute(ctx, "run", func(marshal.Task, BriefContext) string { return "execute approved task" }, nil)
	if executions.Load() != 2 {
		t.Fatalf("providers executed %d times, want once per task", executions.Load())
	}
	if err != nil || finished.State != marshal.Verifying || finished.Tasks[0].State != marshal.Merged || finished.Tasks[1].State != marshal.Merged {
		t.Fatalf("multi-task completion: %+v %v", finished, err)
	}
}

// Assert the real canonical Process 05 wait, retaining Community's historical
// approval-error contract and Extreme's durable resumable orchestration pause.
func assertMarshalProcess05Pause(t *testing.T, s *MarshalService, runID, taskID string, waiting marshal.Run, cause error, actual execution.ExecutionRun) {
	t.Helper()
	if actual.State != execution.RunNeedsApproval || actual.Tasks[taskID].State != execution.TaskNeedsApproval || actual.Tasks[taskID].ApprovalID == "" {
		t.Fatalf("task has no canonical Process 05 approval: %+v", actual)
	}
	if !statepath.PublicEngine {
		if cause == nil || !strings.Contains(cause.Error(), "approval") {
			t.Fatalf("Community approval pause error missing: %v", cause)
		}
		return
	}
	task := waiting.Tasks[taskIndex(waiting, taskID)]
	approvalID := actual.Tasks[taskID].ApprovalID
	if cause != nil || waiting.State != marshal.AwaitingUser || waiting.Pause == nil || waiting.Pause.ResumeState != marshal.Dispatching || waiting.Pause.Reason != "A task is waiting for the Marshal's go-ahead." || waiting.Pause.Detail != fmt.Sprintf("process 05: approval %s required; use /marshal approve-task %s, then /marshal resume", approvalID, approvalID) || task.State != marshal.Queued || task.Process05WaitRunID != actual.RunID || task.Process05WaitApprovalID != approvalID || len(task.ReturnsByAgent) != 0 {
		t.Fatalf("private pause is not bound to exact canonical approval: %+v %v", waiting, cause)
	}
	canonical, _, err := s.load(t.Context(), runID)
	if err != nil || !reflect.DeepEqual(canonical, waiting) {
		t.Fatalf("private pause not durable: %+v %v", canonical, err)
	}
	pending, err := s.Resume(t.Context(), runID)
	if err != nil || pending.State != marshal.AwaitingUser || !reflect.DeepEqual(pending.Pause, waiting.Pause) || pending.Tasks[taskIndex(pending, taskID)].Process05WaitApprovalID != approvalID {
		t.Fatalf("pending approval restarted or lost its binding: %+v %v", pending, err)
	}
	repeated, err := s.Execute(t.Context(), runID, marshalBrief, nil)
	if err != nil || repeated.State != marshal.AwaitingUser || repeated.Tasks[taskIndex(repeated, taskID)].Process05WaitRunID != actual.RunID || repeated.Tasks[taskIndex(repeated, taskID)].Process05WaitApprovalID != approvalID {
		t.Fatalf("pending retry changed its approval binding: %+v %v", repeated, err)
	}
}
