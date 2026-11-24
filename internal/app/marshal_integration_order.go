package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// mergeBlocked reports why accepted task i may not merge yet, or "". Runs
// that merge in plan order wait for every earlier task. Other runs merge a
// task once its dependencies are merged and no earlier unmerged task shares
// its file scope.
func mergeBlocked(run marshal.Run, i int) string {
	if mergeInPlanOrder(run) {
		for j := 0; j < i; j++ {
			if run.Tasks[j].State != marshal.Merged {
				return "merge order violation"
			}
		}
		return ""
	}
	t := run.Tasks[i]
	if !marshalDepsMerged(run, t) {
		return "merge order violation: task " + t.PlanTaskID + " waits for its dependencies"
	}
	for j := 0; j < i; j++ {
		other := run.Tasks[j]
		if other.State == marshal.Merged || other.State == marshal.Cancelled {
			continue
		}
		if taskScopeConflict(t.Files, other.Files) != "" {
			return "merge order violation: task " + t.PlanTaskID + " waits for overlapping task " + other.PlanTaskID
		}
	}
	return ""
}

// mergeReady merges, by plan index, every accepted task whose dependencies
// are merged, so independent work never waits behind a slower task.
func (s *MarshalService) mergeReady(ctx context.Context, runID string, run marshal.Run) (bool, error) {
	progressed := false
	for {
		picked := ""
		for i, t := range run.Tasks {
			if t.State == marshal.Accepted && mergeBlocked(run, i) == "" {
				picked = t.PlanTaskID
				break
			}
		}
		if picked == "" {
			return progressed, nil
		}
		if err := s.Merge(ctx, runID, picked); err != nil {
			return progressed, err
		}
		progressed = true
		next, _, err := s.load(ctx, runID)
		if err != nil {
			return progressed, err
		}
		if next.State == marshal.AwaitingUser || next.Operation != nil {
			return progressed, nil
		}
		run = next
	}
}

// returnUnmergeable returns task i for rework at the integration head
// without touching the integration source.
func (s *MarshalService) returnUnmergeable(ctx context.Context, runID string, run marshal.Run, rev int64, i int, head, reason string, data map[string]any) error {
	run.Tasks[i].BaseCommit = head
	run.State = marshal.Reviewing
	attempt := 1 + run.Tasks[i].EvidenceAttemptBase + taskReturns(run.Tasks[i])
	stored, err := s.Store.GetMarshalReview(ctx, runID, run.Tasks[i].PlanTaskID, attempt)
	if err != nil {
		return err
	}
	review := stored.Value
	review.Reasons = append(review.Reasons, reason)
	if data == nil {
		data = map[string]any{}
	}
	data["base_commit"] = head
	_, err = s.finishMarshalReturn(ctx, runID, run, rev, i, review, data)
	return err
}

func mergeStagingRef(runID, taskID string) string {
	return "refs/" + statepath.RefName + "/staging/" + runID + "/" + taskID
}

