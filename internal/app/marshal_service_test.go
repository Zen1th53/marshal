package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

type marshalFakeModel struct {
	draft  MarshalDraft
	review marshal.Review
	amend  MarshalDraft
}

func (m marshalFakeModel) Draft(context.Context, string) (MarshalDraft, error) { return m.draft, nil }
func (m marshalFakeModel) Review(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, error) {
	return m.review, nil
}
func (m marshalFakeModel) Amend(context.Context, marshal.Run, string) (MarshalDraft, error) {
	return m.amend, nil
}

func marshalGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func TestStartPlanningFromDraftUsesModelValidation(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	draft := s.Model.(marshalFakeModel).draft
	run, err := s.StartPlanningFromDraft(t.Context(), "interactive", "goal", draft, marshal.Budget{})
	if err != nil || run.State != marshal.Drafting {
		t.Fatalf("valid draft: state=%s err=%v", run.State, err)
	}
	s2, _ := marshalFixture(t, 1)
	bad := s2.Model.(marshalFakeModel).draft
	bad.Tasks[0].Worker = ""
	if _, err := s2.StartPlanningFromDraft(t.Context(), "invalid", "goal", bad, marshal.Budget{}); err == nil {
		t.Fatal("invalid draft accepted")
	}
	if _, _, err := s2.load(t.Context(), "invalid"); err == nil {
		t.Fatal("invalid draft started a run")
	}
}

func TestMarshalDraftCannotReplaceApprovedScopeWithDuplicate(t *testing.T) {
	if sameStrings([]string{"a", "a"}, []string{"a", "b"}) {
		t.Fatal("duplicate entries masked a missing approved value")
	}
	if !sameStrings([]string{"b", "a", "a"}, []string{"a", "b", "a"}) {
		t.Fatal("equivalent scopes were rejected")
	}
}

