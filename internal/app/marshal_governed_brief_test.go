package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

const governedNoteFixture = "# header-provider-dots\n\nWrite `tools/e2e/results/v011-e2e-header-provider-dots.md` with the command and `Result: PASS`.\n\nLeave alone: every other file.\n"

func TestGovernedBriefCarriesWholeApprovedPlanNote(t *testing.T) {
	block, err := renderGovernedBrief("header-provider-dots", governedNoteFixture, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tasks/header-provider-dots.md", "not in your worktree", "tools/e2e/results/v011-e2e-header-provider-dots.md", "Leave alone: every other file."} {
		if !strings.Contains(block, want) {
			t.Fatalf("governed brief lacks %q:\n%s", want, block)
		}
	}
	if len(block) > governedBriefBytes {
		t.Fatalf("governed brief is %d bytes", len(block))
	}
	if empty, err := renderGovernedBrief("t", "  \n", nil); err != nil || empty != "" {
		t.Fatalf("empty note rendered %q %v", empty, err)
	}
}

func TestGovernedBriefRefusesOversizedNoteInsteadOfDroppingIt(t *testing.T) {
	note := strings.Repeat("x", governedBriefBytes)
	if _, err := renderGovernedBrief("big", note, nil); !errors.Is(err, errGovernedBriefTooLarge) || !strings.Contains(err.Error(), "tasks/big.md") {
		t.Fatalf("oversized note was not refused with its path: %v", err)
	}
	tasks := []marshal.Task{{PlanTaskID: "big", Mode: marshal.Governed}, {PlanTaskID: "native", Mode: marshal.Native}}
	pack := &marshal.PlanPack{Tasks: map[string]string{"big": note, "native": note}}
	if err := validateGovernedPlanNotes(pack, tasks); !errors.Is(err, errGovernedBriefTooLarge) {
		t.Fatalf("draft with an undeliverable governed note accepted: %v", err)
	}
	if err := validateGovernedPlanNotes(pack, tasks[1:]); err != nil {
		t.Fatalf("native note is delivered in full by its own brief: %v", err)
	}
	if err := validateGovernedPlanNotes(nil, tasks); err != nil {
		t.Fatalf("run without a pack: %v", err)
	}
	if err := validateDraft(MarshalDraft{Plan: plan.ExecutionPlan{ID: "p", Version: 1, State: plan.StateReady}, Tasks: tasks[:1], Pack: pack}); err == nil {
		t.Fatal("validateDraft accepted an undeliverable governed note")
	}
}

func TestGovernedBriefKeepsNewestReturnReasonsWithinBound(t *testing.T) {
	var reasons []string
	for i := 0; i < 40; i++ {
		reasons = append(reasons, strings.Repeat("r", 150))
	}
	reasons = append(reasons, "out of scope file: docs/x-evidence.md; this task may change only: tools/e2e/results/x.md")
	block, err := renderGovernedBrief("x", governedNoteFixture, reasons)
	if err != nil {
		t.Fatal(err)
	}
	if len(block) > governedBriefBytes {
		t.Fatalf("governed brief is %d bytes, bound %d", len(block), governedBriefBytes)
	}
	if !strings.Contains(block, "tools/e2e/results/x.md") || !strings.Contains(block, "older reasons omitted") || !strings.Contains(block, "Leave alone") {
		t.Fatalf("newest reason or omission count missing:\n%s", block)
	}
}

func TestProcess05BriefReachesOnlyItsOwnTask(t *testing.T) {
	ctx := withProcess05Brief(context.Background(), "a", "NOTE-A")
	if got := process05BriefFor(ctx, execution.TaskExecution{TaskID: "a"}); got != "NOTE-A" {
		t.Fatalf("own task brief = %q", got)
	}
	if got := process05BriefFor(ctx, execution.TaskExecution{TaskID: "b"}); got != "" {
		t.Fatalf("another task received the brief: %q", got)
	}
	if withProcess05Brief(context.Background(), "a", "") != context.Background() {
		t.Fatal("empty brief changed the context")
	}
}

