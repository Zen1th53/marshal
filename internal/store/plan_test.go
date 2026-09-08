package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

const planTestProject = projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")

func storedPlan() plan.ExecutionPlan {
	return plan.ExecutionPlan{
		ID:        "PLAN-1",
		ProjectID: planTestProject,
		Goal: plan.GoalBinding{
			GoalID: "GOAL-1", Revision: 1, ConstraintDigest: "sha256:abc",
		},
		Version:             1,
		State:               plan.StateReady,
		Mode:                plan.ModeStandard,
		ConstitutionVersion: constitution.Current,
		Tasks: []plan.Task{
			{ID: "implement", Title: "add the cache", Mutating: true, Weight: 1},
		},
		Graph:     plan.Graph{Order: []string{"implement"}, Digest: "sha256:graph"},
		CreatedAt: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
	}
}

func migratedStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func TestPlanRoundTripsThroughStorage(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)

	if err := st.SavePlan(ctx, storedPlan(), 0); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	loaded, err := st.GetPlan(ctx, "PLAN-1", 1)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if loaded.ID != "PLAN-1" || loaded.Version != 1 {
		t.Fatalf("loaded %s version %d", loaded.ID, loaded.Version)
	}
	if loaded.State != plan.StateReady {
		t.Fatalf("state came back as %s", loaded.State)
	}
	if loaded.Goal.Revision != 1 || loaded.Goal.GoalID != "GOAL-1" {
		t.Fatal("the goal binding did not survive storage")
	}
	// The binding digest is what staleness is judged against, so losing it
	// would make every reloaded plan look current.
	if loaded.Goal.ConstraintDigest != "sha256:abc" {
		t.Fatal("the constraint digest did not survive storage")
	}
	if len(loaded.Tasks) != 1 || loaded.Tasks[0].ID != "implement" {
		t.Fatalf("tasks came back as %+v", loaded.Tasks)
	}
	if loaded.Graph.Digest != "sha256:graph" {
		t.Fatal("the graph digest did not survive storage")
	}
	if loaded.ConstitutionVersion.Compare(constitution.Current) != 0 {
		t.Fatal("the constitution binding did not survive storage")
	}
}

// A plan outlives the process that made it. Without this, every restart would
// silently discard an approved plan.
func TestPlanSurvivesAReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	approved := storedPlan()
	approved.State = plan.StateApproved
	if err := first.SavePlan(ctx, approved, 0); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	if err := second.Migrate(ctx); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	loaded, err := second.GetActivePlan(ctx, planTestProject)
	if err != nil {
		t.Fatalf("GetActivePlan after restart: %v", err)
	}
	if loaded.State != plan.StateApproved {
		t.Fatalf("the approval did not survive the restart: state is %s", loaded.State)
	}
	if !loaded.State.Executable() {
		t.Fatal("a plan approved before the restart is no longer executable after it")
	}
}

// A stale writer is refused rather than winning. Two surfaces revising the same
// plan must not silently overwrite each other.
func TestConcurrentWritersCannotOverwriteEachOther(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)

	if err := st.SavePlan(ctx, storedPlan(), 0); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	// Both surfaces read version 1.
	first, err := st.GetPlan(ctx, "PLAN-1", 1)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	second, err := st.GetPlan(ctx, "PLAN-1", 1)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}

	first.RevisionReason = "the first surface's edit"
	if err := st.SavePlan(ctx, first, 1); err != nil {
		t.Fatalf("the first writer was refused: %v", err)
	}

	second.RevisionReason = "the second surface's edit"
	err = st.SavePlan(ctx, second, 1)
	if !errors.Is(err, plan.ErrPlanConflict) {
		t.Fatalf("a stale write returned %v, want a conflict", err)
	}

	// The first writer's edit is intact: the loser did not partially apply.
	current, err := st.GetActivePlan(ctx, planTestProject)
	if err != nil {
		t.Fatalf("GetActivePlan: %v", err)
	}
	if current.RevisionReason != "the first surface's edit" {
		t.Fatalf("the losing write took effect anyway: %q", current.RevisionReason)
	}
	if current.Version != 2 {
		t.Fatalf("active version is %d, want 2", current.Version)
	}
}

