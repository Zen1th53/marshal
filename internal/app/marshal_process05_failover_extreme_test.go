//go:build !governed

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestExtremeProcess05ProviderFailoverKeepsApprovedCanonicalIdentity(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	ctx := context.Background()
	fixture, _ := marshalFixture(t, 1)
	runtime := requiredProcess05TestRuntime(t, fixture)
	ctx = requiredTestOwnerContext(t, runtime, ctx)
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	goal := planGoal()
	goal.ID = "GOAL-p05-failover"
	goal.SessionID = "SESSION-p05-failover"
	goal.ProjectID = requiredProcess05TestProjectID(runtime, projectRecord.ID)
	if err = runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.SessionID = goal.SessionID
	request.ProjectID = projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID))
	const fixtureVersion = "owned-host-adapter-fixture"
	request.HarnessCandidates[0].Profile.Harness = "codex"
	request.HarnessCandidates[0].Profile.InstalledVersion = fixtureVersion
	request.HarnessCandidates[0].InstalledVersion = fixtureVersion
	request.HarnessCandidates[0].Profile.ExpiresAt = time.Now().UTC().Add(time.Hour)
	fallbackProfile := model.HarnessProfile{Harness: "antigravity", InstalledVersion: fixtureVersion, SupportedModels: []string{"test-agy-model"}, DefaultModel: "test-agy-model", ProbeEvidenceID: "owned-AGY-fallback-probe", ProbedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	request.HarnessCandidates = append(request.HarnessCandidates, plan.HarnessCandidate{Profile: fallbackProfile, InstalledVersion: fixtureVersion, Provider: "agy", Capacity: goalintake.UnknownCapacity("agy", true)})
	for _, candidate := range request.HarnessCandidates {
		if err := runtime.Store().SaveHarnessProfile(ctx, candidate.Profile); err != nil {
			t.Fatal(err)
		}
	}
	approved, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	producer, ok := approved.Assignments.For(plan.RoleDeveloper)
	if !ok || producer.Fallback == nil || marshalHarnessName(normalizeProvider(producer.Fallback.Harness)) != "antigravity" || producer.Fallback.Model != "test-agy-model" {
		t.Fatal("fixture lacks actual builder-selected producer fallback")
	}
	approved.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
	if err = runtime.Store().SavePlan(ctx, approved, approved.Version); err != nil {
		t.Fatal(err)
	}
	approved, err = runtime.Plans().Approve(ctx, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID)))
	if err != nil {
		t.Fatal(err)
	}
	service := runtime.Execution()
	var limitedCalls, retryCalls, reviews atomic.Int32
	var verifier atomic.Bool
	arm := func(path string) error {
		trap, err := runtime.armHoneypot(path)
		if err == nil {
			t.Cleanup(func() { _ = trap.Close() })
		}
		return err
	}
	var originalID, originalCanonical, originalPath string
	requiredRegisterProcess05TestHarness(t, runtime, "codex", func(_ context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, path string) (execution.TaskResult, error) {
		limitedCalls.Add(1)
		originalID = task.TaskID
		originalCanonical = task.CanonicalTaskID
		originalPath = path
		if err := arm(path); err != nil {
			return execution.TaskResult{}, err
		}
		if err := os.WriteFile(filepath.Join(path, "partial.txt"), []byte("limited provider partial"), 0600); err != nil {
			return execution.TaskResult{}, err
		}
		return execution.TaskResult{}, errors.New("usage limit exceeded; retry-after: 30")
	})
	requiredRegisterProcess05TestHarness(t, runtime, "agy", func(_ context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, path string) (execution.TaskResult, error) {
		retryCalls.Add(1)
		if task.AssignedModel != "test-agy-model" {
			return execution.TaskResult{}, errors.New("replacement omitted exact approved fallback selector")
		}
		if task.TaskID != originalID || task.CanonicalTaskID != originalCanonical || path != originalPath {
			return execution.TaskResult{}, errors.New("canonical identity changed during provider retry")
		}
		if _, err := os.Stat(filepath.Join(path, "partial.txt")); !os.IsNotExist(err) {
			return execution.TaskResult{}, errors.New("partial provider edits carried into retry")
		}
		if err := arm(path); err != nil {
			return execution.TaskResult{}, err
		}
		if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("fixed\n"), 0600); err != nil {
			return execution.TaskResult{}, err
		}
		return execution.TaskResult{TaskID: task.TaskID, Success: true}, nil
	})
	s, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	s.Gate = ultraGate{enabled: true}
	s.GateState = fixture.GateState
	s.ModelProvider = "review-test"
	s.Model = marshalFakeModel{review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	s.GovernedCheck = fixtureCheckRunner
	s.InstalledVersion = func(context.Context, string) string { return fixtureVersion }
	s.GovernedDrivers = map[string]driver.Driver{}
	for _, name := range []string{"codex", "agy"} {
		if err = s.Store.SaveHarnessProfile(ctx, func() model.HarnessProfile {
			for _, candidate := range request.HarnessCandidates {
				if marshalHarnessName(candidate.Profile.Harness) == marshalHarnessName(name) {
					return candidate.Profile
				}
			}
			t.Fatal("missing owned profile")
			return model.HarnessProfile{}
		}()); err != nil {
			t.Fatal(err)
		}
		s.GovernedDrivers[name] = driver.Governed{Provider: name, Run: runtime.marshalGovernedRun(s)}
	}
	s.CrossReview = func(_ context.Context, task marshal.Task, h marshal.HandIn, _ marshal.Control) (marshal.Review, string, error) {
		reviews.Add(1)
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{"check:grep -q fixed README.md"}}, "review-test", nil
	}
	s.Verify = fixture.Verify
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "verifier-test", nil }
	canonicalVerify := s.requiredCanonicalVerification
	if canonicalVerify == nil {
		t.Fatal("missing owned canonical verifier")
	}
	s.requiredCanonicalVerification = func(ctx context.Context, id string, run marshal.Run, head string) (verification.Session, verification.Binding, error) {
		session, binding, err := canonicalVerify(ctx, id, run, head)
		if err == nil && verification.Evaluate(session, binding, s.clock()) == verification.VerifiedComplete {
			verifier.Store(true)
		}
		return session, binding, err
	}
	bound, err := s.BindApprovedPlan(ctx, "p05-failover")
	if err != nil {
		t.Fatal(err)
	}
	if !bound.Process05Bound || bound.Tasks[0].Worker != "codex" {
		t.Fatalf("wrong initial binding: %+v", bound)
	}
	paused, err := s.Execute(ctx, "p05-failover", func(marshal.Task, BriefContext) string { return "fix the approved typo" }, nil)
	if err != nil || paused.State != marshal.AwaitingUser || paused.Tasks[0].State != marshal.Queued || paused.Tasks[0].Process05WaitApprovalID == "" || limitedCalls.Load() != 0 || retryCalls.Load() != 0 {
		t.Fatalf("initial hard task approval bypassed or treated as failure: %+v %v", paused, err)
	}
	runs, err := service.ListRuns(ctx)
	if err != nil || len(runs) != 1 {
		t.Fatalf("canonical runs %+v %v", runs, err)
	}
	approvalID := runs[0].Tasks["fix"].ApprovalID
	if approvalID == "" {
		t.Fatal("missing exact task approval")
	}
	if err = service.Approve(ctx, approvalID, "operator", "approve exact task scope"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resume(ctx, "p05-failover"); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Execute(ctx, "p05-failover", func(marshal.Task, BriefContext) string { return "fix the approved typo" }, nil)
	if err != nil || waiting.State != marshal.AwaitingUser || limitedCalls.Load() != 1 || retryCalls.Load() != 0 {
		t.Fatalf("replacement started without fresh task approval: state=%s first=%d replacement=%d err=%v", waiting.State, limitedCalls.Load(), retryCalls.Load(), err)
	}
	pending, err := service.ListRuns(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatal("missing exact replacement wait", err)
	}
	replacementApproval := pending[0].Tasks["fix"].ApprovalID
	if replacementApproval == "" || replacementApproval == approvalID || pending[0].Tasks["fix"].AssignedModel != "test-agy-model" {
		t.Fatal("replacement inherited original approval or guessed model")
	}
	oldReceipt, err := service.Engine().ApprovalManager().GetApproval(approvalID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Approve(ctx, replacementApproval, "operator", "approve exact historical fallback binding"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resume(ctx, "p05-failover"); err != nil {
		t.Fatal(err)
	}
	finished, err := s.Execute(ctx, "p05-failover", func(marshal.Task, BriefContext) string { return "fix the approved typo" }, nil)
	afterReceipt, readErr := service.Engine().ApprovalManager().GetApproval(approvalID)
	if readErr != nil || !reflect.DeepEqual(oldReceipt, afterReceipt) {
		t.Fatal("original receipt rewritten", readErr)
	}
	if err != nil {
		actual, _ := service.ListRuns(ctx)
		for _, run := range actual {
			for _, task := range run.Tasks {
				t.Logf("HOST fixture provider=%s model=%s attempts=%d state=%s reason=%q", task.AssignedHarness, task.AssignedModel, task.Attempts, task.State, task.LastFailureReason)
			}
		}
		t.Logf("HOST fixture first=%d retry=%d", limitedCalls.Load(), retryCalls.Load())
		t.Fatalf("Process05 provider retry failed: %v state=%s", err, finished.State)
	}
	if limitedCalls.Load() != 1 || retryCalls.Load() != 1 || reviews.Load() != 1 || !verifier.Load() || finished.Tasks[0].State != marshal.Merged || finished.Tasks[0].Worker != "agy" || finished.Tasks[0].Branch != bound.Tasks[0].Branch {
		t.Fatalf("retry/review/verify mismatch limited=%d retry=%d reviews=%d verified=%v run=%+v", limitedCalls.Load(), retryCalls.Load(), reviews.Load(), verifier.Load(), finished)
	}
	finalPlan, err := s.Store.GetPlan(ctx, approved.ID, approved.Version)
	if err != nil {
		t.Fatal(err)
	}
	if finalPlan.ID != approved.ID || finalPlan.Version != approved.Version || finalPlan.ApprovalScopeDigest != approved.ApprovalScopeDigest || finalPlan.State != plan.StateApproved {
		t.Fatal("provider retry changed approved plan")
	}
	runs, err = service.ListRuns(ctx)
	if err != nil || len(runs) != 1 {
		t.Fatal("provider retry forked canonical run", err)
	}
	task := runs[0].Tasks["fix"]
	if task.CanonicalTaskID != originalCanonical || task.AssignedHarness != "agy" || task.Attempts != 2 || task.ResultCommit != finished.Tasks[0].ResultCommit || task.State != execution.TaskCompletedPendingVerify {
		t.Fatalf("canonical retry mismatch %+v", task)
	}
	if _, err = os.Stat(originalPath + "-limited-1/partial.txt"); err != nil {
		t.Fatal("failed provider evidence lost", err)
	}
}
