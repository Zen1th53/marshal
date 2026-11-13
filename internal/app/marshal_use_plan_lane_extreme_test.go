//go:build !governed

package app

import (
	"context"
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

// A run that stopped at its rework limit, followed by /marshal use-plan on the
// same approved plan: the new run shares the plan's execution parent and its
// task lanes. Its first attempt must open the next lane after the earlier
// run's newest one, never be refused as "rework is out of order" and never
// reopen the earlier run's lane. The rework limit counts only its own returns.
func TestUsePlanAfterReworkLimitContinuesPriorTaskLanes(t *testing.T) {
	ctx := context.Background()
	fixture, _ := marshalUserCloseFixture(t, 1)
	runtime := requiredProcess05TestRuntime(t, fixture)
	ctx = requiredTestOwnerContext(t, runtime, ctx)
	worker := requiredProcess05TestHarnessName("test-harness")
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := requiredProcess05TestProjectID(runtime, projectRecord.ID)
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-use-plan", "SESSION-use-plan", projectID
	if err = runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(projectID)
	p, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	p.Mode = plan.ModeUltra
	p.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
	if err = runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Plans().Approve(ctx, projectid.ID(projectID)); err != nil {
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
	brief := func(marshal.Task, BriefContext) string { return "fix the typo" }
	approveWaiting := func(runID string) {
		t.Helper()
		run, err := s.Snapshot(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		id := run.Tasks[0].Process05WaitApprovalID
		if id == "" {
			runs, _ := service.ListRuns(ctx)
			for _, r := range runs {
				if task, ok := r.Tasks["fix"]; ok && task.State == execution.TaskNeedsApproval {
					id = task.ApprovalID
				}
			}
		}
		if id == "" {
			t.Fatalf("no lane approval waits: %+v", run)
		}
		if err := service.Approve(ctx, id, "operator-fixture", "approve exact lane"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Resume(ctx, runID); err != nil {
			t.Fatal(err)
		}
	}

	if _, err = s.BindApprovedPlan(ctx, "first-run"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Execute(ctx, "first-run", brief, nil); err != nil {
		t.Fatal(err)
	}
	approveWaiting("first-run")
	if _, err = s.Execute(ctx, "first-run", brief, nil); err != nil {
		t.Fatal(err)
	}
	approveWaiting("first-run")
	limited, err := s.Execute(ctx, "first-run", brief, nil)
	if err != nil {
		t.Fatal(err)
	}
	if limited.Pause == nil || limited.Pause.Code != marshal.CodeGovernedRework || calls.Load() != 2 {
		t.Fatalf("first run did not stop at its rework limit: %+v calls=%d", limited.Pause, calls.Load())
	}
	// The stop names a way on that keeps the run's work.
	if next := strings.Join(limited.Pause.Resolutions, " "); !strings.Contains(next, "/marshal reassign fix") || !strings.Contains(next, `"action":"reassign"`) {
		t.Fatalf("rework-limit stop gives no working path: %q", next)
	}
	parent := ""
	runs, err := service.ListRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.ParentRunID == "" && len(run.TaskLanes) > 0 {
			parent = run.RunID
		}
	}
	if parent == "" {
		t.Fatal("no task lane parent")
	}
	spent := execution.TaskLaneID(parent, "fix", 1)

	// /marshal use-plan on the same approved plan.
	second, err := s.BindApprovedPlan(ctx, "second-run")
	if err != nil {
		t.Fatal(err)
	}
	if got := taskLaneRework(second.Tasks[0]); got != 2 {
		t.Fatalf("second run numbers its first attempt %d, want 2 (after the first run's attempts 0 and 1): it would be refused as out of order", got)
	}
	waiting, err := s.Execute(ctx, "second-run", brief, nil)
	if err != nil {
		snapshot, _ := s.Snapshot(ctx, "second-run")
		t.Fatalf("second run's first attempt failed: %v; task %+v", err, snapshot.Tasks[0].EscalationReason)
	}
	task := waiting.Tasks[0]
	if task.State == marshal.Escalated || (waiting.Pause != nil && waiting.Pause.Code == marshal.CodeLaneAdmission) {
		t.Fatalf("second run's first attempt refused: %+v %+v", task, waiting.Pause)
	}
	if task.Process05WaitRunID != execution.TaskLaneID(parent, "fix", 2) || task.Process05WaitRunID == spent {
		t.Fatalf("second run opened %q, want the fresh lane after %s", task.Process05WaitRunID, spent)
	}
	if len(task.ReturnsByAgent) != 0 {
		t.Fatalf("second run inherited returns: %+v", task.ReturnsByAgent)
	}
	old, err := service.GetRun(ctx, spent)
	if err != nil || old.State != execution.RunDonePendingVerification {
		t.Fatalf("earlier run's lane changed: %v %+v", err, old.State)
	}
}