func TestOutOfScopeReturnReasonNamesAllowedPaths(t *testing.T) {
	task := marshal.Task{Type: marshal.TaskVerification, BaseCommit: "base", Files: []string{"tools/e2e/results/v011-e2e-header-provider-dots.md", "internal/tui/"}}
	err := marshal.ValidateHandIn(task, marshal.HandIn{ResultCommit: "result", TreeDigest: "tree", FilesTouched: []string{"tasks/header-provider-dots-evidence.md"}})
	if err == nil {
		t.Fatal("out of scope file accepted")
	}
	for _, want := range []string{"out of scope file: tasks/header-provider-dots-evidence.md", "tools/e2e/results/v011-e2e-header-provider-dots.md", "internal/tui"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("return reason %q lacks %q", err, want)
		}
	}
}

// The governed Process 05 path never delivers the Marshal brief; the approved
// plan note must still reach the worker's provider context.
func TestMarshalProcess05DeliversApprovedPlanNote(t *testing.T) {
	ctx := context.Background()
	fixture, _ := marshalUserCloseFixture(t, 1)
	runtime := requiredProcess05TestRuntime(t, fixture)
	ctx = requiredTestOwnerContext(t, runtime, ctx)
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := requiredProcess05TestProjectID(runtime, projectRecord.ID)
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-marshal", "SESSION-marshal", projectID
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	worker := requiredProcess05TestHarnessName("test-harness")
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(projectID)
	p, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
	if err := runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if p, err = runtime.Plans().Approve(ctx, projectid.ID(projectID)); err != nil || p.State != plan.StateApproved {
		t.Fatalf("approve canonical plan: %+v %v", p, err)
	}
	var delivered atomic.Value
	var executions atomic.Int32
	requiredRegisterProcess05TestHarness(t, runtime, worker, func(ctx context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		executions.Add(1)
		delivered.Store(process05BriefFor(ctx, task))
		trap, err := runtime.armHoneypot(worktree)
		if err != nil {
			return execution.TaskResult{}, err
		}
		t.Cleanup(func() { _ = trap.Close() })
		if _, err := os.Stat(filepath.Join(worktree, "tasks", "fix.md")); err == nil {
			return execution.TaskResult{}, errors.New("plan note was materialised inside the worker's write scope")
		}
		if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixed\n"), 0600); err != nil {
			return execution.TaskResult{}, err
		}
		return execution.TaskResult{TaskID: task.TaskID, Success: true, EvidenceList: process05ObservedTestEvidence(task)}, nil
	})
	providerContext := captureProcess05ProviderContext(t, runtime, worker)
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
	s.ModelProvider = requiredProcess05TestModelProvider("test-harness")
	if _, err := s.BindApprovedPlan(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	// Approval binds the pack stored with the run, as canonical approval does.
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	pack := marshal.PlanPack{Requirements: "fix the README", Index: "| fix |", Tasks: map[string]string{"fix": governedNoteFixture}}
	pack.Digest = planPackDigest(pack)
	run.Pack = &pack
	approved, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	run.ApprovalScopeDigest = marshalApprovalDigest(approved.ApprovalScopeDigest, run)
	if err := s.save(ctx, "run", run, rev); err != nil {
		t.Fatal(err)
	}
	brief := func(marshal.Task, BriefContext) string { return "fix the typo" }
	if _, err := s.Execute(ctx, "run", brief, nil); err != nil && !strings.Contains(err.Error(), "approval") {
		t.Fatalf("first governed dispatch: %v", err)
	}
	if paused, err := runtime.Execution().ListRuns(ctx); err == nil && len(paused) == 1 && paused[0].State == execution.RunNeedsApproval {
		if err := runtime.Execution().Approve(ctx, paused[0].Tasks["fix"].ApprovalID, "operator", "approve governed task"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Resume(ctx, "run"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Execute(ctx, "run", brief, nil); err != nil {
			t.Fatalf("resume governed task: %v", err)
		}
	}
	if executions.Load() != 1 {
		t.Fatalf("provider executed %d times, want one", executions.Load())
	}
	got, _ := delivered.Load().(string)
	for _, want := range []string{"tasks/fix.md", "tools/e2e/results/v011-e2e-header-provider-dots.md", "Leave alone: every other file."} {
		if !strings.Contains(got, want) {
			t.Fatalf("governed worker context lacks %q:\n%s", want, got)
		}
	}
	wire, captured := providerContext()
	if isExtremeBuild && !captured {
		t.Fatal("the Marshal provider request was not observed")
	}
	if captured {
		if !strings.Contains(wire, got) || !strings.Contains(wire, "Canonical approved task instructions") && !strings.Contains(wire, "GOVERNED EXECUTION CONSTRAINTS") {
			t.Fatalf("provider request lacks the governed brief:\n%s", wire)
		}
	}
}
