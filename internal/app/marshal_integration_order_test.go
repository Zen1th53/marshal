package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

// integrationOrderFixture is marshalFixtureAt with n tasks whose
// dependencies are deps (none by default) and, when shared, one shared file.
func integrationOrderFixture(t *testing.T, n int, dbPath string, deps map[string][]string, shared bool) (*MarshalService, string) {
	t.Helper()
	s, repo := marshalFixtureAt(t, n, dbPath)
	draft := s.Model.(marshalFakeModel).draft
	for i := range draft.Tasks {
		id := draft.Tasks[i].PlanTaskID
		draft.Tasks[i].DependsOn = append([]string(nil), deps[id]...)
		draft.Plan.Tasks[i].DependsOn = append([]string(nil), deps[id]...)
		if shared {
			draft.Tasks[i].Files = []string{"README.md"}
			draft.Tasks[i].Checks = []marshal.Check{{Command: "test -f README.md", Criteria: draft.Tasks[i].Criteria}}
			draft.Plan.Tasks[i].Paths = []string{"README.md"}
			draft.Plan.Checks[id] = []string{"test -f README.md"}
		}
	}
	draft.Plan.Graph, _ = plan.BuildGraph(draft.Plan.Tasks)
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	if shared {
		s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			return nil, os.WriteFile(filepath.Join(req.Worktree, "README.md"), []byte(req.Task.PlanTaskID+"\n"), 0600)
		}}
	}
	if _, err := s.StartPlanning(t.Context(), "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	return s, repo
}

func handInTask(t *testing.T, s *MarshalService, id string) {
	t.Helper()
	d, err := s.Dispatch(t.Context(), "run", id, "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(t.Context(), "run", d); err != nil {
		t.Fatal(err)
	}
}

func acceptTask(t *testing.T, s *MarshalService, id string) {
	t.Helper()
	handInTask(t, s, id)
	if v, err := s.Review(t.Context(), "run", id, knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review %s: %s %v", id, v, err)
	}
}

func loadRun(t *testing.T, s *MarshalService) marshal.Run {
	t.Helper()
	run, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func taskState(t *testing.T, s *MarshalService, id string) marshal.TaskState {
	t.Helper()
	run := loadRun(t, s)
	return run.Tasks[taskIndex(run, id)].State
}

func integrationRef() string { return "refs/heads/" + statepath.RefName + "/run/integration" }

func integrationDir(s *MarshalService, run marshal.Run) string {
	return filepath.Join(s.Worktrees, integrationTaskID("run", run))
}

func requireRollingMerge(t *testing.T) {
	t.Helper()
	if mergeInPlanOrder(marshal.Run{}) {
		t.Skip("this edition merges in plan order")
	}
}

// An accepted task merges while an earlier, unrelated task is still in
// flight, and the task that depends on it can then start.
func TestRollingMergeUnblocksIndependentDependency(t *testing.T) {
	requireRollingMerge(t)
	s, repo := integrationOrderFixture(t, 3, filepath.Join(t.TempDir(), "state.db"), map[string][]string{"c": {"b"}}, false)
	ctx := t.Context()
	handInTask(t, s, "a") // the slow task: handed in, not yet reviewed
	acceptTask(t, s, "b")
	if _, err := s.Dispatch(ctx, "run", "c", "write"); err == nil {
		t.Fatal("c started before its dependency merged")
	}
	progressed, err := s.mergeAccepted(ctx, "run", loadRun(t, s))
	if err != nil || !progressed {
		t.Fatalf("merge: %v %v", progressed, err)
	}
	if got := taskState(t, s, "b"); got != marshal.Merged {
		t.Fatalf("b = %s, want merged before slow a", got)
	}
	if got := taskState(t, s, "a"); got != marshal.HandedIn {
		t.Fatalf("a = %s", got)
	}
	run := loadRun(t, s)
	if !marshalDepsMerged(run, run.Tasks[taskIndex(run, "c")]) {
		t.Fatal("c still waits on b")
	}
	headB := marshalGit(t, repo, "rev-parse", integrationRef())
	acceptTask(t, s, "c")
	run = loadRun(t, s)
	if base := run.Tasks[taskIndex(run, "c")].BaseCommit; base != headB {
		t.Fatalf("c base %s, want integration head %s", base, headB)
	}
	if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review a: %s %v", v, err)
	}
	if _, err := s.mergeAccepted(ctx, "run", loadRun(t, s)); err != nil {
		t.Fatal(err)
	}
	run = loadRun(t, s)
	for _, tk := range run.Tasks {
		if tk.State != marshal.Merged {
			t.Fatalf("%s = %s", tk.PlanTaskID, tk.State)
		}
	}
	if run.State != marshal.Verifying {
		t.Fatalf("run = %s", run.State)
	}
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		if _, err := os.Stat(filepath.Join(integrationDir(s, run), f)); err != nil {
			t.Fatalf("integration checkout lacks %s: %v", f, err)
		}
	}
}

