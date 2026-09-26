package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestMarshalGovernedTaskUsesProcess05AndImportsExactCommit(t *testing.T) {
	ctx := context.Background()
	fixture, _ := marshalFixture(t, 1)
	runtime := &Runtime{store: fixture.Store, layout: project.Layout{Root: fixture.Repository, Worktrees: fixture.Worktrees}}
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !projectid.ID(projectRecord.ID).Valid() {
		t.Fatalf("fixture project ID %q is invalid", projectRecord.ID)
	}
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-marshal", "SESSION-marshal", projectRecord.ID
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	request := planCreateRequest()
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(projectRecord.ID)
	p, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
	if err := runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	p, err = runtime.Plans().Approve(ctx, projectid.ID(projectRecord.ID))
	if err != nil || p.State != plan.StateApproved {
		t.Fatalf("approve canonical plan: %+v %v", p, err)
	}
	execService := runtime.Execution()
	execService.RegisterHarness(execution.NewMockHarness("test-harness", func(_ context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixed\n"), 0600); err != nil {
			return execution.TaskResult{}, err
		}
		return execution.TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	s, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	s.Model = marshalFakeModel{review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	s.InstalledVersion = func(context.Context, string) string { return "1.0" }
	if err := s.Store.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: "test-harness", InstalledVersion: "1.0", ProbeEvidenceID: "process05-probe", ProbedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers["test-harness"] = driver.Governed{Provider: "test", Run: runtime.marshalProcess05Run(s)}
	s.ModelProvider = "test-harness"
	if _, err := s.BindApprovedPlan(ctx, "self-review"); err == nil || !strings.Contains(err.Error(), "review its own") {
		t.Fatalf("self-review was admitted: %v", err)
	}
	s.ModelProvider = "codex"
	run, err := s.BindApprovedPlan(ctx, "run")
	if err != nil || run.Tasks[0].Mode != marshal.Governed {
		t.Fatal(err)
	}
	tampered := run.Tasks[0]
	tampered.Checks[0].Command = "true"
	if _, err := runtime.marshalProcess05Run(s)(ctx, driver.Request{Task: tampered}); err == nil || !strings.Contains(err.Error(), "checks differ") {
		t.Fatalf("changed check reached Process 05: %v", err)
	}
	_, err = s.Execute(ctx, "run", func(marshal.Task) string { return "fix the typo" }, nil)
	if err == nil || !strings.Contains(err.Error(), "approval") {
		t.Fatalf("Process 05 approval pause was not reported: %v", err)
	}
	paused, err := execService.ListRuns(ctx)
	if err != nil || len(paused) != 1 || paused[0].State != execution.RunNeedsApproval {
		t.Fatalf("Process 05 did not pause for its own approval: %+v %v", paused, err)
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
	finished, err := s.Execute(ctx, "run", func(marshal.Task) string { return "fix the typo" }, nil)
	if err != nil {
		t.Fatalf("resume governed task: %v (Marshal state %s)", err, finished.State)
	}
	if finished.State != marshal.Verifying || finished.Tasks[0].State != marshal.Merged {
		handin, _ := s.Store.GetMarshalHandIn(ctx, "run", "fix", 1)
		t.Fatalf("Marshal run did not merge Process 05 result: %+v handin=%+v", finished, handin.Value.RuntimeObserved)
	}
	runs, err := execService.ListRuns(ctx)
	if err != nil || len(runs) != 1 || runs[0].State != execution.RunDonePendingVerification || runs[0].Tasks["fix"].ResultCommit != finished.Tasks[0].ResultCommit {
		t.Fatalf("Process 05 result does not match Marshal hand-in: %+v %v", runs, err)
	}
}

func TestMarshalBindApprovedPlanRefusesMultipleTasks(t *testing.T) {
	ctx := context.Background()
	fixture, _ := marshalFixture(t, 2)
	if _, err := fixture.StartPlanning(ctx, "original", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Approve(ctx, "original"); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{store: fixture.Store, layout: project.Layout{Root: fixture.Repository, Worktrees: fixture.Worktrees}}
	s, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindApprovedPlan(ctx, "run"); err == nil || !strings.Contains(err.Error(), "one-task") {
		t.Fatalf("multi-task plan was bound: %v", err)
	}
}