// stageMergeCandidate builds the merge commit of task i onto before on a
// private ref, without touching the integration checkout, and binds it to
// the inputs it came from. A refusal names why the task returns instead.
func (s *MarshalService) stageMergeCandidate(ctx context.Context, runID string, run marshal.Run, rev int64, i int, before, dir string) (*marshal.MergeIntent, string, error) {
	t := run.Tasks[i]
	base := t.BaseCommit
	if base == "" {
		base = run.BaseCommit
	}
	revalidate := before != base && !mergeInPlanOrder(run)
	if revalidate {
		if reason, err := s.mergeBaseStale(ctx, run, i, base, before); err != nil || reason != "" {
			return nil, reason, err
		}
	}
	// A result already in the integration history merges as the head itself.
	candidate := before
	if _, ancErr := gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", t.ResultCommit, before); ancErr != nil {
		out, err := gitMarshal(ctx, s.Repository, "merge-tree", "--write-tree", before, t.ResultCommit)
		if err != nil {
			return nil, "merge refused: " + err.Error(), nil
		}
		tree := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
		if candidate, err = gitMarshal(ctx, s.Repository, "commit-tree", tree, "-p", before, "-p", t.ResultCommit, "-m", "marshal: merge "+t.PlanTaskID); err != nil {
			return nil, "", err
		}
	}
	tree, err := gitMarshal(ctx, s.Repository, "rev-parse", candidate+"^{tree}")
	if err != nil {
		return nil, "", err
	}
	staging := mergeStagingRef(runID, t.PlanTaskID)
	if _, err = gitMarshal(ctx, s.Repository, "update-ref", staging, candidate); err != nil {
		return nil, "", err
	}
	common, err := gitCommonDir(ctx, s.Repository)
	if err != nil {
		return nil, "", err
	}
	attempt := 1 + t.EvidenceAttemptBase + taskReturns(t)
	deps := map[string]string{}
	for _, dep := range t.DependsOn {
		if j := taskIndex(run, dep); j >= 0 {
			deps[dep] = run.Tasks[j].ResultCommit
		}
	}
	evidence := ""
	if stored, reviewErr := s.Store.GetMarshalReview(ctx, runID, t.PlanTaskID, attempt); reviewErr == nil {
		evidence = checksOnlyDigest(stored.Value)
	}
	in := &marshal.MergeIntent{
		Run: runID, Task: t.PlanTaskID, Attempt: attempt, PlanDigest: run.ApprovalScopeDigest, FenceRevision: rev,
		ExpectedHead: before, AttemptBase: base, Result: t.ResultCommit, DependencyHeads: deps,
		ApprovedChecksDigest: checksOnlyDigest(t.Checks), AcceptedEvidenceDigest: evidence,
		Candidate: candidate, CandidateTree: tree, StagingRef: staging, Worktree: dir, CommonDir: common,
	}
	if revalidate {
		if reason, err := s.revalidateMergeCandidate(ctx, runID, run, i, in); err != nil || reason != "" {
			return nil, reason, err
		}
		in.Revalidated = true
	}
	return in, "", nil
}

// mergeBaseStale proves that the integration head advanced past the
// attempt's base only by disjoint work: the base is in the integration
// history, every dependency is the one the attempt was built on, and no
// path the attempt changed was changed on the integration branch since.
func (s *MarshalService) mergeBaseStale(ctx context.Context, run marshal.Run, i int, base, before string) (string, error) {
	t := run.Tasks[i]
	stale := ErrStaleBase.Error() + ": "
	if _, err := gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", base, before); err != nil {
		return stale + "the attempt's base " + base + " is not in the integration history", nil
	}
	for _, dep := range t.DependsOn {
		j := taskIndex(run, dep)
		if j < 0 || run.Tasks[j].State != marshal.Merged || run.Tasks[j].ResultCommit == "" {
			return stale + "dependency " + dep + " is not merged", nil
		}
		if _, err := gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", run.Tasks[j].ResultCommit, base); err != nil {
			return stale + "dependency " + dep + " changed since the attempt started", nil
		}
	}
	ours, err := gitMarshalStdout(ctx, s.Repository, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "--no-renames", "-z", base, t.ResultCommit)
	if err != nil {
		return "", err
	}
	theirs, err := gitMarshalStdout(ctx, s.Repository, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "--no-renames", "-z", base, before)
	if err != nil {
		return "", err
	}
	changed := map[string]bool{}
	for _, name := range strings.Split(theirs, "\x00") {
		if name != "" {
			changed[name] = true
		}
	}
	for _, name := range strings.Split(ours, "\x00") {
		if changed[name] {
			return stale + "the integration head also changed " + name, nil
		}
	}
	return "", nil
}