func TestMarshalGovernedTaskDoesNotLaunchNativeDriver(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	s.Drivers["worker"] = driver.Codex("false")
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(ctx, "run", "a", "write"); err == nil || !strings.Contains(err.Error(), "task requires governed") {
		t.Fatalf("governed task was not refused before launch: %v", err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil || run.Tasks[0].State != marshal.Queued {
		t.Fatalf("failed dispatch changed task state: %+v %v", run.Tasks[0], err)
	}
}

func marshalFixture(t *testing.T, n int, databasePath ...string) (*MarshalService, string) {
	t.Helper()
	ctx := context.Background()
	repo := t.TempDir()
	marshalGit(t, repo, "init", "-b", "main")
	marshalGit(t, repo, "config", "user.name", "Zen1th53")
	marshalGit(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, repo, "add", "README.md")
	marshalGit(t, repo, "commit", "-m", "base")
	dbPath := filepath.Join(t.TempDir(), "state.db")
	if len(databasePath) > 0 {
		dbPath = databasePath[0]
	}
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	const projectID = "PROJECT-0123456789abcdef0123456789abcdef"
	if err = db.InitProject(ctx, model.Project{ID: projectID, Repository: repo, DefaultBranch: "main", PackVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	tasks := make([]plan.Task, n)
	mt := make([]marshal.Task, n)
	checks := map[string][]string{}
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		file := id + ".txt"
		criterion := "file " + id + " exists"
		command := "test -f " + file
		tasks[i] = plan.Task{ID: id, Title: "write " + file, Criteria: []string{criterion}, Paths: []string{file}, Mutating: true, Weight: 1}
		mt[i] = marshal.Task{PlanTaskID: id, Worker: "worker", Mode: marshal.Governed, Checks: []marshal.Check{{Command: command, Criteria: []string{criterion}}}, Files: []string{file}, Criteria: []string{criterion}}
		checks[id] = []string{command}
		if i > 0 {
			tasks[i].DependsOn = []string{string(rune('a' + i - 1))}
			mt[i].DependsOn = append([]string(nil), tasks[i].DependsOn...)
		}
	}
	graph, err := plan.BuildGraph(tasks)
	if err != nil {
		t.Fatal(err)
	}
	p := plan.ExecutionPlan{ID: "PLAN-test", ProjectID: projectid.ID(projectID), Goal: plan.GoalBinding{GoalID: "GOAL-test", Revision: 1}, Version: 1, State: plan.StateReady, Mode: plan.ModeStandard, ConstitutionVersion: constitution.Current, Tasks: tasks, Checks: checks, Graph: graph}
	s := &MarshalService{Store: db, ProjectID: projectID, Repository: repo, Worktrees: filepath.Join(t.TempDir(), "worktrees"), Reviewer: "marshal", Model: marshalFakeModel{draft: MarshalDraft{Plan: p, Tasks: mt}, review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}}
	s.GovernedCheck = fixtureCheckRunner
	s.ApprovalActor = func(context.Context, string, string) (string, error) { return "user", nil }
	s.GateState = func(context.Context, string, string) (constitution.RuntimeState, error) {
		return constitution.RuntimeState{SandboxAvailable: true, NetworkEnforced: true, AuthorizedActor: true, EvidencePresent: true, EvidenceFresh: true, HarnessGovernance: constitution.GovernanceVerified}, nil
	}
	s.Drivers = map[string]driver.Driver{"worker": driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		file := filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt")
		return nil, os.WriteFile(file, []byte("done\n"), 0600)
	}}}
	s.Verify = func(_ context.Context, run marshal.Run, head string) (verification.Session, verification.Binding, error) {
		b := verification.Binding{ProjectID: projectID, GoalID: "GOAL-test", PlanID: run.PlanID, RunID: "run", GoalRevision: 1, PlanVersion: run.PlanVersion, RunVersion: 1, TreeDigest: head, EnvironmentDigest: "test"}
		now := time.Now().UTC()
		return verification.Session{ID: "verification", Version: 1, Binding: b, RequiredChecks: map[string]verification.Status{"integration": verification.StatusPass}, CreatedAt: now, UpdatedAt: now}, b, nil
	}
	return s, repo
}
func knownCharge() marshal.Charge {
	return marshal.Charge{Tokens: marshal.Amount{Value: 1, Known: true}, Money: marshal.Amount{Value: 1, Known: true}, WallTime: time.Second}
}

func TestM09HappyPathTwoTasksVerifyClose(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 2)
	run, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.Drafting {
		t.Fatal(run.State)
	}
	run, err = s.Approve(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.Approved {
		t.Fatal(run.State)
	}
	for _, id := range []string{"a", "b"} {
		d, err := s.Dispatch(ctx, "run", id, "write file")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
			t.Fatal(err)
		}
		v, err := s.Review(ctx, "run", id, knownCharge())
		if err != nil || v != marshal.VerdictAccept {
			t.Fatalf("review %s: %s %v", id, v, err)
		}
		if err = s.Merge(ctx, "run", id); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.VerifyMerged(ctx, "run", knownCharge())
	if err != nil || result != verification.VerifiedComplete {
		t.Fatalf("verify: %s %v", result, err)
	}
	marshalGit(t, s.Repository, "checkout", "--detach")
	if err = s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.load(ctx, "run")
	if err != nil || got.State != marshal.Closed {
		t.Fatalf("close: %+v %v", got, err)
	}
	target := marshalGit(t, repo, "rev-parse", "refs/heads/main")
	integrated := marshalGit(t, repo, "rev-parse", "refs/heads/marshal/run/integration")
	if target != integrated {
		t.Fatal("target was not fast-forwarded")
	}
}

func TestM09ReturnTwiceReassignThenEscalate(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	draft := s.Model.(marshalFakeModel).draft
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "marshal"}}
	for attempt := 0; attempt < 2; attempt++ {
		d, err := s.Dispatch(ctx, "run", "a", "write")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
			t.Fatal(err)
		}
		v, err := s.Review(ctx, "run", "a", knownCharge())
		if err != nil {
			t.Fatal(err)
		}
		want := marshal.VerdictReturn
		if attempt == 1 {
			want = marshal.VerdictReassign
		}
		if v != want {
			t.Fatalf("attempt %d: %s", attempt, v)
		}
	}
	s.Drivers["other"] = driver.Governed{Provider: "other", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("rework\n"), 0600)
	}}
	if err := s.Reassign(ctx, "run", "a", "other"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "rework")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	v, err := s.Review(ctx, "run", "a", knownCharge())
	if err != nil || v != marshal.VerdictEscalate {
		t.Fatalf("result %s %v", v, err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil || run.State != marshal.AwaitingUser || run.Tasks[0].State != marshal.Escalated {
		t.Fatalf("state %+v %v", run, err)
	}
}

func TestM09MajorAmendmentPausesDispatch(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	original := s.Model.(marshalFakeModel).draft
	changed := original
	changed.Plan.Budget.MaxTasks = 2
	s.Model = marshalFakeModel{draft: original, amend: changed}
	run, err := s.Amend(ctx, "run", "increase budget")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.Drafting {
		t.Fatal(run.State)
	}
	if _, err = s.Dispatch(ctx, "run", "a", "write"); err == nil {
		t.Fatal("dispatch continued during amendment")
	}
	if _, err = s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Dispatch(ctx, "run", "a", "write"); err != nil {
		t.Fatal(err)
	}
}

func TestMajorAmendmentProposalDoesNotMutateBeforeApproval(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	approved, err := s.Approve(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	original := s.Model.(marshalFakeModel).draft
	changed := original
	changed.Plan.Budget.MaxTasks = 2
	s.Model = marshalFakeModel{draft: original, amend: changed}
	draft, major, err := s.ProposeAmend(ctx, "run", "increase budget")
	if err != nil || !major {
		t.Fatalf("proposal major=%v err=%v", major, err)
	}
	current, _, err := s.load(ctx, "run")
	if err != nil || current.PlanVersion != approved.PlanVersion || current.State != marshal.Approved {
		t.Fatalf("proposal changed the run: %+v %v", current, err)
	}
	if _, err := s.ApplyAmendDraftBound(ctx, "run", "increase budget", draft, approved.PlanVersion+1); err == nil {
		t.Fatal("stale proposal was applied")
	}
	current, err = s.ApplyAmendDraftBound(ctx, "run", "increase budget", draft, approved.PlanVersion)
	if err != nil || current.State != marshal.Drafting {
		t.Fatalf("approved proposal not staged for plan approval: %+v %v", current, err)
	}
}

func TestMarshalAmendmentRefusesExecutionModeChange(t *testing.T) {
	for _, mode := range []marshal.WorkerMode{marshal.Native, marshal.Governed} {
		for _, split := range []bool{false, true} {
			t.Run(string(mode)+fmt.Sprint(split), func(t *testing.T) {
				ctx := t.Context()
				s, _ := marshalFixture(t, 1)
				original := s.Model.(marshalFakeModel).draft
				original.Tasks[0].Mode = mode
				s.Model = marshalFakeModel{draft: original}
				if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
					t.Fatal(err)
				}
				approved, err := s.Approve(ctx, "run")
				if err != nil {
					t.Fatal(err)
				}
				changed := original
				changed.Tasks = append([]marshal.Task(nil), original.Tasks...)
				changed.Plan.Routes = map[string]plan.Route{"a": {Provider: "test", Governance: constitution.GovernanceVerified}}
				changed.Tasks[0].Mode = marshal.Native
				if mode == marshal.Native {
					changed.Tasks[0].Mode = marshal.Governed
				}
				if split {
					changed.Tasks[0].PlanTaskID = "child"
					changed.Plan.Tasks = append([]plan.Task(nil), original.Plan.Tasks...)
					changed.Plan.Tasks[0].ID = "child"
					changed.Plan.Checks = map[string][]string{"child": original.Plan.Checks["a"]}
					changed.Plan.ParentTaskIDs = map[string]string{"child": "a"}
					changed.Plan.Routes = map[string]plan.Route{"child": {Provider: "test", Governance: constitution.GovernanceVerified}}
					changed.Plan.Assignments = plan.AssignmentPlan{Assignments: []plan.Assignment{{Tasks: []string{"child"}, Harness: "test"}}}
					changed.Plan.Graph, err = plan.BuildGraph(changed.Plan.Tasks)
					if err != nil {
						t.Fatal(err)
					}
				}
				currentPlan, err := s.Store.GetPlan(ctx, approved.PlanID, approved.PlanVersion)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := currentPlan.AmendScoped(currentPlan.Version, "replace worker", changed.Plan); err != nil {
					t.Fatalf("plan amendment must preserve the other approved boundaries: %v", err)
				}
				s.Model = marshalFakeModel{draft: original, amend: changed}
				if _, _, err := s.ProposeAmend(ctx, "run", "change worker mode"); err == nil {
					t.Fatal("mode change proposal accepted")
				}
				if _, err := s.ApplyAmendDraftBound(ctx, "run", "change worker mode", changed, approved.PlanVersion); err == nil {
					t.Fatal("mode change applied with existing approval")
				}
				current, err := s.Snapshot(ctx, "run")
				if err != nil || current.PlanVersion != approved.PlanVersion || current.ApprovalScopeDigest != approved.ApprovalScopeDigest || current.Tasks[0].Mode != mode {
					t.Fatalf("refused amendment changed run: %+v err=%v", current, err)
				}
				changedRun := approved
				changedRun.Tasks = changed.Tasks
				if marshalApprovalDigest("plan", approved) == marshalApprovalDigest("plan", changedRun) {
					t.Fatal("approval digest does not bind worker mode")
				}
				changed.Tasks[0].Mode = mode
				s.Model = marshalFakeModel{amend: changed}
				if _, major, err := s.ProposeAmend(ctx, "run", "replace worker"); err != nil || major {
					t.Fatalf("amendment preserving mode refused: major=%v err=%v", major, err)
				}
			})
		}
	}
}

func TestM09MergeConflictReturnsAtIntegrationHead(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 2)
	draft := s.Model.(marshalFakeModel).draft
	// Independent tasks that change the same file: a dependent task starts
	// from the integration head and could not conflict with its dependency.
	for i := range draft.Tasks {
		draft.Tasks[i].Files = []string{"README.md"}
		draft.Tasks[i].Checks = []marshal.Check{{Command: "test -f README.md", Criteria: draft.Tasks[i].Criteria}}
		draft.Tasks[i].DependsOn = nil
		draft.Plan.Tasks[i].Paths = []string{"README.md"}
		draft.Plan.Tasks[i].DependsOn = nil
		draft.Plan.Checks[draft.Tasks[i].PlanTaskID] = []string{"test -f README.md"}
	}
	draft.Plan.Graph, _ = plan.BuildGraph(draft.Plan.Tasks)
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		return nil, os.WriteFile(filepath.Join(req.Worktree, "README.md"), []byte(req.Task.PlanTaskID+"\n"), 0600)
	}}
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		d, err := s.Dispatch(ctx, "run", id, "write")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
			t.Fatal(err)
		}
		v, err := s.Review(ctx, "run", id, knownCharge())
		if err != nil || v != marshal.VerdictAccept {
			t.Fatalf("review: %s %v", v, err)
		}
		if err = s.Merge(ctx, "run", id); err != nil {
			t.Fatal(err)
		}
	}
	run, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	head := marshalGit(t, repo, "rev-parse", "refs/heads/marshal/run/integration")
	if run.Tasks[1].State != marshal.Returned || run.Tasks[1].BaseCommit != head {
		t.Fatalf("conflict state %+v", run.Tasks[1])
	}
	if got := marshalGit(t, repo, "rev-parse", "refs/heads/marshal/run/b"); got == head {
		t.Fatal("conflicting task was rebased")
	}
}