// Creating a plan that already exists is a conflict, not an overwrite.
func TestCreatingAnExistingPlanIsRefused(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)

	if err := st.SavePlan(ctx, storedPlan(), 0); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	if err := st.SavePlan(ctx, storedPlan(), 0); !errors.Is(err, plan.ErrPlanConflict) {
		t.Fatalf("re-creating an existing plan returned %v, want a conflict", err)
	}
}

// Every version is kept, so what was approved stays inspectable after the plan
// moves on.
func TestPlanHistoryIsRetained(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)

	if err := st.SavePlan(ctx, storedPlan(), 0); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	approved, err := st.GetPlan(ctx, "PLAN-1", 1)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	approved.State = plan.StateApproved
	if err := st.SavePlan(ctx, approved, 1); err != nil {
		t.Fatalf("SavePlan v2: %v", err)
	}
	revised, err := st.GetPlan(ctx, "PLAN-1", 2)
	if err != nil {
		t.Fatalf("GetPlan v2: %v", err)
	}
	revised.State = plan.StateDraft
	revised.RevisionReason = "the user changed the approach"
	revised.Supersedes = 2
	if err := st.SavePlan(ctx, revised, 2); err != nil {
		t.Fatalf("SavePlan v3: %v", err)
	}

	versions, err := st.ListPlanVersions(ctx, "PLAN-1")
	if err != nil {
		t.Fatalf("ListPlanVersions: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("history holds %d versions, want 3", len(versions))
	}
	// The approved version is still readable as it was approved.
	if versions[1].State != plan.StateApproved {
		t.Fatalf("the approved version now reads as %s", versions[1].State)
	}
	if versions[2].RevisionReason != "the user changed the approach" {
		t.Fatal("the revision reason was not retained")
	}
	if versions[2].Supersedes != 2 {
		t.Fatal("the history does not record what the revision replaced")
	}
}

