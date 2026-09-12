package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/startup"
	"github.com/Zen1th53/marshal/internal/verification"
)

// Work is the section where a UI is most tempted to compute: a readiness
// figure, a progress percentage, a "next task" guess. These tests assert it
// computes none of them, and that its mutations reach real authorities.

type fakeWorkReader struct {
	layout     WorkLayout
	layoutErr  error
	assessment startup.Assessment
	assessErr  error
	goal       model.GoalContract
	goalErr    error
	revisions  []model.GoalContract
	plan       PlanState
	planErr    error
	tasks      []model.Task
	tasksErr   error
	agents     []model.Agent
	artifacts  []model.Artifact
	events     []model.Event
}

func (f fakeWorkReader) Layout(context.Context) (WorkLayout, error) {
	return f.layout, f.layoutErr
}
func (f fakeWorkReader) Assessment(context.Context) (startup.Assessment, error) {
	return f.assessment, f.assessErr
}
func (f fakeWorkReader) Goal(context.Context) (model.GoalContract, error) {
	return f.goal, f.goalErr
}
func (f fakeWorkReader) GoalRevisions(context.Context, string) ([]model.GoalContract, error) {
	return f.revisions, nil
}
func (f fakeWorkReader) Plan(context.Context) (PlanState, error) { return f.plan, f.planErr }
func (f fakeWorkReader) Tasks(context.Context) ([]model.Task, error) {
	return f.tasks, f.tasksErr
}
func (f fakeWorkReader) Agents(context.Context) ([]model.Agent, error)       { return f.agents, nil }
func (f fakeWorkReader) Artifacts(context.Context) ([]model.Artifact, error) { return f.artifacts, nil }
func (f fakeWorkReader) Events(context.Context) ([]model.Event, error)       { return f.events, nil }

func testWork(t *testing.T, reader WorkReader) *WorkSource {
	t.Helper()
	return &WorkSource{Reader: reader, SessionID: "sess-1", ProjectID: "proj-1",
		Now: fixedClock()}
}

// With no runtime attached, every Work field says it could not be read.
func TestWorkWithoutARuntimeIsUnknownNotZero(t *testing.T) {
	snap := (&WorkSource{Now: fixedClock()}).ReadWork(context.Background())

	for name, v := range map[string]Value{
		"root": snap.Root, "goal": snap.GoalID, "plan": snap.PlanID,
		"tasks": snap.TaskStatus, "readiness": snap.Readiness,
	} {
		if v.Status.IsSuccess() {
			t.Fatalf("%s reports a known value with no runtime attached", name)
		}
		if v.Display() == "0" || v.Display() == "" {
			t.Fatalf("%s rendered as %q with no runtime", name, v.Display())
		}
		if v.Reason == "" {
			t.Fatalf("%s is unavailable but gives no reason", name)
		}
	}
}

// An unassessed project is NOT_RUN, never READY. Work is where a user decides
// whether to start executing, so a default of "ready" would be the worst
// possible lie.
func TestWorkReadinessIsNotRunUntilAssessed(t *testing.T) {
	snap := testWork(t, fakeWorkReader{}).ReadWork(context.Background())

	if snap.Readiness.Status.IsSuccess() {
		t.Fatalf("an unassessed project reports readiness %q", snap.Readiness.Display())
	}
	if strings.Contains(strings.ToUpper(snap.Readiness.Display()), "READY") {
		t.Fatalf("an unassessed project rendered as %q", snap.Readiness.Display())
	}
}

func TestProjectPathsDoNotExposeAbsoluteHostLocations(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	root := filepath.Join(home, "Desktop", "work", "marshal")
	snap := testWork(t, fakeWorkReader{layout: WorkLayout{
		Root: root, Repository: root,
	}}).ReadWork(context.Background())
	for name, value := range map[string]Value{"root": snap.Root, "repository": snap.Repository} {
		if strings.Contains(value.Display(), home) || filepath.IsAbs(value.Display()) {
			t.Fatalf("%s exposed absolute host path %q", name, value.Display())
		}
		if !strings.Contains(value.Display(), "marshal") {
			t.Fatalf("%s display lost project identity: %q", name, value.Display())
		}
	}

	outside := knownProjectPath("/srv/tenant/example", workSource)
	if filepath.IsAbs(outside.Display()) || outside.Display() != ".../tenant/example" {
		t.Fatalf("outside-home path display = %q", outside.Display())
	}
}