// A run that merges in plan order never merges a later task first.
func TestBatchRetainsPlanOrder(t *testing.T) {
	s, _ := integrationOrderFixture(t, 2, filepath.Join(t.TempDir(), "state.db"), nil, false)
	ctx := t.Context()
	handInTask(t, s, "a")
	acceptTask(t, s, "b")
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if !mergeInPlanOrder(run) {
		// The serial governed batch keeps plan order in this edition.
		run.Process05Bound, run.Process05Parallel = true, false
		if err := s.save(ctx, "run", run, rev); err != nil {
			t.Fatal(err)
		}
		run = loadRun(t, s)
	}
	if !mergeInPlanOrder(run) {
		t.Fatal("batch run does not merge in plan order")
	}
	if progressed, err := s.mergeAccepted(ctx, "run", run); err != nil || progressed {
		t.Fatalf("merged past unaccepted a: %v %v", progressed, err)
	}
	if err := s.Merge(ctx, "run", "b"); err == nil || !strings.Contains(err.Error(), "merge order violation") {
		t.Fatalf("merge b first: %v", err)
	}
	if got := taskState(t, s, "b"); got != marshal.Accepted {
		t.Fatalf("b = %s", got)
	}
}

// A merge that cannot apply returns the task and leaves the integration
// branch, its checkout and files exactly as they were.
func TestMergeConflictLeavesVerifiedHeadUnchanged(t *testing.T) {
	s, repo := integrationOrderFixture(t, 2, filepath.Join(t.TempDir(), "state.db"), nil, true)
	ctx := t.Context()
	acceptTask(t, s, "a")
	acceptTask(t, s, "b")
	if err := s.Merge(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}
	run := loadRun(t, s)
	dir := integrationDir(s, run)
	head := marshalGit(t, repo, "rev-parse", integrationRef())
	if err := s.Merge(ctx, "run", "b"); err != nil {
		t.Fatal(err)
	}
	run = loadRun(t, s)
	if run.Operation != nil {
		t.Fatal("refused merge left a journal")
	}
	if got := run.Tasks[1]; got.State != marshal.Returned || got.BaseCommit != head {
		t.Fatalf("b = %+v", got)
	}
	if got := marshalGit(t, repo, "rev-parse", integrationRef()); got != head {
		t.Fatalf("integration moved %s -> %s", head, got)
	}
	if got := marshalGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("checkout moved to %s", got)
	}
	if status := marshalGit(t, dir, "status", "--porcelain"); status != "" {
		t.Fatalf("checkout changed: %s", status)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(body) != "a\n" {
		t.Fatalf("README = %q", body)
	}
}