// A stored plan version can never be rewritten in place.
//
// This is what makes the audit trail worth having: if an approved version
// could be overwritten, "what was approved" would mean whatever was written
// last rather than what the user actually agreed to.
//
// SavePlan cannot reach this case on its own — CAS only ever assigns
// expectedVersion+1, so a correct caller's writes strictly increase and never
// collide. The primary key is therefore a backstop for a defect elsewhere in
// MARSHAL rather than a guard on the normal path, and the raw insert below is
// what tests it: going through SavePlan would be stopped earlier by CAS and
// would prove nothing about the row.
func TestStoredPlanVersionsAreImmutable(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)

	original := storedPlan()
	original.State = plan.StateApproved
	original.RevisionReason = "what the user approved"
	if err := st.SavePlan(ctx, original, 0); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	// Reach the INSERT with a version that already exists. Going through the
	// create path would be rejected earlier, by the "plan already exists"
	// check, and would prove nothing about the row itself — so this writes
	// directly, the way a defect elsewhere in MARSHAL eventually would.
	//
	// The storage layer must refuse on its own: the version counter advancing
	// is a property of well-behaved callers, whereas the audit trail has to
	// survive a badly-behaved one.
	body, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = st.db.ExecContext(ctx, `
		INSERT INTO execution_plans (
			plan_id, version, project_id, goal_id, goal_revision, state, mode,
			constitution_version, graph_digest, supersedes, revision_reason,
			plan_json, created_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, "PLAN-1", 1, string(planTestProject), "GOAL-1", 1, string(plan.StateDraft),
		string(plan.ModeStandard), constitution.Current.String(), "sha256:graph",
		0, "something nobody approved", string(body), utcNow())
	if err == nil {
		t.Fatal("an existing plan version was rewritten in place")
	}

	stored, err := st.GetPlan(ctx, "PLAN-1", 1)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if stored.RevisionReason != "what the user approved" {
		t.Fatalf("version 1 now reads %q; the approved version was overwritten", stored.RevisionReason)
	}
	if stored.State != plan.StateApproved {
		t.Fatalf("version 1 now reads as %s", stored.State)
	}

	versions, err := st.ListPlanVersions(ctx, "PLAN-1")
	if err != nil {
		t.Fatalf("ListPlanVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("history holds %d versions after a refused rewrite, want 1", len(versions))
	}
}

// Plans are found by the Goal revision they were built from, which is how
// staleness is detected after a Goal moves on.
func TestPlansAreQueryableByGoalRevision(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)

	if err := st.SavePlan(ctx, storedPlan(), 0); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	forRevisionTwo := storedPlan()
	forRevisionTwo.ID = "PLAN-2"
	forRevisionTwo.Goal.Revision = 2
	if err := st.SavePlan(ctx, forRevisionTwo, 0); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	atOne, err := st.PlansForGoal(ctx, "GOAL-1", 1)
	if err != nil {
		t.Fatalf("PlansForGoal: %v", err)
	}
	if len(atOne) != 1 || atOne[0].ID != "PLAN-1" {
		t.Fatalf("revision 1 returned %d plans", len(atOne))
	}
	atTwo, err := st.PlansForGoal(ctx, "GOAL-1", 2)
	if err != nil {
		t.Fatalf("PlansForGoal: %v", err)
	}
	if len(atTwo) != 1 || atTwo[0].ID != "PLAN-2" {
		t.Fatalf("revision 2 returned %d plans", len(atTwo))
	}
}

// Missing plans are reported as missing rather than as empty plans, which
// would read as a plan with no work in it.
func TestMissingPlansAreReportedAsMissing(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)

	if _, err := st.GetPlan(ctx, "PLAN-nonexistent", 1); !errors.Is(err, plan.ErrPlanNotFound) {
		t.Fatalf("GetPlan on a missing plan returned %v", err)
	}
	if _, err := st.GetActivePlan(ctx, planTestProject); !errors.Is(err, plan.ErrPlanNotFound) {
		t.Fatalf("GetActivePlan with no plan returned %v", err)
	}
	if err := st.SavePlan(ctx, storedPlan(), 5); !errors.Is(err, plan.ErrPlanNotFound) {
		t.Fatalf("updating a nonexistent plan returned %v", err)
	}
}

// A plan missing the state it cannot be stored without is refused, rather than
// stored as a row nothing can bind to.
func TestIncompletePlansAreRefused(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)

	cases := map[string]func(*plan.ExecutionPlan){
		"no id":            func(p *plan.ExecutionPlan) { p.ID = "" },
		"no project":       func(p *plan.ExecutionPlan) { p.ProjectID = "" },
		"no goal":          func(p *plan.ExecutionPlan) { p.Goal.GoalID = "" },
		"no goal revision": func(p *plan.ExecutionPlan) { p.Goal.Revision = 0 },
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			broken := storedPlan()
			damage(&broken)
			if err := st.SavePlan(ctx, broken, 0); !errors.Is(err, plan.ErrPlanInvalid) {
				t.Fatalf("a plan with %s was stored (err=%v)", name, err)
			}
		})
	}
}

// Each project has its own current plan; one project's plan never surfaces as
// another's.
func TestActivePlansAreIsolatedPerProject(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)

	other := projectid.ID("PROJECT-fedcba9876543210fedcba9876543210")
	if err := st.SavePlan(ctx, storedPlan(), 0); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	otherPlan := storedPlan()
	otherPlan.ID = "PLAN-other"
	otherPlan.ProjectID = other
	if err := st.SavePlan(ctx, otherPlan, 0); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	mine, err := st.GetActivePlan(ctx, planTestProject)
	if err != nil {
		t.Fatalf("GetActivePlan: %v", err)
	}
	if mine.ID != "PLAN-1" {
		t.Fatalf("project %s sees plan %s", planTestProject, mine.ID)
	}
	theirs, err := st.GetActivePlan(ctx, other)
	if err != nil {
		t.Fatalf("GetActivePlan: %v", err)
	}
	if theirs.ID != "PLAN-other" {
		t.Fatalf("project %s sees plan %s", other, theirs.ID)
	}
}
