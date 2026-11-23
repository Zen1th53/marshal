package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/statepath"
)

func commitFileOnBranch(t *testing.T, repo, branch, file, content, msg string) string {
	t.Helper()
	wt := filepath.Join(resolvedTempDir(t), "advance-"+strings.NewReplacer("/", "-", " ", "-").Replace(branch))
	if _, err := gitMarshal(t.Context(), repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		// If the branch is checked out in repo itself, commit there directly;
		// otherwise attach a temporary worktree.
		if current, err := gitMarshal(t.Context(), repo, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil && current == branch {
			full := filepath.Join(repo, file)
			if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			marshalGit(t, repo, "add", file)
			marshalGit(t, repo, "commit", "-m", msg)
			return marshalGit(t, repo, "rev-parse", "HEAD")
		}
		marshalGit(t, repo, "worktree", "add", wt, branch)
	} else {
		marshalGit(t, repo, "worktree", "add", "-b", branch, wt, "HEAD")
	}
	defer marshalGit(t, repo, "worktree", "remove", "--force", wt)
	full := filepath.Join(wt, file)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, wt, "add", file)
	marshalGit(t, wt, "commit", "-m", msg)
	return marshalGit(t, wt, "rev-parse", "HEAD")
}

func commitInWorktree(t *testing.T, dir, file, content, msg string) string {
	t.Helper()
	full := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, dir, "add", file)
	marshalGit(t, dir, "commit", "-m", msg)
	return marshalGit(t, dir, "rev-parse", "HEAD")
}

func assertStaleBaseRefusal(t *testing.T, err error, wantEffects marshal.RefusalEffects, substrings ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected stale_base refusal, got nil")
	}
	if !errors.Is(err, ErrStaleBase) {
		t.Fatalf("expected ErrStaleBase, got %v", err)
	}
	receipt, ok := marshal.RefusalOf(err)
	if !ok {
		t.Fatalf("expected typed marshal refusal receipt, got %T %v", err, err)
	}
	if receipt.Code != marshal.RefusalCodeStaleBase {
		t.Fatalf("refusal code = %q, want %q", receipt.Code, marshal.RefusalCodeStaleBase)
	}
	if wantEffects != "" && receipt.Effects != wantEffects {
		t.Fatalf("refusal effects = %q, want %q", receipt.Effects, wantEffects)
	}
	if len(receipt.NextCommands) == 0 {
		t.Fatalf("refusal missing next commands: %+v", receipt)
	}
	var admErr *DispatchAdmissionError
	if !errors.As(err, &admErr) || admErr.Admission.Code != AdmissionStaleBase {
		t.Fatalf("expected DispatchAdmissionError(%s), got %v", AdmissionStaleBase, err)
	}
	var baseErr *LaunchBaseRefusalError
	if !errors.As(err, &baseErr) {
		t.Fatalf("expected LaunchBaseRefusalError, got %T %v", err, err)
	}
	text := err.Error()
	for _, sub := range substrings {
		if !strings.Contains(text, sub) {
			t.Fatalf("refusal %q missing expected detail %q", text, sub)
		}
	}
}