// revalidateMergeCandidate reruns the task's approved checks on the staged
// candidate and, when the approved policy requires it, a fresh cross-review
// of the candidate. Evidence from the attempt's own tree is not reused.
func (s *MarshalService) revalidateMergeCandidate(ctx context.Context, runID string, run marshal.Run, i int, in *marshal.MergeIntent) (string, error) {
	t := run.Tasks[i]
	for _, c := range t.Checks {
		checkCtx := ctx
		if t.Mode == marshal.Governed {
			if s.GovernedCheck == nil {
				return "", errors.New("governed check runner is unavailable for the restaged candidate")
			}
			task := t
			checkCtx = driver.WithCheckRunner(ctx, func(ctx context.Context, dir, command string) marshal.CommandRecord {
				return s.GovernedCheck(ctx, runID, task.PlanTaskID, task.Worker, dir, command)
			})
		}
		// The check runs in its own detached checkout of the candidate.
		if record := driver.RunCheck(checkCtx, s.Repository, in.Candidate, c.Command, driver.DefaultCheckTimeout); record.ExitCode != 0 {
			return "merge refused: approved check " + c.Command + " failed on the advanced integration head: " + strings.TrimSpace(record.Output), nil
		}
	}
	if run.EffectiveReviewPolicy().CrossReview == marshal.CrossReviewOff {
		return "", nil
	}
	if s.CrossReview == nil {
		return "", errors.New("a fresh review of the restaged candidate is unavailable")
	}
	stored, err := s.Store.GetMarshalHandIn(ctx, runID, t.PlanTaskID, in.Attempt)
	if err != nil {
		return "", err
	}
	h := stored.Value
	h.BaseCommit = in.ExpectedHead
	h.ResultCommit = in.Candidate
	reviewTask := t
	architecture, architectureRequired := s.taskArchitecture(ctx, t)
	reviewTask.ArchitectureContext = architecture
	review, _, err := s.CrossReview(ctx, reviewTask, h, run.TaskControl(t))
	if err != nil {
		return "", err
	}
	if err := checkArchitectureReview(&review, architectureRequired); err != nil {
		return "", err
	}
	if review.Verdict != marshal.VerdictAccept || review.Reviewer == "" || review.Reviewer == h.Worker || review.Reviewer == s.Reviewer || len(review.EvidenceRefs) == 0 {
		return "merge refused: the fresh review of the restaged candidate did not accept it", nil
	}
	in.AcceptedEvidenceDigest = checksOnlyDigest(review)
	return "", nil
}

func gitCommonDir(ctx context.Context, dir string) (string, error) {
	common, err := gitMarshal(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(common); err == nil {
		common = resolved
	}
	return filepath.Clean(common), nil
}

// checkIntegrationCheckout requires the run-owned integration checkout to
// be exactly want: the same repository, the integration branch checked out,
// branch and HEAD at want, index and tracked files matching want, no Git
// operation in progress and no source-disqualifying untracked change.
func (s *MarshalService) checkIntegrationCheckout(ctx context.Context, in *marshal.MergeIntent, ref, want, wantTree string) error {
	dir := in.Worktree
	if _, err := os.Lstat(dir); err != nil {
		return fmt.Errorf("integration checkout %s is unavailable: %w", dir, err)
	}
	if common, err := gitCommonDir(ctx, dir); err != nil || common != in.CommonDir {
		return fmt.Errorf("integration checkout %s is not in the approved repository", dir)
	}
	if head, err := gitMarshal(ctx, dir, "symbolic-ref", "-q", "HEAD"); err != nil || head != ref {
		return fmt.Errorf("integration checkout %s does not have %s checked out", dir, ref)
	}
	if got, err := gitMarshal(ctx, s.Repository, "rev-parse", "--verify", ref); err != nil || got != want {
		return fmt.Errorf("integration branch %s is %s, want %s", ref, got, want)
	}
	if got, err := gitMarshal(ctx, dir, "rev-parse", "HEAD"); err != nil || got != want {
		return fmt.Errorf("integration checkout HEAD is %s, want %s", got, want)
	}
	for _, pending := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "REBASE_HEAD"} {
		if _, err := gitMarshal(ctx, dir, "rev-parse", "-q", "--verify", pending); err == nil {
			return fmt.Errorf("integration checkout %s has a Git operation in progress (%s)", dir, pending)
		}
	}
	if wantTree != "" {
		if got, err := gitMarshal(ctx, dir, "rev-parse", want+"^{tree}"); err != nil || got != wantTree {
			return fmt.Errorf("integration head tree is %s, want %s", got, wantTree)
		}
	}
	if _, err := gitMarshal(ctx, dir, "diff-index", "--cached", "--quiet", want); err != nil {
		return fmt.Errorf("integration checkout index does not match %s", want)
	}
	status, err := gitMarshalStdout(ctx, dir, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("inspect integration checkout: %w", err)
	}
	if changes := hostgit.SourceStatusChanges(status); len(changes) > 0 {
		return fmt.Errorf("integration checkout %s has uncommitted changes: %s", dir, strings.Join(changes, "; "))
	}
	return nil
}