func TestM09ResumeFromStore(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	restarted := *s
	run, err := restarted.Resume(ctx, "run")
	if err != nil || run.Tasks[0].State != marshal.HandedIn {
		t.Fatalf("resume %+v %v", run, err)
	}
}

func TestM09PlanBudgetTokensPausesRun(t *testing.T) {
	testM09PlanBudget(t, marshal.Budget{Tokens: marshal.Ceiling{Plan: 1}}, marshal.Charge{Tokens: marshal.Amount{Value: 2, Known: true}, Money: marshal.Amount{Known: true}})
}
func TestM09PlanBudgetWallTimePausesRun(t *testing.T) {
	testM09PlanBudget(t, marshal.Budget{WallTime: marshal.Ceiling{Plan: 1}}, marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}, WallTime: 2 * time.Second})
}
func TestM09PlanBudgetMoneyPausesRun(t *testing.T) {
	testM09PlanBudget(t, marshal.Budget{Money: marshal.Ceiling{Plan: 1}}, marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Value: 2, Known: true}})
}
func testM09PlanBudget(t *testing.T, budget marshal.Budget, charge marshal.Charge) {
	t.Helper()
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write", budget); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Review(ctx, "run", "a", charge); err != nil {
		t.Fatal(err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil || run.State != marshal.AwaitingUser {
		t.Fatalf("budget state %+v %v", run, err)
	}
}
func TestM09TaskBudgetReturnsAndRunContinues(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{WallTime: marshal.Ceiling{Task: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	v, err := s.Review(ctx, "run", "a", marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}, WallTime: 2 * time.Second})
	if err != nil || v != marshal.VerdictReturn {
		t.Fatalf("review %s %v", v, err)
	}
	run, _, _ := s.load(ctx, "run")
	if run.Tasks[0].State != marshal.Returned || run.State == marshal.AwaitingUser {
		t.Fatalf("state %+v", run)
	}
}
func TestM09NativeUnknownUsagePausesWithUnknownUsage(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	draft := s.Model.(marshalFakeModel).draft
	draft.Tasks[0].Mode = marshal.Native
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	s.Drivers["worker"] = driver.Native{Provider: "fake", Binary: "sh", Args: func(driver.Request) []string { return []string{"-c", "printf done > a.txt"} }, Parse: func([]byte) []marshal.CommandRecord { return nil }}
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{Tokens: marshal.Ceiling{Task: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	verdict, err := s.Review(ctx, "run", "a", marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}})
	if err != nil || verdict != marshal.VerdictAccept {
		t.Fatalf("unknown usage changed the verdict: %s %v", verdict, err)
	}
	run, err := s.Snapshot(ctx, "run")
	if err != nil || run.State != marshal.AwaitingUser || run.Tasks[0].State != marshal.HandedIn {
		t.Fatalf("unknown usage did not pause without returning the task: %+v %v", run, err)
	}
	history, err := s.Store.MarshalDecisions(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range history {
		if event.Type == events.EventTypeMarshalUsageCharged && event.Data["charge_phase"] == "dispatch" && event.Data["usage_known"] == false {
			found = true
		}
	}
	if !found {
		t.Fatal("native unknown usage was not recorded")
	}
}

func TestM09CloseUsesProjectBranchOnDetachedHEAD(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review %s %v", v, err)
	}
	if err = s.Merge(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.VerifyMerged(ctx, "run", knownCharge()); err != nil || v != verification.VerifiedComplete {
		t.Fatalf("verify %s %v", v, err)
	}
	marshalGit(t, repo, "checkout", "--detach")
	if err = s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if marshalGit(t, repo, "rev-parse", "refs/heads/main") != marshalGit(t, repo, "rev-parse", "refs/heads/marshal/run/integration") {
		t.Fatal("default branch did not advance")
	}
}

func TestM09InvalidModelDraftNeverStartsRun(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	draft := s.Model.(marshalFakeModel).draft
	draft.Tasks[0].Checks[0].Command = "true"
	s.Model = marshalFakeModel{draft: draft}
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err == nil {
		t.Fatal("invalid model checks reached the store")
	}
	if _, _, err := s.load(ctx, "run"); err == nil {
		t.Fatal("invalid run was persisted")
	}
}

func TestM09StandingCloseAuthorization(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	settings := marshal.DefaultSettings()
	settings.AcceptanceMode = marshal.AcceptMarshal
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	run, err := s.Approve(ctx, "run")
	if err != nil || !run.ValidCloseAuthorization() {
		t.Fatalf("standing authorization %+v %v", run.CloseAuthorization, err)
	}
	s.ApprovalActor = nil
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review %s %v", v, err)
	}
	if err = s.Merge(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.VerifyMerged(ctx, "run", knownCharge()); err != nil || v != verification.VerifiedComplete {
		t.Fatalf("verify %s %v", v, err)
	}
	marshalGit(t, s.Repository, "checkout", "--detach")
	if err = s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
}

func TestM09HeadlessModelOutputAndResumeID(t *testing.T) {
	cases := []struct{ provider, output, id string }{
		{"claude", `{"session_id":"claude-1","structured_output":{"Verdict":"accept","Reviewer":"marshal"}}`, "claude-1"},
		{"agy", `{"result":{"conversation_id":"agy-1","response":"{\"Verdict\":\"accept\",\"Reviewer\":\"marshal\"}"}}`, "agy-1"},
		{"codex", "{\"type\":\"thread.started\",\"thread_id\":\"codex-1\"}\n{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"{\\\"Verdict\\\":\\\"accept\\\",\\\"Reviewer\\\":\\\"marshal\\\"}\"}}\n", "codex-1"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			payload, id, err := marshalCLIOutput(tc.provider, []byte(tc.output))
			if err != nil || id != tc.id {
				t.Fatalf("output %s %v", id, err)
			}
			var review marshal.Review
			if err = json.Unmarshal(payload, &review); err != nil || review.Verdict != marshal.VerdictAccept {
				t.Fatalf("review %+v %v", review, err)
			}
		})
	}
}

func TestM09ResumeInterruptedCleanDispatch(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) { return nil, nil }}
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(ctx, "run", "a", "write"); err != nil {
		t.Fatal(err)
	}
	restarted := *s
	run, err := restarted.Resume(ctx, "run")
	if err != nil || run.Tasks[0].State != marshal.Returned {
		t.Fatalf("resume %+v %v", run, err)
	}
	restarted.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("done"), 0600)
	}}
	if _, err = restarted.Dispatch(ctx, "run", "a", "retry"); err != nil {
		t.Fatal(err)
	}
}