// A project with no goal says so, rather than rendering an empty goal form
// that looks like a goal with blank fields.
func TestAbsentGoalSaysWhyItIsAbsent(t *testing.T) {
	snap := testWork(t, fakeWorkReader{
		goalErr: errors.New("not found: goal"),
	}).ReadWork(context.Background())

	if snap.GoalID.Status.IsSuccess() {
		t.Fatal("an absent goal reports a known id")
	}
	if snap.GoalID.Reason == "" {
		t.Fatal("an absent goal gives no reason")
	}
	if snap.GoalStatus.Status.IsSuccess() {
		t.Fatal("an absent goal reports a successful status")
	}
}

// A real goal shows the user's own words byte for byte, and its revision.
func TestGoalShowsTheCanonicalContract(t *testing.T) {
	snap := testWork(t, fakeWorkReader{
		goal: model.GoalContract{
			ID: "goal-1", Revision: 4, Confirmation: model.ConfirmationApproved,
			OriginalRequest: "make the tests pass",
			DesiredOutcome:  "a green suite",
			RequestDigest:   "digest-goal",
		},
	}).ReadWork(context.Background())

	if got := snap.GoalRequest.Display(); got != "make the tests pass" {
		t.Fatalf("the original request rendered as %q", got)
	}
	if got := snap.GoalRevision.Display(); got != "4" {
		t.Fatalf("the goal revision rendered as %q", got)
	}
	if got := snap.GoalConfirmation.Display(); got != "APPROVED" {
		t.Fatalf("the confirmation rendered as %q", got)
	}
}

func TestGoalProviderCapacityUsesIntakeEvidenceNotPlanRoutes(t *testing.T) {
	snap := testWork(t, fakeWorkReader{goal: model.GoalContract{
		ID: "goal-capacity", Revision: 1, Confirmation: model.ConfirmationApproved,
		DesiredOutcome: "use evidenced capacity", Risk: model.R1,
		AuthoritySource: "operator", Assessment: map[string]string{
			"provider_capacity": "codex UNKNOWN: provider reported no quota endpoint",
			"complexity":        "medium",
		},
	}}).ReadWork(context.Background())

	got := snap.GoalProviderCapacity.Display()
	if !strings.Contains(got, "codex UNKNOWN") {
		t.Fatalf("provider-capacity evidence = %q", got)
	}
	if strings.Contains(got, "complexity") {
		t.Fatalf("unrelated intake assessment leaked into provider capacity: %q", got)
	}
	content := providerCapacityScreen(snap)
	if len(content.Fields) != 1 || content.Fields[0].Value.Display() != got {
		t.Fatalf("provider-capacity screen does not use Process 03 evidence: %#v", content.Fields)
	}
}

func TestWorkRendererRegistryNeverClaimsGovernedGoalAction(t *testing.T) {
	if _, ok := workScreens()["CTUI-0239"]; ok {
		t.Fatal("the governed persist-goal action was registered as a read-only Work screen")
	}
}

// An empty task list is EMPTY; an unreadable one is not. Both render, and they
// must not render the same.
func TestEmptyTasksAreDistinctFromUnreadableTasks(t *testing.T) {
	empty := testWork(t, fakeWorkReader{}).ReadWork(context.Background())
	if empty.TaskStatus.Status != TruthEmpty {
		t.Fatalf("an empty task list reported %s", empty.TaskStatus.Status.Label())
	}

	broken := testWork(t, fakeWorkReader{
		tasksErr: errors.New("the store is unreachable"),
	}).ReadWork(context.Background())
	if broken.TaskStatus.Status != TruthError {
		t.Fatalf("an unreadable task list reported %s", broken.TaskStatus.Status.Label())
	}
	if !strings.Contains(broken.TaskStatus.Reason, "unreachable") {
		t.Fatalf("the read failure was lost: %q", broken.TaskStatus.Reason)
	}
}

// Tasks render in a stable order, so the selected row means the same thing
// between refreshes.
func TestTaskOrderIsStable(t *testing.T) {
	reader := fakeWorkReader{tasks: []model.Task{
		{ID: "task-c", Status: model.TaskReady, Revision: 1},
		{ID: "task-a", Status: model.TaskWorking, Revision: 2},
		{ID: "task-b", Status: model.TaskReady, Revision: 1},
	}}
	first := testWork(t, reader).ReadWork(context.Background())
	second := testWork(t, reader).ReadWork(context.Background())

	if len(first.Tasks) != 3 {
		t.Fatalf("got %d task rows", len(first.Tasks))
	}
	for i := range first.Tasks {
		if first.Tasks[i].ID.Display() != second.Tasks[i].ID.Display() {
			t.Fatal("the task order varied between reads")
		}
	}
	if first.Tasks[0].ID.Display() != "task-a" {
		t.Fatalf("the first task is %q, want stable order", first.Tasks[0].ID.Display())
	}
}

