//go:build governed

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/projectid"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

func assertLegacyRunFixtureRefused(*testing.T, *Runtime, model.Task, RunResult, error) bool {
	return false
}

func privateLegacyRunFixture() bool { return false }

func assertLegacyRunFixtureRefusedWithExistingLease(*testing.T, *Runtime, model.Task, RunResult, error) {
}

func requiredProcess05TestRuntime(t *testing.T, fixture *MarshalService) *Runtime {
	t.Helper()
	// A real Runtime always has its event engine; Process 05 records its
	// plan handoff evidence through it.
	// It also has its evidence sanitizer, which the governed brief requires.
	runtime := &Runtime{store: fixture.Store, eventEngine: events.NewEngine(fixture.Store), evidenceSanitizer: evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), layout: project.Layout{
		Root: fixture.Repository, Worktrees: fixture.Worktrees,
		RuntimeDir: filepath.Join(fixture.Repository, projectid.StateDirName),
	}}
	// Process 05 supervises workers against the runtime's canonical identity,
	// which can differ from the legacy project row used by Marshal's records.
	binding, err := projectid.Adopt(t.Context(), nil, runtime.layout.Root, runtime.layout.RuntimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ProjectIdentity() != string(binding.ID) || runtime.Marshal().CanonicalPlanProjectID() != binding.ID {
		t.Fatal("Process 05 and Marshal fixture identities differ")
	}
	return runtime
}

func requiredProcess05TestProjectID(r *Runtime, _ string) string { return r.ProjectIdentity() }

func requiredTestOwnerContext(_ *testing.T, _ *Runtime, ctx context.Context) context.Context {
	return ctx
}

func requiredMarshalTestPrompt(_ *testing.T, _ *MarshalCLI, original, _ string) string {
	return original
}

func requiredTestAcceptedProjectContract(*testing.T, *Runtime) {}

func requiredGoalReportFixture(t *testing.T) (*Runtime, context.Context, model.GoalContract, *execution.ExecutionRun) {
	r := runtimeForPlan(t)
	ctx := t.Context()
	g := planGoal()
	g.ProjectID = r.ProjectIdentity()
	g.SuccessCriteria = []string{"verified", "failed", "not run", "unknown", "missing", "expired", "unmapped"}
	if err := r.Store().SaveGoalContract(ctx, g, 1); err != nil {
		t.Fatal(err)
	}
	g.Revision = 2
	req := planCreateRequest()
	req.ProjectID = projectid.ID(g.ProjectID)
	req.Tasks[0].Criteria = g.SuccessCriteria
	if _, err := r.Plans().Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plans().Approve(ctx, req.ProjectID); err != nil {
		t.Fatal(err)
	}
	run, err := r.Execution().StartRun(ctx, g.SessionID, req.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return r, ctx, g, run
}
func assertUnqualifiedHandoffFixtureRefused(*testing.T, *Runtime, string, error) bool { return false }

func requiredProcess05TestHarnessName(name string) string                      { return name }
func requiredProcess05PlanRequest(request CreatePlanRequest) CreatePlanRequest { return request }
func requiredRegisterProcess05TestHarness(_ *testing.T, r *Runtime, name string, handle func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error)) {
	r.Execution().RegisterHarness(execution.NewMockHarness(name, handle))
}

func requiredProcess05TestModelProvider(original string) string { return original }