type marshalTestGate bool

func (g marshalTestGate) Capability(name string) bool {
	return bool(g) && name == marshal.CapabilityMarshal
}
func TestM09UltraDispatchRequiresCrossReviewAndVerifier(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	s.Gate = marshalTestGate(true)
	s.ModelProvider = "model"
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Review(ctx, "run", "a", knownCharge()); err == nil {
		t.Fatal("ULTRA review without cross-review passed")
	}
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{"check:test -f a.txt"}}, "other", nil
	}
	if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review %s %v", v, err)
	}
	if err = s.Merge(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyMerged(ctx, "run", knownCharge()); err == nil {
		t.Fatal("ULTRA verified without independent verifier")
	}
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "verifier", nil }
	s.IndependentVerify = func(_ context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
		if session.Binding.TreeDigest != head {
			return marshal.VerifierEvidence{}, errors.New("verifier saw a different commit")
		}
		return marshal.VerifierEvidence{Reviewer: "verifier", Provider: "verifier", Commit: head, Verdict: "pass", InputDigest: verifierInputDigest(run, head, session)}, nil
	}
	if v, err := s.VerifyMerged(ctx, "run", knownCharge()); err != nil || v != verification.VerifiedComplete {
		t.Fatalf("verify %s %v", v, err)
	}
}