// The goal history reads newest first: oldest-first buries the current state.
func TestGoalHistoryIsNewestFirst(t *testing.T) {
	snap := testWork(t, fakeWorkReader{
		goal: model.GoalContract{ID: "goal-1", Revision: 3},
		revisions: []model.GoalContract{
			{ID: "goal-1", Revision: 1, UpdatedAt: time.Now()},
			{ID: "goal-1", Revision: 3, UpdatedAt: time.Now()},
			{ID: "goal-1", Revision: 2, UpdatedAt: time.Now()},
		},
	}).ReadWork(context.Background())

	if len(snap.GoalHistory) != 3 {
		t.Fatalf("got %d history rows", len(snap.GoalHistory))
	}
	if got := snap.GoalHistory[0].Revision.Display(); got != "3" {
		t.Fatalf("the first history row is revision %q, want the newest", got)
	}
}

// An uninitialized project says what is missing rather than rendering blank.
func TestUninitializedProjectNamesWhatIsMissing(t *testing.T) {
	snap := testWork(t, fakeWorkReader{layout: WorkLayout{
		Initialized:   false,
		MissingPieces: []string{"state directory", "capability policy"},
	}}).ReadWork(context.Background())

	if snap.SetupState.Status.IsSuccess() {
		t.Fatal("an uninitialized project reports a successful setup state")
	}
	if !strings.Contains(snap.SetupState.Display(), "state directory") {
		t.Fatalf("the missing pieces were not named: %q", snap.SetupState.Display())
	}
}

// --- Work mutations ---