// publishMergeCandidate fast-forwards the run-owned integration checkout to
// the staged candidate, updating branch, index and files together, then
// requires the checkout to be exactly the candidate.
func (s *MarshalService) publishMergeCandidate(ctx context.Context, in *marshal.MergeIntent, ref string) error {
	if _, err := gitMarshal(ctx, in.Worktree, "merge", "--ff-only", "--no-edit", "--no-overwrite-ignore", in.Candidate); err != nil {
		return fmt.Errorf("publish merge of %s: %w", in.Task, err)
	}
	return s.checkIntegrationCheckout(ctx, in, ref, in.Candidate, in.CandidateTree)
}

// mergeIntentCurrent reports why a journaled merge intent no longer matches
// the run, its approval or its staged candidate, or "".
func (s *MarshalService) mergeIntentCurrent(ctx context.Context, runID string, run marshal.Run, op *marshal.LifecycleOperation) string {
	in := op.Merge
	i := taskIndex(run, op.TaskID)
	switch {
	case i < 0 || in.Task != op.TaskID || in.Run != runID:
		return "the intent names another task"
	case run.Tasks[i].State != marshal.Accepted || run.Tasks[i].ResultCommit != in.Result:
		return "the accepted result changed"
	case run.ApprovalScopeDigest != in.PlanDigest || checksOnlyDigest(run.Tasks[i].Checks) != in.ApprovedChecksDigest:
		return "the approved plan or checks changed"
	case in.Worktree != filepath.Join(s.Worktrees, integrationTaskID(runID, run)):
		return "the intent names another integration checkout"
	}
	if got, err := gitMarshal(ctx, s.Repository, "rev-parse", "--verify", in.StagingRef); err != nil || got != in.Candidate {
		return "the staged candidate ref is missing or moved"
	}
	if got, err := gitMarshal(ctx, s.Repository, "rev-parse", in.Candidate+"^{tree}"); err != nil || got != in.CandidateTree {
		return "the staged candidate tree changed"
	}
	return ""
}

// recoverMergeIntent reconciles a journaled merge with its staged
// candidate. A clean checkout at the candidate acknowledges; a clean
// checkout still at the expected head retries the fast-forward. A partial,
// divergent or dirty checkout holds the journal with an inspection reason.
// No published effect reports completed=false with no error.
func (s *MarshalService) recoverMergeIntent(ctx context.Context, runID string, run marshal.Run, op *marshal.LifecycleOperation) (bool, error) {
	in := op.Merge
	hold := func(reason string) error {
		return interruptedOperationRefusal(runID, op, marshal.EffectsPartial, fmt.Sprintf("unfinished merge %s: %s; inspect retained worktree %s and staged ref %s", op.ID, reason, in.Worktree, in.StagingRef), []string{"/marshal status", "/marshal amend"})
	}
	if reason := s.mergeIntentCurrent(ctx, runID, run, op); reason != "" {
		return false, hold(reason)
	}
	ref, err := gitMarshal(ctx, s.Repository, "rev-parse", "--verify", op.Target)
	switch {
	case err != nil:
		if _, statErr := os.Lstat(in.Worktree); statErr == nil {
			return false, hold("integration branch is unreadable")
		}
		return false, nil
	case ref == in.Candidate:
		if err := s.checkIntegrationCheckout(ctx, in, op.Target, in.Candidate, in.CandidateTree); err != nil {
			return false, hold(err.Error())
		}
		return true, nil
	case ref == in.ExpectedHead:
		if _, statErr := os.Lstat(in.Worktree); statErr != nil {
			return false, nil
		}
		if err := s.checkIntegrationCheckout(ctx, in, op.Target, in.ExpectedHead, ""); err != nil {
			return false, hold(err.Error())
		}
		if err := s.publishMergeCandidate(ctx, in, op.Target); err != nil {
			return false, hold(err.Error())
		}
		return true, nil
	default:
		return false, hold("integration moved to " + ref)
	}
}
