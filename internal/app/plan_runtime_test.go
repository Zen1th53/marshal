package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

const runtimePlanProject = projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")

func TestCreatedPlanCanBeReloaded(t *testing.T) {
	runtime := runtimeForPlan(t)
	service := runtime.Plans()
	created, err := service.Create(context.Background(), planCreateRequest())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	reloaded, err := service.Current(context.Background(), runtimePlanProject)
	if err != nil {
		t.Fatalf("reload active plan: %v", err)
	}
	if reloaded.ID != created.ID || reloaded.Version != 1 {
		t.Fatalf("reloaded plan = %s version %d, want %s version 1", reloaded.ID, reloaded.Version, created.ID)
	}
	if len(reloaded.Assignments.Assignments) == 0 {
		t.Fatal("the stored plan lost its harness assignments")
	}
}

func TestApprovingAPlanCreatesTheNextVersion(t *testing.T) {
	runtime := runtimeForPlan(t)
	service := runtime.Plans()
	if _, err := service.Create(context.Background(), planCreateRequest()); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	approved, err := service.Approve(context.Background(), runtimePlanProject)
	if err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	if approved.Version != 2 || approved.State != plan.StateApproved {
		t.Fatalf("approved plan = version %d state %s, want version 2 APPROVED", approved.Version, approved.State)
	}
}

func TestStalePlanWriteIsRefusedAfterApproval(t *testing.T) {
	runtime := runtimeForPlan(t)
	service := runtime.Plans()
	if _, err := service.Create(context.Background(), planCreateRequest()); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	stale, err := service.Current(context.Background(), runtimePlanProject)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if _, err := service.Approve(context.Background(), runtimePlanProject); err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	if err := runtime.Store().SavePlan(context.Background(), stale.Cancel(time.Now().UTC()), stale.Version); !errors.Is(err, plan.ErrPlanConflict) {
		t.Fatalf("stale save error = %v, want plan conflict", err)
	}
}

func TestCancelledPlanCannotHandOff(t *testing.T) {
	runtime := runtimeForPlan(t)
	service := runtime.Plans()
	if _, err := service.Create(context.Background(), planCreateRequest()); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if _, err := service.Cancel(context.Background(), runtimePlanProject); err != nil {
		t.Fatalf("cancel plan: %v", err)
	}
	if _, err := service.Handoff(context.Background(), "SESSION-plan", runtimePlanProject); err == nil {
		t.Fatal("a cancelled plan reached the handoff boundary")
	}
}

func TestPlanHandoffIsDurablyEvidencedAndIdempotent(t *testing.T) {
	runtime := runtimeForPlan(t)
	service := runtime.Plans()
	ctx := context.Background()
	if _, err := service.Create(ctx, planCreateRequest()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(ctx, runtimePlanProject); err != nil {
		t.Fatal(err)
	}
	first, err := service.Handoff(ctx, "SESSION-plan", runtimePlanProject)
	if err != nil {
		t.Fatalf("first handoff: %v", err)
	}
	second, err := service.Handoff(ctx, "SESSION-plan", runtimePlanProject)
	if err != nil {
		t.Fatalf("idempotent handoff: %v", err)
	}
	if first.EvidenceID == "" || second.EvidenceID != first.EvidenceID {
		t.Fatalf("handoff evidence ids = %q, %q", first.EvidenceID, second.EvidenceID)
	}
	history, err := runtime.EventsSince(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range history {
		if event.Type == events.EventTypeHandoffCreated && event.ID == first.EvidenceID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("durable handoff evidence count = %d, want 1", count)
	}
}

func TestHandoffRefusesWhenTheGoalHasMoved(t *testing.T) {
	runtime := runtimeForPlan(t)
	service := runtime.Plans()
	if _, err := service.Create(context.Background(), planCreateRequest()); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if _, err := service.Approve(context.Background(), runtimePlanProject); err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	goal, err := runtime.Store().GetActiveGoalContract(context.Background(), "SESSION-plan")
	if err != nil {
		t.Fatalf("read goal: %v", err)
	}
	goal.Revision++
	goal.RevisionReason = "criterion added after planning"
	if err := runtime.Store().SaveGoalContract(context.Background(), goal, goal.Revision-1); err != nil {
		t.Fatalf("move goal: %v", err)
	}
	if _, err := service.Handoff(context.Background(), "SESSION-plan", runtimePlanProject); err == nil {
		t.Fatal("a plan for an earlier Goal revision reached handoff")
	}
}

func runtimeForPlan(t *testing.T) *Runtime {
	t.Helper()
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := runtime.Store().SaveGoalContract(ctx, planGoal(), 0); err != nil {
		t.Fatalf("save goal: %v", err)
	}
	return runtime
}

func planGoal() model.GoalContract {
	return model.GoalContract{
		ID: "GOAL-plan", SessionID: "SESSION-plan", Revision: 1,
		ProjectID: string(runtimePlanProject), OriginalRequest: "Fix the documented typo.",
		ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationApproved,
		DesiredOutcome: "The documented typo is fixed", ExpectedArtifact: "README.md",
		SuccessCriteria: []string{"the typo is fixed"}, Risk: model.R1, AuthoritySource: "operator",
	}
}

func planCreateRequest() CreatePlanRequest {
	now := time.Now().UTC()
	capacity := goalintake.UnknownCapacity("openai", true)
	return CreatePlanRequest{
		SessionID: "SESSION-plan", ProjectID: runtimePlanProject,
		Assessment: goalintake.Assessment{},
		Tasks:      []plan.Task{{ID: "fix", Title: "fix the documented typo", Mutating: true, Weight: 1, Paths: []string{"README.md"}, Criteria: []string{"the typo is fixed"}}},
		Candidates: []goalintake.Candidate{{Provider: "openai", Model: "gpt-test", Capacity: capacity, Governance: constitution.GovernanceVerified}},
		HarnessCandidates: []plan.HarnessCandidate{{
			Profile:          model.HarnessProfile{Harness: "test-harness", InstalledVersion: "1.0.0", SupportedModels: []string{"gpt-test"}, DefaultModel: "gpt-test", ProbeEvidenceID: "EVIDENCE-plan", ProbedAt: now},
			InstalledVersion: "1.0.0", Provider: "openai", Capacity: capacity,
		}},
	}
}