func TestM09UltraRejectedCrossReviewReturnsTask(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	s.Gate = marshalTestGate(true)
	s.ModelProvider = "model"
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "second", Reasons: []string{"check failed"}, EvidenceRefs: []string{"check:test -f a.txt"}}, "other", nil
	}
	if verdict, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || verdict != marshal.VerdictReturn {
		t.Fatalf("rejected cross-review: verdict=%s err=%v", verdict, err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil || run.Tasks[0].State != marshal.Returned || run.Tasks[0].ReturnsByAgent[run.Tasks[0].Worker] != 1 {
		t.Fatalf("task did not enter rework: run=%+v err=%v", run, err)
	}
	storedReview, err := s.Store.GetMarshalReview(ctx, "run", "a", 1)
	if err != nil || storedReview.Value.Independent == nil || storedReview.Value.Independent.Verdict != marshal.VerdictReturn || len(storedReview.Value.Independent.Reasons) != 1 {
		t.Fatalf("independent reasons were not retained with the review: %+v %v", storedReview.Value, err)
	}
	history, err := s.Store.MarshalDecisions(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range history {
		if event.Type == events.EventTypeMarshalTaskReturned && event.TaskID == "a" {
			found = event.Data["cross_review_verdict"] == string(marshal.VerdictReturn) && event.Data["cross_review_provider"] == "other"
		}
	}
	if !found {
		t.Fatal("cross-review rejection was not recorded with the task return")
	}
}

// The service tests inject workers and checks. Keep executing their check
// fixtures and preserve all result/cleanliness assertions; production wiring
// supplies the observed sandbox instead.
func fixtureCheckRunner(ctx context.Context, _, _, _, dir, command string) marshal.CommandRecord {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = -1
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
	}
	return marshal.CommandRecord{Command: command, ExitCode: code, Output: string(out)}
}