// Verification and close run on the final integration head after merges in
// dependency order, and close delivers exactly that head.
func TestCloseChecksRunOnFinalIntegrationHead(t *testing.T) {
	s, repo := integrationOrderFixture(t, 2, filepath.Join(t.TempDir(), "state.db"), nil, false)
	ctx := t.Context()
	var verified []string
	verify := s.Verify
	s.Verify = func(ctx context.Context, run marshal.Run, head string) (verification.Session, verification.Binding, error) {
		verified = append(verified, head)
		return verify(ctx, run, head)
	}
	acceptTask(t, s, "a")
	acceptTask(t, s, "b")
	order := []string{"b", "a"}
	if mergeInPlanOrder(loadRun(t, s)) {
		order = []string{"a", "b"}
	}
	for _, id := range order {
		if err := s.Merge(ctx, "run", id); err != nil {
			t.Fatal(err)
		}
	}
	final := marshalGit(t, repo, "rev-parse", integrationRef())
	if got := marshalGit(t, integrationDir(s, loadRun(t, s)), "rev-parse", "HEAD"); got != final {
		t.Fatalf("checkout %s, branch %s", got, final)
	}
	if result, err := s.VerifyMerged(ctx, "run", knownCharge()); err != nil || result != verification.VerifiedComplete {
		t.Fatalf("verify: %s %v", result, err)
	}
	marshalGit(t, repo, "checkout", "--detach")
	if err := s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if len(verified) == 0 {
		t.Fatal("verification did not run")
	}
	for _, head := range verified {
		if head != final {
			t.Fatalf("verified %s, final head %s", head, final)
		}
	}
	if got := deliveredCommit(t, s, "run"); got != final {
		t.Fatalf("delivered %s, want %s", got, final)
	}
	if got := marshalGit(t, repo, "rev-parse", "refs/heads/main"); got != final {
		t.Fatalf("main = %s, want %s", got, final)
	}
}

// When another merge advanced the integration head, the approved checks
// and a fresh review run on the restaged candidate before it is published,
// and a candidate that fails them is never published.
func TestMergeRevalidatesEvidenceWhenHeadAdvances(t *testing.T) {
	requireRollingMerge(t)
	for _, failing := range []bool{false, true} {
		name := "passes"
		if failing {
			name = "fails"
		}
		t.Run(name, func(t *testing.T) {
			s, repo := integrationOrderFixture(t, 2, filepath.Join(t.TempDir(), "state.db"), nil, false)
			ctx := t.Context()
			var mu sync.Mutex
			var rechecked, reviewed []string
			s.GovernedCheck = func(ctx context.Context, runID, task, worker, dir, command string) marshal.CommandRecord {
				if marshalGit(t, dir, "log", "-1", "--format=%s") == "marshal: merge a" {
					mu.Lock()
					rechecked = append(rechecked, marshalGit(t, dir, "rev-parse", "HEAD"))
					mu.Unlock()
					if failing {
						return marshal.CommandRecord{Command: command, ExitCode: 1, Output: "fails on the advanced head"}
					}
				}
				return fixtureCheckRunner(ctx, runID, task, worker, dir, command)
			}
			cross := s.CrossReview
			s.CrossReview = func(ctx context.Context, tk marshal.Task, h marshal.HandIn, c marshal.Control) (marshal.Review, string, error) {
				reviewed = append(reviewed, h.ResultCommit)
				return cross(ctx, tk, h, c)
			}
			acceptTask(t, s, "a")
			acceptTask(t, s, "b")
			if err := s.Merge(ctx, "run", "b"); err != nil {
				t.Fatal(err)
			}
			if len(rechecked) != 0 {
				t.Fatal("a merge onto its own base was rechecked")
			}
			headB := marshalGit(t, repo, "rev-parse", integrationRef())
			reviewsBefore := len(reviewed)
			if err := s.Merge(ctx, "run", "a"); err != nil {
				t.Fatal(err)
			}
			head := marshalGit(t, repo, "rev-parse", integrationRef())
			if failing {
				if head != headB || taskState(t, s, "a") != marshal.Returned {
					t.Fatalf("failing candidate published: head %s, a %s", head, taskState(t, s, "a"))
				}
				return
			}
			if taskState(t, s, "a") != marshal.Merged {
				t.Fatalf("a = %s", taskState(t, s, "a"))
			}
			if len(rechecked) == 0 {
				t.Fatal("approved checks did not rerun on the advanced head")
			}
			for _, got := range rechecked {
				if got != head {
					t.Fatalf("rechecked %s, published %s", got, head)
				}
			}
			if len(reviewed) != reviewsBefore+1 || reviewed[len(reviewed)-1] != head {
				t.Fatalf("fresh review %v, published %s", reviewed[reviewsBefore:], head)
			}
		})
	}
}

