package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestMarshalWiredUltraNeedsRealIndependentVerifier(t *testing.T) {
	fixture, _ := marshalFixture(t, 1)
	runtime := &Runtime{store: fixture.Store, layout: project.Layout{Root: fixture.Repository, Worktrees: fixture.Worktrees}}
	service, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.requireIndependentVerifier(t.Context(), marshal.Run{Tier: marshal.Ultra}); err == nil {
		t.Fatal("production wiring treated runtime check reruns as an independent agent")
	}
}

// wire replaces the fixture's gate state and verifier with the production
// ones, so the run is judged only by what the runtime observes. The fixture
// worker's harness is recorded as probed, with evidence, at the installed
// version, which is what makes it governed.
func wire(t *testing.T, s *MarshalService) {
	t.Helper()
	s.GateState = s.observedGateState
	s.Verify = s.verifyByChecks
	s.InstalledVersion = func(context.Context, string) string { return "1.0.0" }
	profile := model.HarnessProfile{Harness: "worker", InstalledVersion: "1.0.0", ProbeEvidenceID: "evidence-probe-1", ProbedAt: time.Now().UTC()}
	if err := s.Store.SaveHarnessProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
}

// A worker whose harness has no probe evidence is not launched: its work
// could not be accepted, so the task is escalated with the reason.
func TestM09UngovernedWorkerEscalatesBeforeDispatch(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 1)
	s.GateState = s.observedGateState
	s.Verify = s.verifyByChecks
	s.InstalledVersion = func(context.Context, string) string { return "1.0.0" }
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	run, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.AwaitingUser || run.Tasks[0].State != marshal.Escalated {
		t.Fatalf("run %s task %s, want awaiting_user/escalated", run.State, run.Tasks[0].State)
	}
	if out := marshalGit(t, repo, "branch", "--list", "marshal/run/a"); out != "" {
		t.Fatalf("an ungoverned worker was dispatched: %q", out)
	}
}

// A task the reviewer keeps returning moves to a different worker after the
// rework limit, and escalates when that worker fails too; the loop ends.
func TestM09ReassignMovesToAnotherWorkerThenEscalates(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	fake := s.Model.(marshalFakeModel)
	fake.review = marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "marshal", Reasons: []string{"not good enough"}}
	s.Model = fake
	s.Drivers["other"] = s.Drivers["worker"]
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	var workers []string
	run, err := s.Execute(ctx, "run", marshalBrief, func(r marshal.Run) { workers = append(workers, r.Tasks[0].Worker) })
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.AwaitingUser || run.Tasks[0].State != marshal.Escalated {
		t.Fatalf("run %s task %s, want awaiting_user/escalated", run.State, run.Tasks[0].State)
	}
	if run.Tasks[0].Worker != "other" {
		t.Fatalf("worker = %s; the task was never reassigned (saw %v)", run.Tasks[0].Worker, workers)
	}
}

// With the production gate state and check-based verification, a run whose
// work passes its approved checks at the integrated head closes.
func TestM09WiredRunClosesOnObservedState(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 2)
	wire(t, s)
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	run, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.Verifying {
		t.Fatalf("state = %s, want verifying", run.State)
	}
	marshalGit(t, repo, "checkout", "--detach")
	if err := s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if marshalGit(t, repo, "rev-parse", "refs/heads/main") != marshalGit(t, repo, "rev-parse", "refs/heads/marshal/run/integration") {
		t.Fatal("target was not fast-forwarded")
	}
}

// Verification re-runs the approved checks at the integrated head; one that
// fails there makes the result incomplete.
func TestM09WiredVerificationFailsOnIntegratedCheck(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	wire(t, s)
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	run, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err != nil || run.State != marshal.Verifying {
		t.Fatalf("execute: %s %v", run.State, err)
	}
	head := marshalGit(t, filepath.Join(s.Worktrees, "TASK-run-integration"), "rev-parse", "HEAD")
	session, binding, err := s.verifyByChecks(ctx, run, head)
	if err != nil || verification.Evaluate(session, binding, s.clock()) != verification.VerifiedComplete {
		t.Fatalf("passing checks: %v %v", verification.Evaluate(session, binding, s.clock()), err)
	}
	run.Tasks[0].Checks = append(run.Tasks[0].Checks, marshal.Check{Command: "test -f never-created.txt", Criteria: []string{"never"}})
	session, binding, err = s.verifyByChecks(ctx, run, head)
	if err != nil {
		t.Fatal(err)
	}
	if got := verification.Evaluate(session, binding, s.clock()); got == verification.VerifiedComplete {
		t.Fatal("a failing integrated check must not verify complete")
	}
}

// A check that commits leaves a clean tree but moves HEAD; verification must
// fail rather than bind the passing result to a commit that is not the one
// being closed.
func TestM09WiredVerificationFailsWhenCheckMovesHead(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	wire(t, s)
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	run, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err != nil || run.State != marshal.Verifying {
		t.Fatalf("execute: %s %v", run.State, err)
	}
	head := marshalGit(t, filepath.Join(s.Worktrees, "TASK-run-integration"), "rev-parse", "HEAD")
	run.Tasks[0].Checks = append(run.Tasks[0].Checks, marshal.Check{Command: "git -c user.name=x -c user.email=x@example.invalid commit -q --allow-empty -m moved", Criteria: []string{"moves"}})
	session, binding, err := s.verifyByChecks(ctx, run, head)
	if err != nil {
		t.Fatal(err)
	}
	if verification.Evaluate(session, binding, s.clock()) == verification.VerifiedComplete {
		t.Fatal("a check that moved HEAD still verified complete")
	}
}

// A hand-in that adds a merge driver through .gitattributes is detected
// before any merge runs; a commented line does not count.
func TestM09DetectsMergeDriverInHandIn(t *testing.T) {
	_, repo := marshalFixture(t, 1)
	marshalGit(t, repo, "checkout", "-q", "-b", "marshal/run/a")
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("# * merge=ignored\n*.txt text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, repo, "add", ".gitattributes")
	marshalGit(t, repo, "commit", "-q", "-m", "comment only")
	marshalGit(t, repo, "checkout", "-q", "main")
	sets, err := marshalSetsMergeDriver(context.Background(), repo, "marshal/run/a")
	if err != nil || sets {
		t.Fatalf("commented attribute: sets=%v err=%v", sets, err)
	}
	marshalGit(t, repo, "checkout", "-q", "marshal/run/a")
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("* merge=worker-driver\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, repo, "commit", "-q", "-am", "adds a merge driver")
	marshalGit(t, repo, "checkout", "-q", "main")
	sets, err = marshalSetsMergeDriver(context.Background(), repo, "marshal/run/a")
	if err != nil || !sets {
		t.Fatalf("merge driver attribute: sets=%v err=%v", sets, err)
	}
}

// Before the user approves the plan, the runtime is not an authorized actor,
// and the state never claims control MARSHAL does not have.
func TestM09ObservedGateStateNeedsApproval(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	state, err := s.observedGateState(ctx, "run", "a")
	if err != nil {
		t.Fatal(err)
	}
	if state.AuthorizedActor {
		t.Fatal("an unapproved run must not make the runtime an authorized actor")
	}
	if state.HarnessGovernance.Governed() || state.HarnessGovernance == "" || state.SandboxAvailable || state.NetworkEnforced {
		t.Fatalf("state claims control MARSHAL does not have: %+v", state)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if state, err = s.observedGateState(ctx, "run", "a"); err != nil || !state.AuthorizedActor {
		t.Fatalf("approved run: %+v %v", state, err)
	}
}
