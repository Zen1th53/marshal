//go:build !governed

package app

import (
	"context"
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
	"github.com/Zen1th53/marshal/internal/statepath"
)

// Marshal was stopped (SIGTERM) while a governed worker ran task convert's
// first lane attempt: after the restart the durable lane is RUNNING and no
// executor of the new runtime owns it. The Marshal's dispatch of convert is
// refused as an orphan; Marshal closes the interrupted attempt (its record
// kept, nothing claimed), tells the person so in plain words, and starts the
// task again in its next lane. The run completes with no pause and no rework
// counted.
func TestExtremeInterruptedLaneClosedAfterRestartAndTaskRunsAgain(t *testing.T) {
	ctx := context.Background()
	fixture, _ := marshalFixture(t, 1)
	runtime := requiredProcess05TestRuntime(t, fixture)
	ctx = requiredTestOwnerContext(t, runtime, ctx)
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := requiredProcess05TestProjectID(runtime, projectRecord.ID)
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-p05-orphan", "SESSION-p05-orphan", projectID
	goal.SuccessCriteria = []string{"convert.txt exists", "tests.txt exists"}
	if err = runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.Mode = plan.ModeUltra
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(projectID)
	request.Tasks = []plan.Task{
		{ID: "convert", Title: "convert the notes", Mutating: true, Weight: 1, Paths: []string{"convert.txt"}, Criteria: []string{"convert.txt exists"}, Instructions: "Write convert.txt"},
		{ID: "tests", Title: "add the tests", Mutating: true, Weight: 1, Paths: []string{"tests.txt"}, Criteria: []string{"tests.txt exists"}, Instructions: "Write tests.txt"},
	}
	p, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"convert": {"test -f convert.txt"}, "tests": {"test -f tests.txt"}}
	if err = runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Plans().Approve(ctx, projectid.ID(projectID)); err != nil {
		t.Fatal(err)
	}
	var turns sync.Map
	requiredRegisterProcess05TestHarness(t, runtime, "codex", func(_ context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		count, _ := turns.LoadOrStore(task.TaskID, new(atomic.Int32))
		count.(*atomic.Int32).Add(1)
		trap, err := runtime.armHoneypot(worktree)
		if err != nil {
			return execution.TaskResult{}, err
		}
		t.Cleanup(func() { _ = trap.Close() })
		if err := os.WriteFile(filepath.Join(worktree, task.TaskID+".txt"), []byte("done\n"), 0600); err != nil {
			return execution.TaskResult{}, err
		}
		return execution.TaskResult{TaskID: task.TaskID, Success: true}, nil
	})
	var activity []string
	var activityMu sync.Mutex
	s, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	s.Gate = ultraGate{enabled: true}
	s.GateState = fixture.GateState
	s.ModelProvider = "review-test"
	s.Model = marshalFakeModel{review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	s.GovernedCheck = fixtureCheckRunner
	s.InstalledVersion = func(context.Context, string) string { return "1.0" }
	s.RecordActivity = func(line string) { activityMu.Lock(); activity = append(activity, line); activityMu.Unlock() }
	if err = s.Store.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: "codex", InstalledVersion: "1.0", ProbeEvidenceID: "fixture-probe", ProbedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers = map[string]driver.Driver{"codex": driver.Governed{Provider: "codex", Run: runtime.marshalProcess05Run(s)}}
	s.CrossReview = func(_ context.Context, task marshal.Task, _ marshal.HandIn, _ marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{"check:test -f " + task.PlanTaskID + ".txt"}}, "review-test", nil
	}
	s.Verify = fixture.Verify
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "verifier-test", nil }
	if s.process05CloseOrphanLane == nil {
		t.Fatal("the runtime does not close an interrupted lane")
	}
	const id = "p05-orphan"
	if _, err = s.BindApprovedPlan(ctx, id); err != nil {
		t.Fatal(err)
	}
	bound, _, err := s.load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	convert := bound.Tasks[taskIndex(bound, "convert")]

	// The earlier process: it opened convert's first lane and its worker
	// started, then Marshal was killed before anything was recorded.
	approved, err := runtime.Store().GetPlan(ctx, bound.PlanID, bound.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Plans().Handoff(ctx, goal.SessionID, s.CanonicalPlanProjectID()); err != nil {
		t.Fatal(err)
	}
	parent, err := runtime.Execution().StartRunBound(ctx, goal.SessionID, s.CanonicalPlanProjectID(), approved.ID, approved.Version, approved.Goal.RequestDigest+"/"+approved.Goal.ConstraintDigest)
	if err != nil {
		t.Fatal(err)
	}
	engine := runtime.Execution().Engine()
	if err = engine.SetTaskLaneParent(ctx, parent.RunID, bound.BaseCommit); err != nil {
		t.Fatal(err)
	}
	lane, err := engine.OpenTaskLane(ctx, parent.RunID, "convert", convert.BaseCommit)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := execution.NewFileRunStore(filepath.Join(runtime.layout.Root, statepath.DirName, "execution", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	interrupted, err := disk.GetRun(ctx, lane.RunID)
	if err != nil {
		t.Fatal(err)
	}
	task := interrupted.Tasks["convert"]
	started := time.Now().UTC()
	task.State, task.Attempts, task.StartedAt = execution.TaskRunning, 1, &started
	interrupted.Tasks["convert"] = task
	interrupted.State, interrupted.CurrentPhase = execution.RunRunning, execution.PhaseExecuting
	if err = disk.UpdateRun(ctx, interrupted); err != nil {
		t.Fatal(err)
	}
	// The restart: this runtime's execution layer reopens from the durable
	// store, with no executor for the lane.
	runtime.execMu.Lock()
	runtime.execService = nil
	runtime.execMu.Unlock()
	service := runtime.Execution()
	if reopened, err := service.GetRun(ctx, lane.RunID); err != nil || reopened.State != execution.RunRunning {
		t.Fatalf("the restarted runtime does not see the interrupted lane: %v", err)
	}

	brief := func(marshal.Task, BriefContext) string { return "do the approved task" }
	var finished marshal.Run
	for range 20 {
		finished, err = s.Execute(ctx, id, brief, nil)
		if err != nil || finished.State != marshal.AwaitingUser || finished.Pause == nil || finished.Pause.Code != marshal.CodeApprovalWait {
			break
		}
		runs, err := service.ListRuns(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, run := range runs {
			if task := run.Tasks[run.LaneTaskID]; run.ParentRunID != "" && task.State == execution.TaskNeedsApproval {
				if err = service.Approve(ctx, task.ApprovalID, "operator", "approve the exact task"); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err = s.Resume(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err != nil {
		t.Fatalf("the run failed after the restart: %v (pause %+v)", err, finished.Pause)
	}
	if finished.State != marshal.Verifying || finished.Pause != nil {
		t.Fatalf("the run did not pass verification: %s %+v", finished.State, finished.Pause)
	}
	for _, task := range finished.Tasks {
		if task.State != marshal.Merged || len(task.ReturnsByAgent) != 0 {
			t.Fatalf("task %s ended %s (returns %v)", task.PlanTaskID, task.State, task.ReturnsByAgent)
		}
	}
	closed, err := service.GetRun(ctx, lane.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.State != execution.RunPaused || closed.Tasks["convert"].ResultCommit != "" || closed.Tasks["convert"].StartedAt == nil {
		t.Fatalf("the interrupted attempt was not closed with its record: %s %+v", closed.State, closed.Tasks["convert"])
	}
	root, err := service.GetRun(ctx, parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if next := root.TaskLanes["convert"]; next != execution.TaskLaneID(parent.RunID, "convert", 1) {
		t.Fatalf("convert did not run in its next lane: %s", next)
	}
	if count, _ := turns.Load("convert"); count == nil || count.(*atomic.Int32).Load() != 1 {
		t.Fatalf("convert ran %v turns after the restart, want one", count)
	}
	want := "convert the notes: its earlier attempt was interrupted; Marshal closed it and will start the task again."
	activityMu.Lock()
	said := strings.Join(activity, "\n")
	activityMu.Unlock()
	if !strings.Contains(said, want) || strings.Contains(said, "didn't match") {
		t.Fatalf("activity does not say the attempt was interrupted:\n%s", said)
	}
	decisions, err := s.Store.MarshalDecisions(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	recorded := false
	for _, ev := range decisions {
		if ev.Type == events.EventTypeMarshalTaskReturned && ev.Data["summary"] == want && strings.Contains(fmt.Sprint(ev.Data["reason"]), "orphan task lane requires explicit reconciliation") {
			recorded = true
		}
	}
	if !recorded {
		t.Fatal("the closed attempt's event lost the plain summary or the raw refusal")
	}
}