// A dependency whose result is no longer the one the attempt was built on
// makes the attempt stale: it returns and the integration does not move.
func TestMergeRejectsChangedDependencyHead(t *testing.T) {
	requireRollingMerge(t)
	s, repo := integrationOrderFixture(t, 3, filepath.Join(t.TempDir(), "state.db"), map[string][]string{"b": {"a"}}, false)
	ctx := t.Context()
	acceptTask(t, s, "a")
	handInTask(t, s, "c")
	if err := s.Merge(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}
	acceptTask(t, s, "b")
	if v, err := s.Review(ctx, "run", "c", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review c: %s %v", v, err)
	}
	if err := s.Merge(ctx, "run", "c"); err != nil {
		t.Fatal(err)
	}
	head := marshalGit(t, repo, "rev-parse", integrationRef())
	// The dependency's result is replaced by a commit the attempt never saw.
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	other := marshalGit(t, repo, "commit-tree", run.BaseCommit+"^{tree}", "-p", run.BaseCommit, "-m", "other a")
	run.Tasks[taskIndex(run, "a")].ResultCommit = other
	if err := s.save(ctx, "run", run, rev); err != nil {
		t.Fatal(err)
	}
	if err := s.Merge(ctx, "run", "b"); err != nil {
		t.Fatal(err)
	}
	run = loadRun(t, s)
	b := run.Tasks[taskIndex(run, "b")]
	if b.State != marshal.Returned {
		t.Fatalf("b = %s", b.State)
	}
	reasons, err := s.taskReturnReasons(ctx, "run", b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(reasons, "\n"), "stale_base: dependency a changed") {
		t.Fatalf("reasons %v", reasons)
	}
	if got := marshalGit(t, repo, "rev-parse", integrationRef()); got != head {
		t.Fatalf("integration moved %s -> %s", head, got)
	}
}

// restartService reopens the database under a new service, so recovery
// uses only durable state.
func restartService(t *testing.T, s *MarshalService, dbPath string) *MarshalService {
	t.Helper()
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	restarted := *s
	restarted.Store = db
	restarted.AfterLifecycleEffect = nil
	return &restarted
}

func mergedEvents(t *testing.T, s *MarshalService) int {
	t.Helper()
	history, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range history {
		if e.Type == events.EventTypeMarshalTaskMerged {
			n++
		}
	}
	return n
}