func TestFirstDispatchRejectsStaleApprovedBase(t *testing.T) {
	t.Run("disjoint commit on approved delivery target refuses first dispatch", func(t *testing.T) {
		ctx := t.Context()
		s, repo := marshalFixture(t, 2)
		settings := marshal.DefaultSettings()
		settings.AcceptanceMode = marshal.AcceptMarshal
		if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings, 0); err != nil {
			t.Fatal(err)
		}
		var launched atomic.Int32
		s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			launched.Add(1)
			return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0o600)
		}}
		if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		approved, err := s.Approve(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		// Complete task a first so we also verify retained evidence is preserved
		// when task b's first dispatch meets a stale delivery target.
		dA, err := s.Dispatch(ctx, "run", "a", "write a")
		if err != nil {
			t.Fatal(err)
		}
		hA, err := s.CollectHandIn(ctx, "run", dA)
		if err != nil {
			t.Fatal(err)
		}
		if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
			t.Fatalf("review a: %s %v", v, err)
		}
		if err := s.Merge(ctx, "run", "a"); err != nil {
			t.Fatal(err)
		}
		beforeRun, beforeRev, err := s.load(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		launchesBefore := launched.Load()

		// Advance the approved delivery target (refs/heads/main) with a completely
		// disjoint commit. Any target change must yield stale_base.
		newMain := commitFileOnBranch(t, repo, "main", "disjoint.txt", "unrelated\n", "disjoint advance on main")

		_, err = s.Dispatch(ctx, "run", "b", "write b")
		assertStaleBaseRefusal(t, err, marshal.EffectsNone, "refs/heads/main", approved.BaseCommit, newMain)

		if launched.Load() != launchesBefore {
			t.Fatalf("refused stale dispatch launched worker process: %d -> %d", launchesBefore, launched.Load())
		}
		bWorktree := filepath.Join(s.Worktrees, taskArtifactID("run", beforeRun.Tasks[1]))
		if _, statErr := os.Stat(bWorktree); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("refused stale dispatch created task worktree %s (err=%v)", bWorktree, statErr)
		}
		if _, refErr := gitMarshal(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+beforeRun.Tasks[1].Branch); refErr == nil {
			t.Fatalf("refused stale dispatch created task branch %s", beforeRun.Tasks[1].Branch)
		}

		// Approval binding, close authorization, base commit, task state, and retained evidence are preserved.
		afterRun, afterRev, err := s.load(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		if afterRev != beforeRev || afterRun.ApprovalScopeDigest != beforeRun.ApprovalScopeDigest || afterRun.BaseCommit != approved.BaseCommit || afterRun.Tasks[1].State != marshal.Queued {
			t.Fatalf("stale refusal mutated run or approval: before=%+v (rev %d) after=%+v (rev %d)", beforeRun, beforeRev, afterRun, afterRev)
		}
		if !afterRun.ValidCloseAuthorization() {
			t.Fatalf("stale refusal voided standing close authorization: %+v", afterRun.CloseAuthorization)
		}
		retainedHandIn, err := s.Store.GetMarshalHandIn(ctx, "run", "a", 1)
		if err != nil || retainedHandIn.Value.ResultCommit != hA.ResultCommit {
			t.Fatalf("retained evidence for task a lost: %+v %v", retainedHandIn, err)
		}
	})

	t.Run("overlap, broad scope, wrong repository, and tampered scope are refused", func(t *testing.T) {
		ctx := t.Context()

		// 1. Overlapping advance on private integration (touches a.txt when a owns a.txt).
		sOverlap, repoOverlap := marshalFixture(t, 1)
		if _, err := sOverlap.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		runOverlap, err := sOverlap.Approve(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		intBranch := integrationBranch("run", runOverlap)
		marshalGit(t, repoOverlap, "branch", intBranch, runOverlap.BaseCommit)
		commitFileOnBranch(t, repoOverlap, intBranch, "a.txt", "overlapping change\n", "overlap on integration")
		if _, err := sOverlap.Dispatch(ctx, "run", "a", "write"); err == nil {
			t.Fatal("overlapping integration advance was admitted")
		} else {
			assertStaleBaseRefusal(t, err, marshal.EffectsNone, "overlaps")
		}
		if _, err := os.Stat(filepath.Join(sOverlap.Worktrees, taskArtifactID("run", runOverlap.Tasks[0]))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("overlapping refusal created worktree: %v", err)
		}

		// 2. Broad file scope cannot prove disjointness on private integration advance.
		sBroad, repoBroad := marshalFixture(t, 1)
		draft := sBroad.Model.(marshalFakeModel).draft
		draft.Tasks[0].Files = []string{"src/**"}
		draft.Plan.Tasks[0].Paths = []string{"src/**"}
		g, err := plan.BuildGraph(draft.Plan.Tasks)
		if err != nil {
			t.Fatal(err)
		}
		draft.Plan.Graph = g
		sBroad.Model = marshalFakeModel{draft: draft, review: sBroad.Model.(marshalFakeModel).review}
		if _, err := sBroad.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		runBroad, err := sBroad.Approve(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		broadInt := integrationBranch("run", runBroad)
		marshalGit(t, repoBroad, "branch", broadInt, runBroad.BaseCommit)
		commitFileOnBranch(t, repoBroad, broadInt, "disjoint.txt", "other\n", "disjoint commit")
		if _, err := sBroad.Dispatch(ctx, "run", "a", "write"); err == nil {
			t.Fatal("broad-scope task admitted on advanced integration")
		} else {
			assertStaleBaseRefusal(t, err, marshal.EffectsNone, "broad task file scope")
		}

		// 3. Governing/reserved file change on private integration is refused.
		sGov, repoGov := marshalFixture(t, 1)
		if _, err := sGov.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		runGov, err := sGov.Approve(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		govInt := integrationBranch("run", runGov)
		marshalGit(t, repoGov, "branch", govInt, runGov.BaseCommit)
		commitFileOnBranch(t, repoGov, govInt, "AGENTS.md", "policy change\n", "governing file change")
		if _, err := sGov.Dispatch(ctx, "run", "a", "write"); err == nil {
			t.Fatal("governing file advance on integration was admitted")
		} else {
			assertStaleBaseRefusal(t, err, marshal.EffectsNone, "governing or reserved")
		}

		// 4. Wrong repository is refused.
		sRepo, _ := marshalFixture(t, 1)
		if _, err := sRepo.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		if _, err := sRepo.Approve(ctx, "run"); err != nil {
			t.Fatal(err)
		}
		other, _ := marshalFixture(t, 1)
		sRepo.Repository = other.Repository
		if _, err := sRepo.Dispatch(ctx, "run", "a", "write"); err == nil {
			t.Fatal("wrong repository was admitted")
		} else {
			assertStaleBaseRefusal(t, err, marshal.EffectsNone)
		}
	})

	t.Run("legacy run derives demonstrable binding and refuses missing binding", func(t *testing.T) {
		ctx := t.Context()
		s, repo := marshalFixture(t, 1)
		if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Approve(ctx, "run"); err != nil {
			t.Fatal(err)
		}
		run, rev, err := s.load(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
		if err != nil {
			t.Fatal(err)
		}
		// Simulate an unversioned legacy run created before Repository/TargetRef were stored.
		run.Compatibility = nil
		run.Repository = ""
		run.TargetRef = ""
		run.CloseAuthorization = nil
		run.ApprovalScopeDigest = marshalApprovalDigest(p.ApprovalScopeDigest, run)
		if err := s.save(ctx, "run", run, rev); err != nil {
			t.Fatal(err)
		}
		// Demonstrable legacy binding is usable while delivery ref matches run.BaseCommit.
		if _, err := s.AdmitLaunchBase(ctx, "run", run, run.Tasks[0], nil); err != nil {
			t.Fatalf("demonstrable legacy run refused: %v", err)
		}
		// Advancing main refuses the legacy run with stale_base.
		commitFileOnBranch(t, repo, "main", "legacy-disjoint.txt", "moved\n", "move main")
		if _, err := s.Dispatch(ctx, "run", "a", "write"); err == nil {
			t.Fatal("legacy run with moved delivery ref was admitted")
		} else {
			assertStaleBaseRefusal(t, err, marshal.EffectsNone, "refs/heads/main")
		}

		// Legacy run without demonstrable approval binding asks for approved recovery.
		sBad, _ := marshalFixture(t, 1)
		if _, err := sBad.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		if _, err := sBad.Approve(ctx, "run"); err != nil {
			t.Fatal(err)
		}
		badRun, badRev, err := sBad.load(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		badRun.Compatibility = nil
		badRun.Repository = ""
		badRun.TargetRef = ""
		badRun.CloseAuthorization = nil
		badRun.ApprovalScopeDigest = "sha256:undemonstrable-legacy-digest"
		if err := sBad.save(ctx, "run", badRun, badRev); err != nil {
			t.Fatal(err)
		}
		if _, err := sBad.Dispatch(ctx, "run", "a", "write"); err == nil {
			t.Fatal("undemonstrable legacy run was admitted")
		} else {
			assertStaleBaseRefusal(t, err, marshal.EffectsNone, "recovery")
		}
	})
}

func TestFirstDispatchAcceptsProvenDisjointAdvance(t *testing.T) {
	ctx := t.Context()
	s, repo := marshalUserCloseFixture(t, 2)

	var briefBase, worktreeHead, taskBase string
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if req.Task.PlanTaskID == "b" {
			taskBase = req.Task.BaseCommit
			head, err := gitMarshal(ctx, req.Worktree, "rev-parse", "HEAD")
			if err != nil {
				return nil, err
			}
			worktreeHead = head
			if _, err := os.Stat(filepath.Join(req.Worktree, "a.txt")); err != nil {
				return nil, errors.New("merged dependency a.txt missing from worktree")
			}
			if _, err := os.Stat(filepath.Join(req.Worktree, "disjoint.txt")); err != nil {
				return nil, errors.New("disjoint private integration commit missing from worktree")
			}
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0o600)
	}}

	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	approved, err := s.Approve(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}

	// Run and merge task a onto the private integration branch.
	dA, err := s.Dispatch(ctx, "run", "a", "write a")
	if err != nil {
		t.Fatal(err)
	}
	hA, err := s.CollectHandIn(ctx, "run", dA)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review a: %s %v", v, err)
	}
	if err := s.Merge(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}

	// Apply a disjoint change on private integration ONLY while refs/heads/main
	// stays exactly at approved.BaseCommit.
	integrationDir := filepath.Join(s.Worktrees, integrationTaskID("run", approved))
	disjointIntegrationHead := commitInWorktree(t, integrationDir, "disjoint.txt", "private integration only\n", "disjoint private integration advance")
	if got := marshalGit(t, repo, "rev-parse", "refs/heads/main"); got != approved.BaseCommit {
		t.Fatalf("main moved unexpectedly: %s != %s", got, approved.BaseCommit)
	}

	// Verify AdmitLaunchBase pins delivery ref/SHA, private integration head,
	// admitted base, and exact merged dependency heads separately.
	runBeforeB, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	pin, err := s.AdmitLaunchBase(ctx, "run", runBeforeB, runBeforeB.Tasks[1], nil)
	if err != nil {
		t.Fatalf("proven disjoint integration advance refused: %v", err)
	}
	if pin.DeliveryRef != "refs/heads/main" || pin.DeliveryCommit != approved.BaseCommit || pin.ApprovedBaseCommit != approved.BaseCommit {
		t.Fatalf("delivery pin mismatch: %+v", pin)
	}
	if pin.IntegrationHead != disjointIntegrationHead || pin.AdmittedBaseCommit != disjointIntegrationHead || !pin.DisjointAdvanceAdmitted {
		t.Fatalf("integration pin mismatch: %+v want head %s", pin, disjointIntegrationHead)
	}
	if pin.MergedDependencyHeads["a"] != hA.ResultCommit {
		t.Fatalf("merged dependency pin = %v, want a=%s", pin.MergedDependencyHeads, hA.ResultCommit)
	}

	// Dispatch b via dispatchWithBrief with a builder to verify brief and worktree
	// share the exact same pinned integration base.
	dB, err := s.dispatchWithBrief(ctx, "run", "b", "", func(task marshal.Task, _ BriefContext) string {
		briefBase = task.BaseCommit
		return "write b on " + task.BaseCommit
	})
	if err != nil {
		t.Fatalf("first dispatch on proven disjoint private integration failed: %v", err)
	}
	if _, err := s.CollectHandIn(ctx, "run", dB); err != nil {
		t.Fatal(err)
	}
	if briefBase != disjointIntegrationHead || taskBase != disjointIntegrationHead || worktreeHead != disjointIntegrationHead {
		t.Fatalf("brief/task/worktree bases diverged: brief=%s task=%s worktree=%s want=%s", briefBase, taskBase, worktreeHead, disjointIntegrationHead)
	}
	if v, err := s.Review(ctx, "run", "b", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review b: %s %v", v, err)
	}
	if err := s.Merge(ctx, "run", "b"); err != nil {
		t.Fatal(err)
	}

	// Exercise close's final target recheck when the delivery target advances
	// after admission.
	movedMain := commitFileOnBranch(t, repo, "main", "late-main.txt", "advanced after admission\n", "advance main after admission")
	err = s.Close(ctx, "run")
	if err == nil {
		t.Fatal("close succeeded after delivery target advanced post-admission")
	}
	var stop *DeliveryStopError
	if !errors.As(err, &stop) || !strings.Contains(err.Error(), "approved delivery base changed") {
		t.Fatalf("expected DeliveryStopError for changed delivery base, got %v", err)
	}
	if got := marshalGit(t, repo, "rev-parse", "refs/heads/main"); got != movedMain {
		t.Fatalf("refused close moved delivery ref: %s != %s", got, movedMain)
	}
	finalRun, err := s.Snapshot(ctx, "run")
	if err != nil || finalRun.State != marshal.Verifying {
		t.Fatalf("expected run to remain verifying after refused close: %+v %v", finalRun, err)
	}

	// Also verify a first task (index 0, no dependencies) admits a proven disjoint
	// private integration branch advance and merges/closes cleanly when main stays put.
	sSolo, repoSolo := marshalUserCloseFixture(t, 1)
	if _, err := sSolo.StartPlanning(ctx, "solo", "write a", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	soloRun, err := sSolo.Approve(ctx, "solo")
	if err != nil {
		t.Fatal(err)
	}
	soloInt := integrationBranch("solo", soloRun)
	marshalGit(t, repoSolo, "branch", soloInt, soloRun.BaseCommit)
	soloIntHead := commitFileOnBranch(t, repoSolo, soloInt, "other.txt", "disjoint solo\n", "disjoint advance on solo integration")
	dSolo, err := sSolo.Dispatch(ctx, "solo", "a", "write a")
	if err != nil {
		t.Fatalf("task 0 disjoint private integration dispatch failed: %v", err)
	}
	hSolo, err := sSolo.CollectHandIn(ctx, "solo", dSolo)
	if err != nil {
		t.Fatal(err)
	}
	if hSolo.BaseCommit != soloIntHead {
		t.Fatalf("solo hand-in base = %s, want pinned integration head %s", hSolo.BaseCommit, soloIntHead)
	}
	if v, err := sSolo.Review(ctx, "solo", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review solo: %s %v", v, err)
	}
	if err := sSolo.Merge(ctx, "solo", "a"); err != nil {
		t.Fatalf("merge solo onto pre-existing disjoint integration failed: %v", err)
	}
	if err := sSolo.Close(ctx, "solo"); err != nil {
		t.Fatalf("close solo with unchanged main failed: %v", err)
	}
}

func TestLaunchBaseRetainsRelaunchBaseAfterDisjointAdvance(t *testing.T) {
	ctx := t.Context()
	s, repo := marshalUserCloseFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write a", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	run, err := s.Approve(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	task := run.Tasks[0]
	saved, err := s.AdmitLaunchBase(ctx, "run", run, task, nil)
	if err != nil {
		t.Fatal(err)
	}
	branch := integrationBranch("run", run)
	marshalGit(t, repo, "branch", branch, run.BaseCommit)
	commitFileOnBranch(t, repo, branch, "other.txt", "disjoint\n", "advance integration")
	for _, state := range []marshal.TaskState{marshal.Queued, marshal.Dispatched, marshal.Returned} {
		task.State = state
		pin, err := s.AdmitLaunchBase(ctx, "run", run, task, &saved)
		if err != nil {
			t.Fatal(err)
		}
		if pin.AdmittedBaseCommit != saved.AdmittedBaseCommit {
			t.Fatalf("%s relaunch base changed: %s != %s", state, pin.AdmittedBaseCommit, saved.AdmittedBaseCommit)
		}
	}
}

func TestFirstDispatchRefreshesBaseAfterOverlappingMergedTask(t *testing.T) {
	ctx := t.Context()
	s, repo := marshalUserCloseFixture(t, 2)
	fake := s.Model.(marshalFakeModel)
	for i := range fake.draft.Tasks {
		fake.draft.Tasks[i].Files = []string{"a.txt", "b.txt"}
		fake.draft.Tasks[i].DependsOn = nil
		fake.draft.Plan.Tasks[i].Paths = []string{"a.txt", "b.txt"}
		fake.draft.Plan.Tasks[i].DependsOn = nil
	}
	fake.draft.Plan.Graph, _ = plan.BuildGraph(fake.draft.Plan.Tasks)
	s.Model = fake
	var briefBase, workerBase, workerHead string
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if req.Task.PlanTaskID == "b" {
			workerBase = req.Task.BaseCommit
			var err error
			workerHead, err = gitMarshal(ctx, req.Worktree, "rev-parse", "HEAD")
			if err != nil {
				return nil, err
			}
			if _, err := os.Stat(filepath.Join(req.Worktree, "a.txt")); err != nil {
				return nil, err
			}
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0o600)
	}}
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	approved, err := s.Approve(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	oldPin, err := s.AdmitLaunchBase(ctx, "run", before, before.Tasks[1], nil)
	if err != nil {
		t.Fatal(err)
	}
	dA, err := s.Dispatch(ctx, "run", "a", "write a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(ctx, "run", dA); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review a: %s %v", v, err)
	}
	if err := s.Merge(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	head, _, err := s.observeIntegrationHead(ctx, "run", run)
	if err != nil {
		t.Fatal(err)
	}
	for _, retained := range []string{"intent", "result", "attempt", "started", "dirty worktree", "moved worktree", "mismatched result", "foreign advance"} {
		t.Run(retained, func(t *testing.T) {
			checkRun := run
			checkRun.Tasks = append([]marshal.Task(nil), run.Tasks...)
			task := checkRun.Tasks[1]
			var expected *marshal.LaunchBasePin
			switch retained {
			case "intent":
				expected = &oldPin
			case "result":
				task.ResultCommit = approved.BaseCommit
			case "attempt":
				task.EvidenceAttemptBase = 1
			case "started":
				task.State = marshal.Dispatched
			case "dirty worktree", "moved worktree":
				dir := filepath.Join(s.Worktrees, taskArtifactID("run", task))
				marshalGit(t, repo, "worktree", "add", "--detach", dir, approved.BaseCommit)
				defer marshalGit(t, repo, "worktree", "remove", "--force", dir)
				if retained == "dirty worktree" {
					if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("retained\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					commitInWorktree(t, dir, "b.txt", "retained\n", "retain task output")
				}
			case "mismatched result":
				checkRun.Tasks[0].ResultCommit = approved.BaseCommit
			case "foreign advance":
				checkRun.Tasks[0].PlanTaskID = "other"
			}
			_, err := s.AdmitLaunchBase(ctx, "run", checkRun, task, expected)
			assertStaleBaseRefusal(t, err, marshal.EffectsNone)
		})
	}
	pin, err := s.AdmitLaunchBase(ctx, "run", run, run.Tasks[1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if pin.AdmittedBaseCommit != head || pin.ApprovedBaseCommit != approved.BaseCommit || pin.DeliveryCommit != approved.BaseCommit {
		t.Fatalf("unexpected refreshed pin: %+v", pin)
	}
	dB, err := s.dispatchWithBrief(ctx, "run", "b", "", func(task marshal.Task, _ BriefContext) string {
		briefBase = task.BaseCommit
		return "write b"
	})
	if err != nil {
		t.Fatal(err)
	}
	hB, err := s.CollectHandIn(ctx, "run", dB)
	if err != nil {
		t.Fatal(err)
	}
	if briefBase != head || workerBase != head || workerHead != head || hB.BaseCommit != head {
		t.Fatalf("refreshed bases differ: brief=%s worker=%s head=%s hand-in=%s want=%s", briefBase, workerBase, workerHead, hB.BaseCommit, head)
	}
	if got := marshalGit(t, repo, "rev-parse", "refs/heads/main"); got != approved.BaseCommit {
		t.Fatalf("delivery base moved: %s", got)
	}
}

func TestRecoveredLaunchRejectsChangedDependencyOrBase(t *testing.T) {
	t.Run("saved pre-start intent refuses advanced delivery target with disjoint commit", func(t *testing.T) {
		ctx := t.Context()
		s, repo := marshalFixture(t, 1)
		settings := marshal.DefaultSettings()
		settings.AcceptanceMode = marshal.AcceptMarshal
		if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings, 0); err != nil {
			t.Fatal(err)
		}
		var launched atomic.Int32
		s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			launched.Add(1)
			return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("done\n"), 0o600)
		}}
		if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		approved, err := s.Approve(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		run, rev, err := s.load(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		pin, err := s.AdmitLaunchBase(ctx, "run", run, run.Tasks[0], nil)
		if err != nil {
			t.Fatal(err)
		}
		pin.ProcessStarted = false
		next := run
		next.Tasks = append([]marshal.Task(nil), run.Tasks...)
		next.Tasks[0].State = marshal.Dispatched
		next.State = marshal.Dispatching
		op := &marshal.LifecycleOperation{
			Kind:       "launch",
			TaskID:     "a",
			Dir:        filepath.Join(s.Worktrees, taskArtifactID("run", run.Tasks[0])),
			Before:     pin.DeliveryCommit,
			After:      pin.AdmittedBaseCommit,
			Target:     pin.DeliveryRef,
			LaunchBase: &pin,
			Next:       &next,
			Event:      events.Event{Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"worker": "worker"}},
		}
		if _, err := s.beginOperation(ctx, "run", run, rev, op); err != nil {
			t.Fatal(err)
		}

		// Advance delivery target with a disjoint commit after intent was saved.
		newMain := commitFileOnBranch(t, repo, "main", "disjoint-recovery.txt", "moved\n", "disjoint main advance")

		// Startup recovery records a stale_base pause without creating worktree or process.
		if err := s.RecoverPendingOperations(ctx); err != nil {
			t.Fatal(err)
		}
		paused, err := s.Snapshot(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		if paused.State != marshal.AwaitingUser || paused.Pause == nil || paused.Pause.Code != marshal.CodeStaleBase {
			t.Fatalf("expected stale_base pause after startup recovery, got state=%s pause=%+v", paused.State, paused.Pause)
		}
		if paused.Tasks[0].State != marshal.Queued {
			t.Fatalf("pre-start stale intent transitioned task to %s, want queued", paused.Tasks[0].State)
		}
		if paused.Operation != nil {
			t.Fatalf("pre-start stale intent was retained and would block amend: %+v", paused.Operation)
		}
		if paused.ApprovalScopeDigest != approved.ApprovalScopeDigest || !paused.ValidCloseAuthorization() {
			t.Fatalf("recovery lost approval binding: %+v", paused)
		}

		// Resume and Dispatch both return typed stale_base refusals with no worktree or launch.
		_, resumeErr := s.Resume(ctx, "run")
		assertStaleBaseRefusal(t, resumeErr, marshal.EffectsNone, "refs/heads/main", approved.BaseCommit, newMain)

		_, dispatchErr := s.Dispatch(ctx, "run", "a", "write")
		assertStaleBaseRefusal(t, dispatchErr, marshal.EffectsNone, "refs/heads/main", approved.BaseCommit, newMain)

		if launched.Load() != 0 {
			t.Fatalf("recovered stale launch started worker process (%d times)", launched.Load())
		}
		if _, statErr := os.Stat(op.Dir); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("recovered stale launch created worktree %s (err=%v)", op.Dir, statErr)
		}
	})

	t.Run("saved intent refuses changed dependency head and modified dependency file", func(t *testing.T) {
		ctx := t.Context()
		s, _ := marshalFixture(t, 2)
		if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Approve(ctx, "run"); err != nil {
			t.Fatal(err)
		}
		// Complete and merge dependency a.
		dA, err := s.Dispatch(ctx, "run", "a", "write a")
		if err != nil {
			t.Fatal(err)
		}
		hA, err := s.CollectHandIn(ctx, "run", dA)
		if err != nil {
			t.Fatal(err)
		}
		if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
			t.Fatalf("review a: %s %v", v, err)
		}
		if err := s.Merge(ctx, "run", "a"); err != nil {
			t.Fatal(err)
		}

		// Save a pre-start launch intent for b pinning dependency a's exact merged head.
		run, rev, err := s.load(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		pin, err := s.AdmitLaunchBase(ctx, "run", run, run.Tasks[1], nil)
		if err != nil {
			t.Fatal(err)
		}
		if pin.MergedDependencyHeads["a"] != hA.ResultCommit {
			t.Fatalf("expected dependency pin a=%s, got %+v", hA.ResultCommit, pin.MergedDependencyHeads)
		}
		pin.ProcessStarted = false
		next := run
		next.Tasks = append([]marshal.Task(nil), run.Tasks...)
		next.Tasks[1].State = marshal.Dispatched
		next.State = marshal.Dispatching
		op := &marshal.LifecycleOperation{
			Kind:       "launch",
			TaskID:     "b",
			Dir:        filepath.Join(s.Worktrees, taskArtifactID("run", run.Tasks[1])),
			Before:     pin.DeliveryCommit,
			After:      pin.AdmittedBaseCommit,
			Target:     pin.DeliveryRef,
			LaunchBase: &pin,
			Next:       &next,
			Event:      events.Event{Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"worker": "worker"}},
		}
		rev, err = s.beginOperation(ctx, "run", run, rev, op)
		if err != nil {
			t.Fatal(err)
		}

		// Mutate dependency a's file on private integration (changing dependency output).
		integrationDir := filepath.Join(s.Worktrees, integrationTaskID("run", run))
		commitInWorktree(t, integrationDir, "a.txt", "mutated dependency\n", "modify dependency a file")

		_, err = s.Resume(ctx, "run")
		assertStaleBaseRefusal(t, err, marshal.EffectsNone, "dependency a")

		// Also verify direct changed dependency ResultCommit on saved intent.
		s2, _ := marshalFixture(t, 2)
		if _, err := s2.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		if _, err := s2.Approve(ctx, "run"); err != nil {
			t.Fatal(err)
		}
		dA2, err := s2.Dispatch(ctx, "run", "a", "write a")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s2.CollectHandIn(ctx, "run", dA2); err != nil {
			t.Fatal(err)
		}
		if _, err := s2.Review(ctx, "run", "a", knownCharge()); err != nil {
			t.Fatal(err)
		}
		if err := s2.Merge(ctx, "run", "a"); err != nil {
			t.Fatal(err)
		}
		run2, rev2, err := s2.load(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		pin2, err := s2.AdmitLaunchBase(ctx, "run", run2, run2.Tasks[1], nil)
		if err != nil {
			t.Fatal(err)
		}
		// Alter the saved intent's pinned dependency head to simulate a changed dependency commit.
		pin2.MergedDependencyHeads["a"] = run2.BaseCommit
		pin2.ProcessStarted = false
		op2 := &marshal.LifecycleOperation{
			Kind:       "launch",
			TaskID:     "b",
			Dir:        filepath.Join(s2.Worktrees, taskArtifactID("run", run2.Tasks[1])),
			Before:     pin2.DeliveryCommit,
			After:      pin2.AdmittedBaseCommit,
			Target:     pin2.DeliveryRef,
			LaunchBase: &pin2,
			Next:       &run2,
			Event:      events.Event{Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"worker": "worker"}},
		}
		if _, err := s2.beginOperation(ctx, "run", run2, rev2, op2); err != nil {
			t.Fatal(err)
		}
		_, err = s2.Resume(ctx, "run")
		assertStaleBaseRefusal(t, err, marshal.EffectsNone, "merged dependency heads changed")
	})

	t.Run("already-started stale process holds capacity until verified exit then quarantines output", func(t *testing.T) {
		ctx := t.Context()
		s, repo := marshalFixture(t, 2)
		// Make tasks independent so capacity holding can be checked against task b.
		draft := s.Model.(marshalFakeModel).draft
		draft.Tasks[1].DependsOn = nil
		draft.Plan.Tasks[1].DependsOn = nil
		graph, err := plan.BuildGraph(draft.Plan.Tasks)
		if err != nil {
			t.Fatal(err)
		}
		draft.Plan.Graph = graph
		s.Model = marshalFakeModel{draft: draft, review: s.Model.(marshalFakeModel).review}

		if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		approved, err := s.Approve(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}

		// Simulate an already-started worker on task a that wrote a commit in its worktree
		// before a restart/interruption.
		run, rev, err := s.load(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		pin, err := s.AdmitLaunchBase(ctx, "run", run, run.Tasks[0], nil)
		if err != nil {
			t.Fatal(err)
		}
		pin.ProcessStarted = true
		wtDir := filepath.Join(s.Worktrees, taskArtifactID("run", run.Tasks[0]))
		marshalGit(t, repo, "worktree", "add", "-b", run.Tasks[0].Branch, wtDir, run.BaseCommit)
		staleCommit := commitInWorktree(t, wtDir, "a.txt", "stale output\n", "worker output on stale base")

		next := run
		next.Tasks = append([]marshal.Task(nil), run.Tasks...)
		next.Tasks[0].State = marshal.Dispatched
		next.State = marshal.Dispatching
		op := &marshal.LifecycleOperation{
			Kind:       "launch",
			TaskID:     "a",
			Dir:        wtDir,
			Before:     pin.DeliveryCommit,
			After:      pin.AdmittedBaseCommit,
			Target:     pin.DeliveryRef,
			LaunchBase: &pin,
			Next:       &next,
			Event:      events.Event{Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"worker": "worker"}},
		}
		if _, err := s.beginOperation(ctx, "run", run, rev, op); err != nil {
			t.Fatal(err)
		}

		// Advance main so the already-started process is now on a stale base.
		commitFileOnBranch(t, repo, "main", "disjoint-started.txt", "moved\n", "main advanced while worker ran")

		// While the started process has NOT exited yet, capacity MUST be held.
		processExited := false
		s.VerifyProcessExit = func(context.Context, string, string) (bool, error) {
			return processExited, nil
		}
		_, err = s.Resume(ctx, "run")
		assertStaleBaseRefusal(t, err, marshal.EffectsUnknown)

		heldRun, err := s.Snapshot(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		if !heldRun.Tasks[0].StaleProcessExitUnverified || heldRun.Tasks[0].State != marshal.Dispatched {
			t.Fatalf("expected capacity held while exit unverified: %+v", heldRun.Tasks[0])
		}
		adm := EvaluateDispatchAdmission(s.Gate, heldRun, heldRun.Tasks[1], 1)
		if adm.Allowed {
			t.Fatalf("sibling task admitted while stale started process had unverified exit: %+v", adm)
		}

		// Once exit IS verified, stale output is quarantined, capacity is released,
		// and stale output is never promoted to HandedIn/Accepted/Merged.
		processExited = true
		_, err = s.Resume(ctx, "run")
		assertStaleBaseRefusal(t, err, marshal.EffectsPartial)

		quarantinedRun, err := s.Snapshot(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		if quarantinedRun.Operation != nil {
			t.Fatalf("expected reconciled operation cleared after verified exit: %+v", quarantinedRun.Operation)
		}
		if quarantinedRun.Tasks[0].StaleProcessExitUnverified || quarantinedRun.Tasks[0].State != marshal.Escalated || quarantinedRun.Tasks[0].EscalationCode != marshal.CodeStaleBase {
			t.Fatalf("unexpected task state after verified exit: %+v", quarantinedRun.Tasks[0])
		}
		if quarantinedRun.Tasks[0].ResultCommit != "" {
			t.Fatalf("stale output promoted to ResultCommit: %s", quarantinedRun.Tasks[0].ResultCommit)
		}
		qRef := quarantinedRun.Tasks[0].QuarantinedRef
		if qRef == "" || !strings.HasPrefix(qRef, "refs/"+statepath.RefName+"/run/quarantined/a/") {
			t.Fatalf("unexpected quarantined ref %q", qRef)
		}
		if got := marshalGit(t, repo, "rev-parse", "--verify", qRef); got != staleCommit {
			t.Fatalf("quarantined ref %s = %s, want stale commit %s", qRef, got, staleCommit)
		}
		if quarantinedRun.ApprovalScopeDigest != approved.ApprovalScopeDigest || quarantinedRun.BaseCommit != approved.BaseCommit {
			t.Fatalf("quarantine altered preserved approval: %+v", quarantinedRun)
		}
		// Verify capacity is no longer held by task a once exit was verified.
		admAfter := EvaluateDispatchAdmission(s.Gate, quarantinedRun, quarantinedRun.Tasks[1], 1)
		if !admAfter.Allowed {
			t.Fatalf("capacity remained held after verified exit: %+v", admAfter)
		}
	})
}