// Confirming a goal goes through the canonical Process 03 boundary and proves
// itself by the revision advancing and the confirmation settling.
func TestConfirmGoalUsesTheCanonicalBoundary(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	c := &Confirmation{}
	binding := source.Bindings()["CTUI-0239"]
	if !binding.Bound() {
		t.Fatal("confirming a goal is not bound")
	}
	if err := c.Begin(ctx, binding, ActionRequest{
		Action: "CTUI-0239", ApprovalID: "app-1"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	outcome, err := c.Submit(ctx)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if outcome.Verdict != VerdictPass {
		t.Fatalf("confirming a goal produced %s: %s", outcome.Verdict, outcome.Detail)
	}
	if n := atomic.LoadInt32(&auth.goalApprovals); n != 1 {
		t.Fatalf("the canonical boundary was called %d times, want exactly 1", n)
	}
	if !outcome.Proof.Status.IsSuccess() {
		t.Fatal("a confirmed goal carries no proof")
	}
}

// A goal that moved since it was reviewed is refused by the canonical
// boundary's own revision check.
func TestConfirmGoalRefusesAStaleRevision(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	req := ActionRequest{Action: "CTUI-0239", ApprovalID: "app-1",
		Target: Target{Kind: "goal", ID: "goal-1", Revision: 99}}
	outcome, _ := source.executeApproveGoal(ctx, req)

	if outcome.Verdict == VerdictPass {
		t.Fatal("a stale goal revision was confirmed")
	}
	if outcome.Proof.Status.IsSuccess() {
		t.Fatal("a refused confirmation carries a successful proof")
	}
	// The call is made and declined: the check belongs to Process 03.
	if n := atomic.LoadInt32(&auth.goalApprovals); n != 1 {
		t.Fatalf("the boundary was called %d times", n)
	}
}

// Work actions with no application-layer boundary are unavailable and say
// exactly what is missing, rather than being faked.
func TestUnboundWorkActionsExplainWhatIsMissing(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	// Every Work action the frozen pack declares.
	for _, id := range []ActionID{
		"CTUI-0201", "CTUI-0202", "CTUI-0203", "CTUI-0210", "CTUI-0219",
		"CTUI-0229", "CTUI-0240", "CTUI-0248", "CTUI-0249", "CTUI-0266",
		"CTUI-0317", "CTUI-0318", "CTUI-0319", "CTUI-0320", "CTUI-0321",
	} {
		binding, ok := source.Bindings()[id]
		if !ok {
			t.Fatalf("%s has no binding at all", id)
		}
		if binding.Bound() {
			continue // some may become bound in a later slice
		}
		reason := binding.Gap
		if reason == "" {
			reason = binding.Requires
		}
		if reason == "" {
			t.Fatalf("%s is unavailable but says nothing about why", id)
		}
		// The reason must name a canonical package or service, not just
		// "unavailable" — a user needs to know what would close the gap.
		if len(reason) < 40 {
			t.Fatalf("%s gives too thin a reason: %q", id, reason)
		}

		c := &Confirmation{}
		if err := c.Begin(ctx, binding, ActionRequest{Action: id}); err == nil {
			t.Fatalf("%s began a confirmation despite being unavailable", id)
		}
		if _, err := c.Submit(ctx); err == nil {
			t.Fatalf("%s submitted despite being unavailable", id)
		}
	}

	// None of them reached an authority.
	total := atomic.LoadInt32(&auth.cancels) + atomic.LoadInt32(&auth.executes) +
		atomic.LoadInt32(&auth.startRuns) + atomic.LoadInt32(&auth.goalApprovals) +
		atomic.LoadInt32(&auth.plansCreated)
	if total != 0 {
		t.Fatalf("unavailable Work actions made %d authority calls", total)
	}
}

// Creating a plan proves itself by rereading the current plan.
func TestCreatePlanProvesItself(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	outcome, err := source.executeCreatePlan(ctx,
		ActionRequest{Action: "CTUI-0251", Target: Target{Kind: "goal", ID: "goal-1"}})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if outcome.Verdict != VerdictPass {
		t.Fatalf("creating a plan produced %s: %s", outcome.Verdict, outcome.Detail)
	}
	if n := atomic.LoadInt32(&auth.plansCreated); n != 1 {
		t.Fatalf("the plan service was called %d times", n)
	}
	if !outcome.Proof.Status.IsSuccess() {
		t.Fatal("a created plan carries no proof")
	}
}

// fakeVerifyReader supplies canonical Process 06 shapes to the Verify tests.
type fakeVerifyReader struct {
	session     verification.Session
	sessionErr  error
	attestation verification.CompletionAttestation
	attestErr   error
}

func (f fakeVerifyReader) CurrentVerification(context.Context) (verification.Session, error) {
	return f.session, f.sessionErr
}

func (f fakeVerifyReader) Attestation(context.Context, string) (verification.CompletionAttestation, error) {
	if f.attestErr != nil {
		return verification.CompletionAttestation{}, f.attestErr
	}
	return f.attestation, nil
}

// --- regressions from the Slice 5 adversarial review ---

// An assessment that produced no checks established nothing, and must not
// default to READY. An earlier version only guarded the case where the
// timestamp was also zero, so a real assessment yielding no checks passed.
func TestZeroCheckAssessmentDoesNotDefaultToReady(t *testing.T) {
	snap := testWork(t, fakeWorkReader{assessment: startup.Assessment{
		// Assessed, but nothing was actually checked.
		AssessedAt: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Phase:      startup.Phase("READY"),
	}}).ReadWork(context.Background())

	if snap.Readiness.Status.IsSuccess() {
		t.Fatalf("a zero-check assessment reported %q", snap.Readiness.Display())
	}
	if snap.Readiness.Status != TruthNotRun {
		t.Fatalf("a zero-check assessment reported %s, want NOT_RUN",
			snap.Readiness.Status.Label())
	}
	if strings.Contains(strings.ToUpper(snap.Readiness.Display()), "READY") {
		t.Fatalf("a zero-check assessment rendered as %q", snap.Readiness.Display())
	}
}

// With real checks and none blocking, readiness restates the canonical phase
// rather than inventing a verdict of its own.
func TestReadinessRestatesTheCanonicalPhase(t *testing.T) {
	snap := testWork(t, fakeWorkReader{assessment: startup.Assessment{
		AssessedAt: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Phase:      startup.Phase("READY"),
		Checks: []startup.Check{{
			ID: "core.store", Dimension: startup.DimensionCore,
			Status: startup.StatusReady, Summary: "the store opened",
		}},
	}}).ReadWork(context.Background())

	if !snap.Readiness.Status.IsSuccess() {
		t.Fatalf("a clean assessment reported %s", snap.Readiness.Status.Label())
	}
	// The count is shown, so "READY" is never a bare claim.
	if !strings.Contains(snap.Readiness.Display(), "1 checks") {
		t.Fatalf("readiness does not say what was checked: %q", snap.Readiness.Display())
	}
}

// The readiness reader must surface its own unavailability rather than
// silently reading as an error forever.
func TestUnavailableAssessmentIsReportedAsSuch(t *testing.T) {
	snap := testWork(t, fakeWorkReader{
		assessErr: errors.New("readiness is assessed when MARSHAL starts"),
	}).ReadWork(context.Background())

	if snap.Readiness.Status.IsSuccess() {
		t.Fatal("an unavailable assessment reported success")
	}
	if snap.Readiness.Reason == "" {
		t.Fatal("an unavailable assessment gives no reason")
	}
}