// A merge interrupted at each boundary after its journal is reconciled
// from the staged candidate: a clean candidate acknowledges once, a clean
// checkout at the expected head publishes it, and a partial or competing
// checkout stays held until repaired. Verification and close then consume
// the exact candidate from the integration checkout.
func TestMergeIntentRefAndReceiptCrashWindows(t *testing.T) {
	for _, boundary := range []string{"before-state-write", "before-fast-forward", "ref-moved-old-index", "competing-checkout"} {
		t.Run(boundary, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "state.db")
			s, repo := integrationOrderFixture(t, 1, dbPath, nil, false)
			ctx := t.Context()
			acceptTask(t, s, "a")
			interrupted := errors.New("injected interruption after merge effect")
			s.AfterLifecycleEffect = func(kind, _ string) error {
				if kind == "merge" {
					return interrupted
				}
				return nil
			}
			if err := s.Merge(ctx, "run", "a"); !errors.Is(err, interrupted) {
				t.Fatalf("fault not reached: %v", err)
			}
			run := loadRun(t, s)
			if run.Operation == nil || run.Operation.Merge == nil {
				t.Fatal("merge intent was not journaled")
			}
			in := *run.Operation.Merge
			dir := integrationDir(s, run)
			if in.Worktree != dir || in.Candidate == "" || in.ExpectedHead != run.BaseCommit {
				t.Fatalf("intent %+v", in)
			}
			if got := marshalGit(t, repo, "rev-parse", in.StagingRef); got != in.Candidate {
				t.Fatalf("staging ref %s", got)
			}
			held := false
			switch boundary {
			case "before-fast-forward":
				marshalGit(t, dir, "reset", "--hard", in.ExpectedHead)
			case "ref-moved-old-index":
				marshalGit(t, dir, "reset", "--hard", in.ExpectedHead)
				marshalGit(t, repo, "update-ref", integrationRef(), in.Candidate)
				held = true
			case "competing-checkout":
				if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other\n"), 0600); err != nil {
					t.Fatal(err)
				}
				marshalGit(t, dir, "add", "other.txt")
				marshalGit(t, dir, "commit", "-m", "competing")
				held = true
			}
			s = restartService(t, s, dbPath)
			if held {
				if _, err := s.Resume(ctx, "run"); !errors.Is(err, ErrUnfinishedLifecycleOperation) {
					t.Fatalf("held merge acknowledged: %v", err)
				}
				if run := loadRun(t, s); run.Operation == nil || run.Tasks[0].State != marshal.Accepted {
					t.Fatalf("journal not held: %+v", run.Operation)
				}
				if _, err := s.VerifyMerged(ctx, "run", knownCharge()); err == nil {
					t.Fatal("verification ran on a held merge")
				}
				if got := marshalGit(t, repo, "rev-parse", in.StagingRef); got != in.Candidate {
					t.Fatal("staged candidate was not preserved")
				}
				// Repair the checkout to the candidate; recovery then rechecks it.
				marshalGit(t, dir, "reset", "--hard", in.Candidate)
				s = restartService(t, s, dbPath)
			}
			run, err := s.Resume(ctx, "run")
			if err != nil {
				t.Fatal(err)
			}
			if run.Operation != nil || run.Tasks[0].State != marshal.Merged || run.State != marshal.Verifying {
				t.Fatalf("recovered run %s %s op=%v", run.State, run.Tasks[0].State, run.Operation != nil)
			}
			if n := mergedEvents(t, s); n != 1 {
				t.Fatalf("merge acknowledged %d times", n)
			}
			if got := marshalGit(t, repo, "rev-parse", integrationRef()); got != in.Candidate {
				t.Fatalf("integration %s, candidate %s", got, in.Candidate)
			}
			if _, err := s.Resume(ctx, "run"); err != nil {
				t.Fatal(err)
			}
			if n := mergedEvents(t, s); n != 1 {
				t.Fatalf("merge acknowledged %d times after a second resume", n)
			}
			var verified string
			verify := s.Verify
			s.Verify = func(ctx context.Context, run marshal.Run, head string) (verification.Session, verification.Binding, error) {
				verified = head
				return verify(ctx, run, head)
			}
			if result, err := s.VerifyMerged(ctx, "run", knownCharge()); err != nil || result != verification.VerifiedComplete {
				t.Fatalf("verify: %s %v", result, err)
			}
			if verified != in.Candidate {
				t.Fatalf("verified %s, candidate %s", verified, in.Candidate)
			}
			marshalGit(t, repo, "checkout", "--detach")
			if err := s.Close(ctx, "run"); err != nil {
				t.Fatal(err)
			}
			if got := deliveredCommit(t, s, "run"); got != in.Candidate {
				t.Fatalf("delivered %s, candidate %s", got, in.Candidate)
			}
		})
	}
}
