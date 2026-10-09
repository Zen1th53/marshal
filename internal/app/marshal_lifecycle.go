package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/verification"
	"github.com/Zen1th53/marshal/internal/worker"
	"github.com/Zen1th53/marshal/internal/worktree"
)

type MarshalDispatch struct {
	Driver   driver.Driver
	Handle   *driver.Handle
	TaskID   string
	Started  time.Time
	Deadline time.Time
}

func (s *MarshalService) Dispatch(ctx context.Context, runID, taskID, brief string) (MarshalDispatch, error) {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return MarshalDispatch{}, err
	}
	if run.State != marshal.Approved && run.State != marshal.Dispatching && run.State != marshal.Reviewing && run.State != marshal.Merging {
		return MarshalDispatch{}, errors.New("dispatch is paused")
	}
	if err := s.executionAdmission(ctx, runID, taskID, run); err != nil {
		return MarshalDispatch{}, err
	}
	i := taskIndex(run, taskID)
	if i < 0 {
		return MarshalDispatch{}, model.ErrNotFound
	}
	t := &run.Tasks[i]
	if t.State != marshal.Queued && t.State != marshal.Returned && t.State != marshal.Reassigned {
		return MarshalDispatch{}, errors.New("task is not ready")
	}
	for _, dep := range t.DependsOn {
		j := taskIndex(run, dep)
		if j < 0 || run.Tasks[j].State != marshal.Merged {
			return MarshalDispatch{}, errors.New("dependency is not merged")
		}
	}
	taskUsage, planUsage, err := s.marshalUsage(ctx, runID, taskID)
	if err != nil {
		return MarshalDispatch{}, err
	}
	if blocked, reason := budgetBlocksDispatch(run.Budget, taskUsage, planUsage); blocked {
		pauseMarshal(&run, "budget boundary: "+reason, "start a new run with updated budget settings; settings do not change this run", "")
		if err := s.save(ctx, runID, run, rev); err != nil {
			return MarshalDispatch{}, err
		}
		return MarshalDispatch{}, fmt.Errorf("dispatch paused at budget boundary: %s", reason)
	}
	policy := marshal.TierPolicy(s.Gate, run.Settings)
	active := 0
	for _, other := range run.Tasks {
		if other.State == marshal.Dispatched {
			active++
		}
	}
	if active >= policy.Concurrency {
		return MarshalDispatch{}, errors.New("dispatch concurrency reached")
	}
	if run.Process05Bound && t.Mode == marshal.Governed && active > 0 {
		return MarshalDispatch{}, errors.New("governed plan tasks dispatch one at a time")
	}
	if run.Process05Bound && t.Mode == marshal.Governed {
		for j := 0; j < i; j++ {
			if run.Tasks[j].State != marshal.Merged {
				return MarshalDispatch{}, errors.New("earlier governed task must be merged before dispatch")
			}
		}
	}
	d := s.Drivers[t.Worker]
	if t.Mode == marshal.Governed && s.GovernedDrivers != nil {
		d = s.GovernedDrivers[t.Worker]
	}
	if governed, ok := d.(driver.Governed); ok && governed.Check == nil && s.GovernedCheck != nil {
		governed.Check = func(ctx context.Context, req driver.Request, dir, command string) marshal.CommandRecord {
			return s.GovernedCheck(ctx, runID, req.Task.PlanTaskID, req.Task.Worker, dir, command)
		}
		d = governed
	}
	if d == nil {
		return MarshalDispatch{}, fmt.Errorf("no driver for %s", t.Worker)
	}
	if d.Mode() != t.Mode {
		return MarshalDispatch{}, fmt.Errorf("worker %s has %s driver, task requires %s", t.Worker, d.Mode(), t.Mode)
	}
	// A reassigned worker starts from the task's base, not from the work the
	// previous worker had returned twice.
	fresh := t.State == marshal.Reassigned && t.ResultCommit != ""
	if (t.ResultCommit == "" || fresh) && len(t.DependsOn) > 0 {
		// A task builds on its dependencies' merged work: it starts from the
		// integration head, where every dependency has been merged.
		integration := filepath.Join(s.Worktrees, integrationTaskID(runID, run))
		head, headErr := gitMarshal(ctx, integration, "rev-parse", "HEAD")
		if headErr != nil {
			return MarshalDispatch{}, fmt.Errorf("task base is unavailable: %w", headErr)
		}
		t.BaseCommit = strings.TrimSpace(head)
	}
	if run.Process05Bound && t.Mode == marshal.Governed && t.ResultCommit == "" && i > 0 {
		integration := filepath.Join(s.Worktrees, integrationTaskID(runID, run))
		if head, headErr := gitMarshal(ctx, integration, "rev-parse", "HEAD"); headErr == nil {
			t.BaseCommit = head
		} else if run.Tasks[i-1].State == marshal.Merged {
			return MarshalDispatch{}, fmt.Errorf("governed task base is unavailable: %w", headErr)
		}
	}
	next := run
	next.Tasks = append([]marshal.Task(nil), run.Tasks...)
	next.Tasks[i].HoneypotRequired = t.Mode == marshal.Governed && s.HandInGuard != nil
	next.Tasks[i].State = marshal.Dispatched
	if fresh {
		next.Tasks[i].ResultCommit = ""
	}
	next.State = marshal.Dispatching
	if next.Tier != marshal.Ultra {
		next.Tier = policy.Tier
	}
	briefSum := sha256.Sum256([]byte(brief))
	op := &marshal.LifecycleOperation{Kind: "launch", TaskID: taskID, Next: &next, Event: events.Event{Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"tier": string(policy.Tier), "worker": t.Worker, "brief": brief, "brief_sha256": hex.EncodeToString(briefSum[:])}}}
	rev, err = s.beginOperation(ctx, runID, run, rev, op)
	if err != nil {
		return MarshalDispatch{}, err
	}
	effectCompleted := false
	defer func() {
		if !effectCompleted {
			s.abandonOperation(ctx, runID, op)
		}
	}()

	wt := worktree.New(s.Repository, s.Worktrees)
	request := model.WorktreeRequest{TaskID: taskArtifactID(runID, *t), Branch: t.Branch, BaseCommit: t.BaseCommit}
	var tree model.Worktree
	if t.ResultCommit != "" {
		request.BaseCommit = t.ResultCommit
		tree, err = wt.Resume(ctx, request)
		if err == nil && fresh {
			err = restartTaskBranch(ctx, s.Repository, runID, t, tree.Path)
		}
	} else {
		tree, err = wt.Prepare(ctx, request)
	}
	if err != nil {
		return MarshalDispatch{}, err
	}
	dispatchCtx := ctx
	deadline := time.Time{}
	if remaining := wallBudgetRemaining(run.Budget, taskUsage, planUsage); remaining > 0 {
		deadline = time.Now().Add(remaining)
		var cancel context.CancelFunc
		dispatchCtx, cancel = context.WithDeadline(ctx, deadline)
		_ = cancel
	}
	handle, err := d.Launch(dispatchCtx, driver.Request{RunID: runID, Task: *t, Worktree: tree.Path, Brief: brief})
	if err != nil {
		return MarshalDispatch{}, err
	}
	effectCompleted = true
	if err = s.afterOperationEffect(op); err != nil {
		_ = d.Cancel(handle)
		return MarshalDispatch{}, err
	}
	if err = s.completeOperation(ctx, runID, rev, op); err != nil {
		_ = d.Cancel(handle)
		return MarshalDispatch{}, err
	}
	if _, err = s.Charge(ctx, runID, taskID, "dispatch", marshal.Charge{Tokens: marshal.Amount{}, Money: marshal.Amount{}}); err != nil {
		_ = d.Cancel(handle)
		return MarshalDispatch{}, err
	}
	return MarshalDispatch{Driver: d, Handle: handle, TaskID: taskID, Started: s.clock(), Deadline: deadline}, nil
}

// restartTaskBranch sets a task's branch and worktree back to its base for a
// new worker. The returned attempt stays reachable under
// refs/marshal/<run>/returned/<task>/attempt-<n>, because its hand-ins and
// reviews name that commit as evidence.
func restartTaskBranch(ctx context.Context, repository, runID string, t *marshal.Task, dir string) error {
	attempt := t.EvidenceAttemptBase
	for _, n := range t.ReturnsByAgent {
		attempt += n
	}
	ref := fmt.Sprintf("refs/marshal/%s/returned/%s/attempt-%d", runID, t.PlanTaskID, attempt)
	if _, err := gitMarshal(ctx, repository, "update-ref", ref, t.ResultCommit); err != nil {
		return fmt.Errorf("preserve returned attempt: %w", err)
	}
	if _, err := gitMarshal(ctx, dir, "reset", "--hard", t.BaseCommit); err != nil {
		return fmt.Errorf("restart task branch: %w", err)
	}
	t.ResultCommit = ""
	return nil
}

func worktreeTaskID(runID, taskID string) string {
	return "TASK-" + strings.NewReplacer("/", "-", " ", "-").Replace(runID+"-"+taskID)
}

func (s *MarshalService) CollectHandIn(ctx context.Context, runID string, dispatch MarshalDispatch) (marshal.HandIn, error) {
	if dispatch.Driver == nil || dispatch.Handle == nil {
		return marshal.HandIn{}, errors.New("missing dispatch handle")
	}
	initial, initialRev, err := s.load(ctx, runID)
	if err != nil {
		return marshal.HandIn{}, err
	}
	op := &marshal.LifecycleOperation{Kind: "hand-in", TaskID: dispatch.TaskID, Dir: dispatch.Handle.Worktree(), Event: events.Event{Type: events.EventTypeMarshalTaskHandedIn}}
	if _, err = s.beginOperation(ctx, runID, initial, initialRev, op); err != nil {
		return marshal.HandIn{}, err
	}
	handin, err := dispatch.Driver.Wait(ctx, dispatch.Handle)
	if err != nil {
		pending, pendingRev, loadErr := s.load(context.WithoutCancel(ctx), runID)
		if loadErr != nil {
			return handin, errors.Join(err, loadErr)
		}
		pending.Operation = nil
		if saveErr := s.save(context.WithoutCancel(ctx), runID, pending, pendingRev); saveErr != nil {
			return handin, errors.Join(err, saveErr)
		}
	}
	if errors.Is(err, worker.ErrHoneypot) {
		run, rev, loadErr := s.load(context.WithoutCancel(ctx), runID)
		if loadErr != nil {
			return handin, errors.Join(err, loadErr)
		}
		i := taskIndex(run, dispatch.TaskID)
		if i < 0 {
			return handin, errors.Join(err, model.ErrNotFound)
		}
		run.Tasks[i].State = marshal.Escalated
		run.Tasks[i].EscalationReason = "security: " + err.Error()
		pauseMarshal(&run, "security: "+err.Error(), "resolve the security alert and amend the plan before retrying", "")
		if saveErr := s.save(context.WithoutCancel(ctx), runID, run, rev); saveErr != nil {
			return handin, errors.Join(err, saveErr)
		}
		alertErr := s.record(context.WithoutCancel(ctx), runID, dispatch.TaskID, events.EventTypeMarshalEscalated, map[string]any{"reason": err.Error()})
		return handin, errors.Join(err, alertErr)
	}
	if err != nil {
		if !dispatch.Deadline.IsZero() && time.Now().After(dispatch.Deadline) {
			run, rev, loadErr := s.load(ctx, runID)
			if loadErr != nil {
				return handin, loadErr
			}
			i := taskIndex(run, dispatch.TaskID)
			if i < 0 || run.Tasks[i].State != marshal.Dispatched {
				return handin, errors.New("task is not dispatched")
			}
			run.State = marshal.Reviewing
			_, returnErr := s.finishMarshalReturn(ctx, runID, run, rev, i, marshal.Review{Reviewer: "marshal-runtime", Reasons: []string{"worker stopped at the wall-time ceiling"}}, nil)
			elapsed := s.clock().Sub(dispatch.Started)
			if elapsed < 0 {
				elapsed = 0
			}
			_, chargeErr := s.Charge(ctx, runID, dispatch.TaskID, "dispatch_timeout", marshal.Charge{Tokens: marshal.Amount{Known: false}, Money: marshal.Amount{Known: false}, WallTime: elapsed})
			return marshal.HandIn{}, errors.Join(returnErr, chargeErr)
		}
		return handin, err
	}
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return handin, err
	}
	i := taskIndex(run, dispatch.TaskID)
	if i < 0 || run.Tasks[i].State != marshal.Dispatched {
		return handin, errors.New("task is not dispatched")
	}
	t := &run.Tasks[i]
	if handin.Worker != t.Worker || handin.BaseCommit != t.BaseCommit || handin.Mode != t.Mode {
		return handin, errors.New("hand-in identity differs from dispatch")
	}
	validationErr := marshal.ValidateHandIn(*t, handin)
	if t.HoneypotRequired && s.HandInGuard == nil {
		validationErr = errors.Join(validationErr, errors.New("honeypot: required scan guard unavailable after recovery"))
	}
	if t.Mode == marshal.Governed && s.HandInGuard != nil {
		scanID, scanErr := s.HandInGuard(ctx, dispatch.TaskID, dispatch.Handle.Worktree(), handin)
		t.HoneypotScanID = scanID
		validationErr = errors.Join(validationErr, scanErr)
	}
	attempt := 1 + t.EvidenceAttemptBase
	for _, n := range t.ReturnsByAgent {
		attempt += n
	}
	t.ResultCommit = handin.ResultCommit
	if validationErr == nil {
		t.State = marshal.HandedIn
		run.State = marshal.Reviewing
	}
	run.Operation = nil
	op.HandIn = &handin
	op.Attempt = attempt
	op.Next = &run
	op.Event.Data["result_commit"] = handin.ResultCommit
	// Persist the observed hand-in as the receipt before recording completion.
	pending := initial
	pending.Operation = op
	if err = s.save(ctx, runID, pending, rev); err != nil {
		return handin, err
	}
	rev++
	if err = s.afterOperationEffect(op); err != nil {
		return handin, err
	}
	if err = s.completeOperation(ctx, runID, rev, op); err != nil {
		return handin, err
	}
	elapsed := s.clock().Sub(dispatch.Started)
	if elapsed < 0 {
		elapsed = 0
	}
	budget, err := s.Charge(ctx, runID, dispatch.TaskID, "dispatch_wall", marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}, WallTime: elapsed})
	if err != nil {
		return handin, err
	}
	if len(budget.PlanExceeded) > 0 || budgetNeedsOperator(run.Budget, budget) {
		run, rev, err = s.load(ctx, runID)
		if err != nil {
			return handin, err
		}
		pauseMarshalBudget(&run, budget)
		if err = s.save(ctx, runID, run, rev); err != nil {
			return handin, err
		}
		return handin, nil
	}
	if validationErr != nil || len(budget.PlanExceeded) > 0 || len(budget.TaskExceeded) > 0 {
		run, rev, err = s.load(ctx, runID)
		if err != nil {
			return handin, err
		}
		if validationErr != nil {
			run.Tasks[i].ResultCommit = handin.ResultCommit
			if run.State != marshal.AwaitingUser {
				run.State = marshal.Reviewing
			}
		}
		if len(budget.PlanExceeded) > 0 {
			run.State = marshal.AwaitingUser
		}
		var reasons []string
		if validationErr != nil {
			reasons = append(reasons, validationErr.Error())
		}
		if len(budget.TaskExceeded) > 0 {
			reasons = append(reasons, "task budget exceeded")
		}
		if len(reasons) > 0 {
			_, err = s.finishMarshalReturn(ctx, runID, run, rev, i, marshal.Review{Reviewer: "marshal-runtime", Reasons: reasons}, nil)
			return handin, err
		}
		if err = s.save(ctx, runID, run, rev); err != nil {
			return handin, err
		}
		return handin, s.record(ctx, runID, dispatch.TaskID, events.EventTypeMarshalEscalated, map[string]any{"reason": "plan budget exceeded"})
	}
	return handin, nil
}

func (s *MarshalService) Review(ctx context.Context, runID, taskID string, charge marshal.Charge) (marshal.Verdict, error) {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return "", err
	}
	if err = unfinishedMarshalOperation(run); err != nil {
		return "", err
	}
	i := taskIndex(run, taskID)
	if i < 0 || run.Tasks[i].State != marshal.HandedIn {
		return "", errors.New("task has no hand-in")
	}
	t := &run.Tasks[i]
	attempt := 1 + t.EvidenceAttemptBase
	for _, n := range t.ReturnsByAgent {
		attempt += n
	}
	stored, err := s.Store.GetMarshalHandIn(ctx, runID, taskID, attempt)
	if err != nil {
		return "", err
	}
	h := stored.Value
	if h.ResultCommit == "" || h.ResultCommit != t.ResultCommit {
		return "", errors.New("hand-in result does not match task")
	}
	if err := marshal.ValidateHandIn(*t, h); err != nil {
		return "", err
	}
	if s.Model == nil {
		return "", errors.New("Marshal model is unavailable")
	}
	proposal, err := s.Model.Review(ctx, *t, h, run.Settings.EffectiveControl())
	if err != nil {
		return "", err
	}
	// Only the runtime may attach an independent review. Model output is a claim.
	proposal.Independent = nil
	if proposal.Verdict != marshal.VerdictAccept && proposal.Verdict != marshal.VerdictReturn && proposal.Verdict != marshal.VerdictReassign && proposal.Verdict != marshal.VerdictEscalate {
		return "", errors.New("invalid model review verdict")
	}
	if s.Reviewer == "" || s.Reviewer == h.Worker {
		return "", errors.New("reviewer must differ from executor")
	}
	tier, err := s.dispatchedTier(ctx, runID, taskID)
	if err != nil {
		return "", err
	}
	policy := marshal.DispatchPolicy{Tier: tier, CrossReviewRequired: tier == marshal.Ultra, VerifierRequired: tier == marshal.Ultra}
	crossReviewerProvider := ""
	crossReviewAccepted := false
	crossReviewData := map[string]any{}
	if policy.CrossReviewRequired {
		if s.CrossReview == nil {
			return "", errors.New("cross-review is unavailable")
		}
		crossReview, provider, reviewErr := s.CrossReview(ctx, *t, h, run.Settings.EffectiveControl())
		if reviewErr != nil {
			return "", reviewErr
		}
		if (crossReview.Verdict != marshal.VerdictAccept && crossReview.Verdict != marshal.VerdictReturn && crossReview.Verdict != marshal.VerdictReassign && crossReview.Verdict != marshal.VerdictEscalate) || crossReview.Reviewer == "" || crossReview.Reviewer == h.Worker || crossReview.Reviewer == s.Reviewer || len(crossReview.EvidenceRefs) == 0 {
			return "", errors.New("independent cross-review is incomplete")
		}
		if err := resolveReviewReferences(crossReview, h); err != nil {
			return "", err
		}
		crossReviewerProvider = provider
		if err = marshal.CheckCrossReviewProvider(policy, h.Provider, crossReviewerProvider); err != nil {
			return "", err
		}
		crossReviewAccepted = crossReview.Verdict == marshal.VerdictAccept
		crossReviewData["cross_review_verdict"] = string(crossReview.Verdict)
		crossReviewData["cross_review_reviewer"] = crossReview.Reviewer
		crossReviewData["cross_review_provider"] = crossReviewerProvider
		proposal.Independent = &marshal.IndependentReview{Verdict: crossReview.Verdict, Reviewer: crossReview.Reviewer, Provider: crossReviewerProvider, Reasons: crossReview.Reasons, EvidenceRefs: crossReview.EvidenceRefs}
	}
	met, total, failing := marshal.CriteriaMet(*t, h)
	envelope, state, err := s.gateInputs(ctx, runID, taskID, run, constitution.DomainCompletion)
	if err != nil {
		return "", err
	}
	if policy.Tier == marshal.Ultra {
		envelope.Mode = constitution.ModeUltra
		state.EntitlementValid = s.Gate != nil && s.Gate.Capability(marshal.CapabilityMarshal)
	}
	userApproval := ""
	if run.Settings.AcceptanceMode == marshal.AcceptUser {
		if s.ApprovalActor == nil {
			return "", errors.New("user approval source is unavailable")
		}
		userApproval, err = s.ApprovalActor(ctx, runID, taskAcceptancePurpose(run, *t, attempt, h))
		if err != nil {
			return "", err
		}
	}
	verdict := constitution.EvaluateTaskAcceptance(constitution.Default(), envelope, state, constitution.TaskAcceptance{Mode: run.Settings.AcceptanceMode, MarshalVerdictAccept: proposal.Verdict == marshal.VerdictAccept, UserApprovalActor: userApproval, Executor: h.Worker, Reviewer: s.Reviewer, ResultCommit: h.ResultCommit, EvidenceCommit: h.ResultCommit, CriteriaMet: met, CriteriaTotal: total, IndependentReviewDone: !policy.CrossReviewRequired || crossReviewAccepted})
	if err := s.recordConstitutionalVerdict(ctx, envelope, verdict); err != nil {
		return "", err
	}
	proposal.Reviewer = s.Reviewer
	result := proposal.Verdict
	if !verdict.Outcome.Permits() {
		result = marshal.VerdictReturn
		if proposal.Verdict == marshal.VerdictAccept {
			proposal.Reasons = append(proposal.Reasons, "acceptance refused: "+string(verdict.Reason))
		}
		if len(failing) > 0 {
			proposal.Reasons = append(proposal.Reasons, "criteria without passing evidence: "+strings.Join(failing, "; "))
		}
	}
	budget, err := s.Charge(ctx, runID, taskID, "review", charge)
	if err != nil {
		return "", err
	}
	if len(budget.PlanExceeded) > 0 || budgetNeedsOperator(run.Budget, budget) {
		paused, pausedRev, loadErr := s.load(ctx, runID)
		if loadErr != nil {
			return "", loadErr
		}
		pauseMarshalBudget(&paused, budget)
		if loadErr = s.save(ctx, runID, paused, pausedRev); loadErr != nil {
			return "", loadErr
		}
		return proposal.Verdict, nil
	}
	if len(budget.TaskExceeded) > 0 && result == marshal.VerdictAccept {
		result = marshal.VerdictReturn
		proposal.Reasons = append(proposal.Reasons, "task budget exceeded")
	}
	eventData := map[string]any{"gate": string(verdict.Outcome), "reason": string(verdict.Reason)}
	for key, value := range crossReviewData {
		eventData[key] = value
	}
	if result != marshal.VerdictAccept {
		return s.finishMarshalReturn(ctx, runID, run, rev, i, proposal, eventData)
	}
	if _, err = s.Store.SetMarshalReview(ctx, runID, taskID, attempt, proposal); err != nil {
		return "", err
	}
	t.State = marshal.Accepted
	if err = s.save(ctx, runID, run, rev); err != nil {
		return "", err
	}
	return result, s.record(ctx, runID, taskID, events.EventTypeMarshalTaskAccepted, eventData)
}

// finishMarshalReturn records the attempt's reasons and applies the same
// rework policy for validation, review, budget and merge failures.
func (s *MarshalService) finishMarshalReturn(ctx context.Context, runID string, run marshal.Run, revision int64, index int, review marshal.Review, data map[string]any) (marshal.Verdict, error) {
	t := &run.Tasks[index]
	attempt := 1 + t.EvidenceAttemptBase
	for _, n := range t.ReturnsByAgent {
		attempt += n
	}
	if len(review.Reasons) == 0 {
		review.Reasons = []string{"the review did not accept the hand-in"}
	}
	result := applyMarshalReturn(&run, t)
	if review.Verdict == "" {
		review.Verdict = marshal.VerdictReturn
	}
	if _, err := s.Store.GetMarshalReview(ctx, runID, t.PlanTaskID, attempt); errors.Is(err, model.ErrNotFound) {
		if _, err = s.Store.SetMarshalReview(ctx, runID, t.PlanTaskID, attempt, review); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	kind := events.EventTypeMarshalTaskReturned
	if result == marshal.VerdictReassign {
		kind = events.EventTypeMarshalTaskReassigned
	} else if result == marshal.VerdictEscalate {
		kind = events.EventTypeMarshalEscalated
	}
	if data == nil {
		data = map[string]any{}
	}
	data["reason"] = strings.Join(review.Reasons, "; ")
	data["return_attempt"] = fmt.Sprint(attempt)
	reasons := append([]string(nil), review.Reasons...)
	if review.Independent != nil {
		reasons = append(reasons, review.Independent.Reasons...)
	}
	data["return_reasons"] = reasons
	if err := s.record(ctx, runID, t.PlanTaskID, kind, data); err != nil {
		return result, err
	}
	if err := s.save(ctx, runID, run, revision); err != nil {
		return "", err
	}
	_, err := s.Charge(ctx, runID, t.PlanTaskID, "return", zeroMarshalCharge())
	return result, err
}

// applyMarshalReturn counts a return against the task's worker and moves the
// task on: back to the same worker, to another worker once the rework limit
// is reached, or to the user when another worker has failed it too.
func applyMarshalReturn(run *marshal.Run, t *marshal.Task) marshal.Verdict {
	if t.ReturnsByAgent == nil {
		t.ReturnsByAgent = map[string]int{}
	}
	t.ReturnsByAgent[t.Worker]++
	result := marshal.NextAfterReturn(*t, run.Settings.ReworkLimit)
	if len(t.ReturnsByAgent) == 1 && t.ReturnsByAgent[t.Worker] >= run.Settings.ReworkLimit {
		result = marshal.VerdictReassign
	}
	switch result {
	case marshal.VerdictReturn:
		t.State = marshal.Returned
	case marshal.VerdictReassign:
		t.State = marshal.Reassigned
	case marshal.VerdictEscalate:
		t.State = marshal.Escalated
		t.EscalationReason = "worker rework limit reached"
		pauseMarshal(run, "worker rework limit reached", "resolve the escalation or amend the plan", "")
	}
	return result
}

// ReturnByUser sends a handed-in task back on the person's decision, with
// their reason. It takes the same rework path as a returned review, so the
// rework limit, reassignment and escalation apply unchanged. The reason is
// stored as the attempt's review, where the task's next brief can read it.
func (s *MarshalService) ReturnByUser(ctx context.Context, runID, taskID, reason string) (marshal.Verdict, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", errors.New("a reason is required to return a task")
	}
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return "", err
	}
	if err = unfinishedMarshalOperation(run); err != nil {
		return "", err
	}
	i := taskIndex(run, taskID)
	if i < 0 || run.Tasks[i].State != marshal.HandedIn {
		return "", errors.New("task is not awaiting a decision")
	}
	if s.ApprovalActor == nil {
		return "", errors.New("user approval source is unavailable")
	}
	user, err := s.ApprovalActor(ctx, runID, "return:"+taskID)
	if err != nil || user == "" {
		return "", errors.New("the return was not given by the person")
	}
	return s.finishMarshalReturn(ctx, runID, run, rev, i, marshal.Review{Reviewer: user, Reasons: []string{reason}}, map[string]any{"returned_by": user})
}

func (s *MarshalService) gateInputs(ctx context.Context, runID, taskID string, run marshal.Run, domain constitution.Domain) (constitution.Envelope, constitution.RuntimeState, error) {
	now := s.clock()
	env := constitution.Envelope{DecisionID: runID + "-" + taskID + "-" + fmt.Sprint(now.UnixNano()), ConstitutionVersion: constitution.Current, Process: 6, ProjectID: s.ProjectID, SessionID: runID, Actor: "marshal-runtime", ActorRole: "orchestrator", Surface: constitution.SurfaceCore, Mode: constitution.ModeStandard, Domain: domain, Action: "marshal " + string(domain), Reversibility: constitution.ReversibleInternal, StateDigest: run.ApprovalScopeDigest, RequestedAt: now}
	if s.GateState == nil {
		return env, constitution.RuntimeState{}, errors.New("constitutional runtime state is unavailable")
	}
	state, err := s.GateState(ctx, runID, taskID)
	bound, found, bindingErr := s.constitutionService().SessionVersion(ctx, runID)
	if bindingErr != nil {
		return env, state, bindingErr
	}
	if found {
		env.ConstitutionVersion = bound
	}
	state.RuntimeConstitution = constitution.Current
	state.Now = now
	return env, state, err
}

// Charge records known and unknown usage in the durable decision stream.
func (s *MarshalService) Charge(ctx context.Context, runID, taskID, phase string, charge marshal.Charge) (marshal.BudgetResult, error) {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return marshal.BudgetResult{}, err
	}
	if err = unfinishedMarshalOperation(run); err != nil {
		return marshal.BudgetResult{}, err
	}
	if phase == "" {
		return marshal.BudgetResult{}, errors.New("charge phase is required")
	}
	if err = s.record(ctx, runID, taskID, events.EventTypeMarshalUsageCharged, map[string]any{"charge_phase": phase, "usage_units": charge.Tokens.Value, "usage_known": charge.Tokens.Known, "wall_ns": charge.WallTime.Nanoseconds(), "money": charge.Money.Value, "money_known": charge.Money.Known}); err != nil {
		return marshal.BudgetResult{}, err
	}
	task, planTotal, err := s.marshalUsage(ctx, runID, taskID)
	if err != nil {
		return marshal.BudgetResult{}, err
	}
	result := marshal.CheckBudget(run.Budget, task, planTotal)
	if len(result.PlanExceeded) > 0 || budgetNeedsOperator(run.Budget, result) {
		pauseMarshalBudget(&run, result)
		if err := s.save(ctx, runID, run, rev); err != nil {
			return marshal.BudgetResult{}, err
		}
	}
	return result, nil
}

func (s *MarshalService) marshalUsage(ctx context.Context, runID, taskID string) (marshal.Charge, marshal.Charge, error) {
	history, err := s.Store.MarshalDecisions(ctx, runID)
	if err != nil {
		return marshal.Charge{}, marshal.Charge{}, err
	}
	task, planTotal := marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}}, marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}}
	add := func(to *marshal.Charge, e events.Event) {
		data := e.Data
		if data["charge_phase"] == nil {
			return
		}
		if known, _ := data["usage_known"].(bool); known {
			to.Tokens.Value += int64Number(data["usage_units"])
		} else {
			to.Tokens.Known = false
		}
		if known, _ := data["money_known"].(bool); known {
			to.Money.Value += int64Number(data["money"])
		} else {
			to.Money.Known = false
		}
		to.WallTime += time.Duration(int64Number(data["wall_ns"]))
	}
	for _, event := range history {
		add(&planTotal, event)
		if event.TaskID == taskID {
			add(&task, event)
		}
	}
	return task, planTotal, nil
}

func budgetNeedsOperator(b marshal.Budget, result marshal.BudgetResult) bool {
	for _, name := range result.Unknown {
		switch name {
		case "tokens":
			if b.Tokens.Task > 0 || b.Tokens.Plan > 0 {
				return true
			}
		case "money":
			if b.Money.Task > 0 || b.Money.Plan > 0 {
				return true
			}
		}
	}
	return false
}

func budgetBlocksDispatch(b marshal.Budget, task, plan marshal.Charge) (bool, string) {
	if budgetNeedsOperator(b, marshal.CheckBudget(b, task, plan)) {
		return true, "configured token or money ceiling cannot be evaluated because usage is unknown"
	}
	checks := []struct {
		name                 string
		task, plan           int64
		taskLimit, planLimit int64
		taskKnown, planKnown bool
	}{
		{"tokens", task.Tokens.Value, plan.Tokens.Value, b.Tokens.Task, b.Tokens.Plan, task.Tokens.Known, plan.Tokens.Known},
		{"money", task.Money.Value, plan.Money.Value, b.Money.Task, b.Money.Plan, task.Money.Known, plan.Money.Known},
		{"wall time", int64(task.WallTime / time.Second), int64(plan.WallTime / time.Second), b.WallTime.Task, b.WallTime.Plan, true, true},
	}
	for _, check := range checks {
		if check.taskLimit > 0 && check.taskKnown && check.task >= check.taskLimit {
			return true, "task " + check.name + " ceiling reached"
		}
		if check.planLimit > 0 && check.planKnown && check.plan >= check.planLimit {
			return true, "plan " + check.name + " ceiling reached"
		}
	}
	return false, ""
}

func wallBudgetRemaining(b marshal.Budget, task, plan marshal.Charge) time.Duration {
	remaining := time.Duration(0)
	for _, limit := range []struct {
		ceiling int64
		used    time.Duration
	}{
		{b.WallTime.Task, task.WallTime},
		{b.WallTime.Plan, plan.WallTime},
	} {
		if limit.ceiling <= 0 {
			continue
		}
		left := time.Duration(limit.ceiling)*time.Second - limit.used
		if remaining == 0 || left < remaining {
			remaining = left
		}
	}
	if remaining < 0 {
		return 0
	}
	return remaining
}
func containsMarshal(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
func int64Number(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}

func isPermanentEscalationReason(reason string) bool {
	r := strings.ToLower(reason)
	for _, s := range []string{"budget", "quarantine", "suspension", "honeypot", "security", "governing files changed"} {
		if strings.Contains(r, s) {
			return true
		}
	}
	return false
}

func unpauseIfNoEscalated(run *marshal.Run) {
	for _, t := range run.Tasks {
		if t.State == marshal.Escalated {
			return
		}
	}
	if run.State == marshal.AwaitingUser {
		run.State = marshal.Dispatching
		run.Pause = nil
	}
}

func (s *MarshalService) Reassign(ctx context.Context, runID, taskID, worker string) error {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	if err = unfinishedMarshalOperation(run); err != nil {
		return err
	}
	i := taskIndex(run, taskID)
	if i < 0 {
		return errors.New("task not found")
	}
	t := &run.Tasks[i]
	if t.State == marshal.Escalated {
		if isPermanentEscalationReason(t.EscalationReason) {
			return fmt.Errorf("cannot reassign task with escalation reason: %s", t.EscalationReason)
		}
	} else if t.State != marshal.Reassigned {
		return errors.New("only escalated tasks can be reassigned")
	}
	if worker == "" {
		return errors.New("worker is required")
	}
	if marshalHarnessName(worker) == marshalHarnessName(t.Worker) {
		return errors.New("cannot reassign to the same harness")
	}
	drivers := s.Drivers
	if t.Mode == marshal.Governed && s.GovernedDrivers != nil {
		drivers = s.GovernedDrivers
	}
	if d := drivers[worker]; d == nil || d.Mode() != t.Mode {
		return fmt.Errorf("worker %s does not support task mode %s", worker, t.Mode)
	}
	t.Worker = worker
	t.State = marshal.Reassigned
	t.EscalationReason = ""
	if t.ReturnsByAgent == nil {
		t.ReturnsByAgent = map[string]int{}
	}
	t.ReturnsByAgent[worker] = 0
	if run.CloseAuthorization != nil {
		run.CloseAuthorization.Voided = true
	}
	unpauseIfNoEscalated(&run)
	if err = s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	if err = s.record(ctx, runID, taskID, events.EventTypeMarshalTaskReassigned, map[string]any{"worker": worker}); err != nil {
		return err
	}
	_, err = s.Charge(ctx, runID, taskID, "reassign", marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}})
	return err
}

func (s *MarshalService) RetryTask(ctx context.Context, runID, taskID string, freshSession bool) error {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	if err = unfinishedMarshalOperation(run); err != nil {
		return err
	}
	i := taskIndex(run, taskID)
	if i < 0 {
		return errors.New("task not found")
	}
	t := &run.Tasks[i]
	if t.State != marshal.Escalated {
		return errors.New("only escalated tasks can be retried")
	}
	if isPermanentEscalationReason(t.EscalationReason) {
		return fmt.Errorf("cannot retry task with escalation reason: %s", t.EscalationReason)
	}
	hasOtherWorker := s.otherWorker(t.Worker, t.Mode) != ""
	if !hasOtherWorker {
		if !freshSession {
			return errors.New("single provider retry requires a fresh session")
		}
		if t.FreshSessionRetries >= 2 {
			return errors.New("maximum fresh session retries exceeded")
		}
		t.FreshSessionRetries++
	} else if freshSession {
		if t.FreshSessionRetries >= 2 {
			return errors.New("maximum fresh session retries exceeded")
		}
		t.FreshSessionRetries++
	}
	if err := marshal.TransitionTask(t.State, marshal.Returned); err != nil {
		return err
	}
	t.State = marshal.Returned
	t.EscalationReason = ""
	if t.ReturnsByAgent != nil {
		t.ReturnsByAgent[t.Worker] = 0
	}
	if run.CloseAuthorization != nil {
		run.CloseAuthorization.Voided = true
	}
	unpauseIfNoEscalated(&run)
	if err = s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	return s.record(ctx, runID, taskID, events.EventTypeMarshalTaskReturned, map[string]any{"retry": true, "fresh_session": freshSession})
}

func (s *MarshalService) CancelTask(ctx context.Context, runID, taskID string) error {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	if err = unfinishedMarshalOperation(run); err != nil {
		return err
	}
	i := taskIndex(run, taskID)
	if i < 0 {
		return errors.New("task not found")
	}
	t := &run.Tasks[i]
	if t.State != marshal.Escalated {
		return errors.New("only escalated tasks can be cancelled")
	}
	if isPermanentEscalationReason(t.EscalationReason) {
		return fmt.Errorf("cannot cancel task with escalation reason: %s", t.EscalationReason)
	}
	if err := marshal.TransitionTask(t.State, marshal.Cancelled); err != nil {
		return err
	}
	t.State = marshal.Cancelled
	if run.CloseAuthorization != nil {
		run.CloseAuthorization.Voided = true
	}
	allDone := true
	for _, task := range run.Tasks {
		if task.State != marshal.Merged && task.State != marshal.Cancelled {
			allDone = false
		}
	}
	if allDone {
		run.State = marshal.Verifying
		run.Pause = nil
	} else {
		unpauseIfNoEscalated(&run)
	}
	if err = s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	return s.record(ctx, runID, taskID, events.EventTypeMarshalTaskCancelled, map[string]any{"reason": "cancelled by operator"})
}

func (s *MarshalService) Merge(ctx context.Context, runID, taskID string) error {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	i := taskIndex(run, taskID)
	if i < 0 || run.Tasks[i].State != marshal.Accepted {
		return errors.New("task is not accepted")
	}
	for j := 0; j < i; j++ {
		if run.Tasks[j].State != marshal.Merged && run.Tasks[j].State != marshal.Cancelled {
			return errors.New("merge order violation")
		}
	}
	accepted := run.Tasks[i].ResultCommit
	if err := s.checkHoneypotAdmission(ctx, run.Tasks[i], accepted); err != nil {
		return err
	}
	if !marshalCommitPattern.MatchString(accepted) {
		return errors.New("accepted result commit is missing or invalid")
	}
	if _, err = gitMarshal(ctx, s.Repository, "cat-file", "-e", accepted+"^{commit}"); err != nil {
		return err
	}
	if err := s.checkGoverningIntegrity(ctx, runID, run); err != nil {
		return err
	}
	if err := s.approveReservedMerge(ctx, runID, taskID, run, rev); err != nil {
		return err
	}
	branch := integrationBranch(runID, run)
	dir := filepath.Join(s.Worktrees, integrationTaskID(runID, run))
	before := run.BaseCommit
	if i > 0 {
		before, err = gitMarshal(ctx, dir, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
	}
	next := run
	next.Tasks = append([]marshal.Task(nil), run.Tasks...)
	next.Tasks[i].State = marshal.Merged
	next.State = marshal.Merging
	all := true
	for _, t := range next.Tasks {
		if t.State != marshal.Merged && t.State != marshal.Cancelled {
			all = false
		}
	}
	if all {
		next.State = marshal.Verifying
	}
	op := &marshal.LifecycleOperation{Kind: "merge", TaskID: taskID, Dir: dir, Target: "refs/heads/" + branch, Before: before, After: accepted, Next: &next, Event: events.Event{Type: events.EventTypeMarshalTaskMerged}}
	rev, err = s.beginOperation(ctx, runID, run, rev, op)
	if err != nil {
		return err
	}
	effectCompleted := false
	defer func() {
		if !effectCompleted {
			s.abandonOperation(ctx, runID, op)
		}
	}()

	if i == 0 {
		if _, err = worktree.New(s.Repository, s.Worktrees).Prepare(ctx, model.WorktreeRequest{TaskID: integrationTaskID(runID, run), Branch: branch, BaseCommit: run.BaseCommit}); err != nil {
			return err
		}
	}
	// A merge driver named in .gitattributes runs a configured command during
	// the merge. A hand-in that adds one is returned instead of merged, so a
	// worker cannot choose code for the runtime to run.
	if sets, derr := marshalSetsMergeDriver(ctx, dir, accepted); derr != nil {
		return derr
	} else if sets {
		err = errors.New("the hand-in declares a merge driver in .gitattributes")
	} else {
		_, err = gitMarshal(ctx, dir, "merge", "--no-ff", "-m", "marshal: merge "+taskID+"\n\nMarshal-Operation: "+op.ID, accepted)
	}
	if err != nil {
		_, _ = gitMarshal(ctx, dir, "merge", "--abort")
		head, headErr := gitMarshal(ctx, dir, "rev-parse", "HEAD")
		if headErr != nil {
			return headErr
		}
		run.Tasks[i].BaseCommit = head
		run.State = marshal.Reviewing
		attempt := 1 + run.Tasks[i].EvidenceAttemptBase
		for _, n := range run.Tasks[i].ReturnsByAgent {
			attempt += n
		}
		stored, reviewErr := s.Store.GetMarshalReview(ctx, runID, taskID, attempt)
		if reviewErr != nil {
			return reviewErr
		}
		review := stored.Value
		review.Reasons = append(review.Reasons, "merge refused: "+err.Error())
		_, err = s.finishMarshalReturn(ctx, runID, run, rev, i, review, map[string]any{"base_commit": head})
		return err
	}
	effectCompleted = true
	if err = s.afterOperationEffect(op); err != nil {
		return err
	}
	return s.completeOperation(ctx, runID, rev, op)
}

func (s *MarshalService) VerifyMerged(ctx context.Context, runID string, charge marshal.Charge) (verification.Decision, error) {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return verification.Blocked, err
	}
	if err = unfinishedMarshalOperation(run); err != nil {
		return verification.Blocked, err
	}
	if run.State != marshal.Verifying || s.Verify == nil {
		return verification.Blocked, errors.New("run is not ready for verification")
	}
	if err = s.requireIndependentVerifier(ctx, run); err != nil {
		return verification.Blocked, err
	}
	head, err := gitMarshal(ctx, filepath.Join(s.Worktrees, integrationTaskID(runID, run)), "rev-parse", "HEAD")
	if err != nil {
		return verification.Blocked, err
	}
	session, binding, err := s.Verify(ctx, run, head)
	if err != nil {
		return verification.Blocked, err
	}
	result := verification.Evaluate(session, binding, s.clock())
	if run.Tier == marshal.Ultra && result == verification.VerifiedComplete {
		if err = s.captureIndependentVerification(ctx, runID, run, head, session); err != nil {
			return verification.Blocked, err
		}
	}
	budget, err := s.Charge(ctx, runID, "", "verification", charge)
	if err != nil {
		return result, err
	}
	if len(budget.PlanExceeded) > 0 || budgetNeedsOperator(run.Budget, budget) {
		paused, pausedRev, loadErr := s.load(ctx, runID)
		if loadErr != nil {
			return result, loadErr
		}
		pauseMarshalBudget(&paused, budget)
		if loadErr = s.save(ctx, runID, paused, pausedRev); loadErr != nil {
			return result, loadErr
		}
		return result, nil
	}
	if result != verification.VerifiedComplete {
		pauseMarshal(&run, "verification failed: "+string(result), "/marshal resume to retry verification", marshal.Verifying)
	}
	if err = s.save(ctx, runID, run, rev); err != nil {
		return result, err
	}
	return result, nil
}

func (s *MarshalService) Close(ctx context.Context, runID string) error {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	if err = unfinishedMarshalOperation(run); err != nil {
		return err
	}
	if run.State != marshal.Verifying {
		return errors.New("run is not verified")
	}
	if err := s.checkGoverningIntegrity(ctx, runID, run); err != nil {
		return err
	}
	if s.Verify == nil {
		return errors.New("verifier is unavailable")
	}
	if err = s.requireIndependentVerifier(ctx, run); err != nil {
		return err
	}
	project, err := s.Store.Project(ctx)
	if err != nil {
		return err
	}
	if project.DefaultBranch == "" {
		return errors.New("project target branch is missing")
	}
	if err = s.validateDelivery(ctx, run); err != nil {
		return err
	}
	dir := filepath.Join(s.Worktrees, integrationTaskID(runID, run))
	head, err := gitMarshal(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	session, binding, err := s.Verify(ctx, run, head)
	if err != nil || verification.Evaluate(session, binding, s.clock()) != verification.VerifiedComplete {
		return errors.New("integrated result is not verified")
	}
	if run.Tier == marshal.Ultra {
		if err = s.captureIndependentVerification(ctx, runID, run, head, session); err != nil {
			return err
		}
	}
	// What moves the target must be exactly the commit that was verified.
	if after, err := gitMarshal(ctx, dir, "rev-parse", "HEAD"); err != nil || after != head {
		return errors.New("the integration branch moved during verification")
	}
	env, state, err := s.gateInputs(ctx, runID, "close", run, constitution.DomainProjectMutation)
	if err != nil {
		return err
	}
	// The checkpoint is real: the target's current commit is recorded as a ref
	// before the target moves, so the close is undone by moving the target
	// back to it. A checkpoint ID with nothing behind it would satisfy the
	// mutating-domain rule on paper only.
	target := "refs/heads/" + project.DefaultBranch
	old, err := gitMarshal(ctx, s.Repository, "rev-parse", target)
	if err != nil {
		return err
	}
	if old != run.BaseCommit {
		return errors.New("approved delivery base changed; review and approve again")
	}
	if err = s.validateDelivery(ctx, run); err != nil {
		return err
	}
	checkpoint := "refs/marshal/" + runID + "/pre-close"
	if run.ArtifactRevision > 0 {
		checkpoint = fmt.Sprintf("refs/marshal/%s/r%d/pre-close", runID, run.ArtifactRevision)
	}
	next := run
	next.State = marshal.Closed
	op := &marshal.LifecycleOperation{Kind: "close", Before: old, After: head, Target: target, Next: &next, Event: events.Event{Type: events.EventTypeMarshalRunClosed, Data: map[string]any{"target": project.DefaultBranch, "commit": head, "checkpoint": checkpoint, "previous_commit": old}}}
	rev, err = s.beginOperation(ctx, runID, run, rev, op)
	if err != nil {
		return err
	}
	effectCompleted := false
	defer func() {
		if !effectCompleted {
			s.abandonOperation(ctx, runID, op)
		}
	}()

	if _, err = gitMarshal(ctx, s.Repository, "update-ref", checkpoint, old); err != nil {
		return err
	}
	env.Reversibility = constitution.ReversibleInternal
	env.CheckpointID = checkpoint + "@" + old
	userApproval := ""
	if !run.ValidCloseAuthorization() {
		if s.ApprovalActor == nil {
			return errors.New("user approval source is unavailable")
		}
		userApproval, err = s.ApprovalActor(ctx, runID, "close")
		if err != nil {
			return err
		}
	}
	gateRun := run
	if userApproval != "" {
		gateRun.Settings.AcceptanceMode = marshal.AcceptUser
	}
	gate := constitution.EvaluateMarshalClose(constitution.Default(), env, state, gateRun, userApproval)
	if err := s.recordConstitutionalVerdict(ctx, env, gate); err != nil {
		return err
	}
	if !gate.Outcome.Permits() {
		return fmt.Errorf("close gate: %s", gate.Reason)
	}
	if err = s.validateDelivery(ctx, run); err != nil {
		return err
	}
	// Recovery may finish a close only after this exact intent passed the gate.
	op.Authorized = true
	pending := run
	pending.Operation = op
	if err = s.save(ctx, runID, pending, rev); err != nil {
		return err
	}
	rev++
	if _, err = gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", old, head); err != nil {
		return errors.New("target cannot fast-forward")
	}
	checkedOut, err := marshalTargetWorktree(ctx, s.Repository, target)
	if err != nil {
		return err
	}
	if checkedOut != "" {
		refusal := fmt.Errorf("target branch %s is checked out at %s; run `git switch --detach` from that worktree to switch it away before the run can close", project.DefaultBranch, checkedOut)
		// Only the main worktree has the same Git and common directories.
		// A linked worktree must still be switched away by its operator.
		dirs, dirErr := gitMarshal(ctx, checkedOut, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
		paths := strings.Split(dirs, "\n")
		status, statusErr := gitMarshal(ctx, checkedOut, "status", "--porcelain", "--untracked-files=all")
		if dirErr != nil || len(paths) != 2 || paths[0] != paths[1] || statusErr != nil || status != "" {
			return refusal
		}
		// Recheck the checkpoint and checkout identity before Git updates the
		// branch, index and files together. Git's fast-forward merge refuses
		// divergence and worktree conflicts; hooks stay disabled by gitMarshal.
		branch, branchErr := gitMarshal(ctx, checkedOut, "symbolic-ref", "HEAD")
		current, currentErr := gitMarshal(ctx, checkedOut, "rev-parse", "HEAD")
		if branchErr != nil || branch != target || currentErr != nil || current != old {
			return refusal
		}
		if _, err = gitMarshal(ctx, checkedOut, "merge", "--ff-only", "--no-edit", "--no-overwrite-ignore", head); err != nil {
			return fmt.Errorf("target fast-forward failed: %w", err)
		}
		if after, err := gitMarshal(ctx, checkedOut, "rev-parse", "HEAD"); err != nil || after != head {
			return errors.New("the target branch moved during close")
		}
	} else {
		// An unchecked target moves with compare-and-swap so a concurrent
		// advance since the checkpoint cannot be overwritten.
		if _, err = gitMarshal(ctx, s.Repository, "update-ref", target, head, old); err != nil {
			return fmt.Errorf("the target branch moved since the checkpoint was taken: %w", err)
		}
	}
	effectCompleted = true
	if err = s.afterOperationEffect(op); err != nil {
		return err
	}
	return s.completeOperation(ctx, runID, rev, op)
}

func (s *MarshalService) Amend(ctx context.Context, runID, reason string) (marshal.Run, error) {
	d, _, err := s.ProposeAmend(ctx, runID, reason)
	if err != nil {
		return marshal.Run{}, err
	}
	return s.ApplyAmendDraft(ctx, runID, reason, d)
}

// ProposeAmend computes and validates an amendment without changing the run.
// The UI can show a major proposal and discard it when the user denies it.
func (s *MarshalService) ProposeAmend(ctx context.Context, runID, reason string) (MarshalDraft, bool, error) {
	run, _, err := s.load(ctx, runID)
	if err != nil {
		return MarshalDraft{}, false, err
	}
	if s.Model == nil || strings.TrimSpace(reason) == "" {
		return MarshalDraft{}, false, errors.New("missing amendment input")
	}
	d, err := s.Model.Amend(ctx, run, reason)
	if err != nil {
		return MarshalDraft{}, false, err
	}
	if err = validateDraft(d); err != nil {
		return MarshalDraft{}, false, err
	}
	if err = instructionsPresent(run.Settings, d.Tasks); err != nil {
		return MarshalDraft{}, false, err
	}
	p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return MarshalDraft{}, false, err
	}
	if err = validateAmendModes(run, p, d); err != nil {
		return MarshalDraft{}, false, err
	}
	_, err = p.AmendScoped(p.Version, reason, d.Plan)
	return d, err != nil, nil
}

// ApplyAmendDraft rechecks the proposal against the current plan revision.
func (s *MarshalService) ApplyAmendDraft(ctx context.Context, runID, reason string, d MarshalDraft) (marshal.Run, error) {
	return s.ApplyAmendDraftBound(ctx, runID, reason, d, 0)
}

func (s *MarshalService) ApplyAmendDraftBound(ctx context.Context, runID, reason string, d MarshalDraft, expectedPlanVersion int64) (marshal.Run, error) {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return run, err
	}
	if err = unfinishedMarshalOperation(run); err != nil {
		return run, err
	}
	if expectedPlanVersion != 0 && run.PlanVersion != expectedPlanVersion {
		return run, errors.New("amendment proposal is stale")
	}
	if strings.TrimSpace(reason) == "" {
		return run, errors.New("missing amendment input")
	}
	if err = validateDraft(d); err != nil {
		return run, err
	}
	if err = instructionsPresent(run.Settings, d.Tasks); err != nil {
		return run, err
	}
	p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return run, err
	}
	if err = validateAmendModes(run, p, d); err != nil {
		return run, err
	}
	amended, err := p.AmendScoped(p.Version, reason, d.Plan)
	major := err != nil
	if major {
		amended, err = p.Revise(p.Version, reason, s.clock())
		if err != nil {
			return run, err
		}
		amended.Tasks = d.Plan.Tasks
		amended.Graph = d.Plan.Graph
		amended.Checks = d.Plan.Checks
		amended.Budget = d.Plan.Budget
		amended.State = plan.StateReady
		amended.ApprovedScope = nil
		amended.ApprovalScopeDigest = ""
		run.ArtifactRevision = amended.Version
		run.Pause = nil
		run.Tasks = d.Tasks
		for i := range run.Tasks {
			run.Tasks[i].EvidenceAttemptBase, err = s.Store.MarshalLastAttempt(ctx, runID, run.Tasks[i].PlanTaskID)
			if err != nil {
				return run, err
			}
			run.Tasks[i].ReturnsByAgent = nil
			run.Tasks[i].State = marshal.Queued
			run.Tasks[i].BaseCommit = run.BaseCommit
			run.Tasks[i].ResultCommit = ""
			run.Tasks[i].Branch = fmt.Sprintf("marshal/%s/r%d/%s", runID, amended.Version, run.Tasks[i].PlanTaskID)
		}
		run.State = marshal.Drafting
		if run.CloseAuthorization != nil {
			run.CloseAuthorization.Voided = true
		}
	}
	if !major {
		for _, task := range d.Tasks {
			if !marshalIdentifier(task.PlanTaskID) {
				return run, errors.New("invalid task ID")
			}
			parent := taskIndex(run, task.PlanTaskID)
			if parent < 0 {
				parent = taskIndex(run, d.Plan.ParentTaskIDs[task.PlanTaskID])
				if parent < 0 || run.Tasks[parent].State != marshal.Queued {
					return run, errors.New("a scoped split requires a queued parent")
				}
			}
			for _, check := range task.Checks {
				approved := false
				for _, old := range run.Tasks[parent].Checks {
					if check.Command != old.Command {
						continue
					}
					approved = true
					for _, criterion := range check.Criteria {
						if !containsMarshal(old.Criteria, criterion) {
							approved = false
						}
					}
					if approved {
						break
					}
				}
				if !approved {
					return run, errors.New("a scoped amendment cannot expand a check's criterion mapping")
				}
			}
		}
	}
	run.PlanVersion = amended.Version
	if !major {
		previous := run.Tasks
		run.Tasks = d.Tasks
		for i := range run.Tasks {
			run.Tasks[i].State = marshal.Queued
			run.Tasks[i].Branch = "marshal/" + runID + "/" + run.Tasks[i].PlanTaskID
			if run.ArtifactRevision > 0 {
				run.Tasks[i].Branch = fmt.Sprintf("marshal/%s/r%d/%s", runID, run.ArtifactRevision, run.Tasks[i].PlanTaskID)
			}
			run.Tasks[i].BaseCommit = run.BaseCommit
			run.Tasks[i].ResultCommit = ""
			run.Tasks[i].ReturnsByAgent = nil
			for _, old := range previous {
				if old.PlanTaskID == run.Tasks[i].PlanTaskID {
					run.Tasks[i].State = old.State
					run.Tasks[i].Branch = old.Branch
					run.Tasks[i].BaseCommit = old.BaseCommit
					run.Tasks[i].ResultCommit = old.ResultCommit
					run.Tasks[i].ReturnsByAgent = old.ReturnsByAgent
					run.Tasks[i].EvidenceAttemptBase = old.EvidenceAttemptBase
					break
				}
			}
		}
		run.ApprovalScopeDigest = marshalApprovalDigest(amended.ApprovalScopeDigest, run)
		if run.CloseAuthorization != nil && run.CloseAuthorization.ApprovalScopeDigest != run.ApprovalScopeDigest {
			run.CloseAuthorization.Voided = true
		}
	}
	event, err := s.decisionEvent(runID, "", events.EventTypeMarshalPlanAmended, map[string]any{"major": major})
	if err != nil {
		return run, err
	}
	return run, s.Store.SaveMarshalPlanState(ctx, s.ProjectID, runID, amended, p.Version, run, rev, &event)
}

// A replacement worker or split child must keep the approved execution mode.
func validateAmendModes(run marshal.Run, current plan.ExecutionPlan, next MarshalDraft) error {
	modes := map[string]marshal.WorkerMode{}
	for _, task := range run.Tasks {
		modes[task.PlanTaskID] = task.Mode
	}
	for _, task := range run.Tasks {
		if parent := current.ParentTaskIDs[task.PlanTaskID]; parent != "" {
			if mode, ok := modes[parent]; ok && mode != task.Mode {
				return errors.New("approved children have inconsistent execution modes")
			}
			modes[parent] = task.Mode
		}
	}
	for _, task := range next.Tasks {
		id := task.PlanTaskID
		if _, ok := modes[id]; !ok {
			id = next.Plan.ParentTaskIDs[id]
		}
		if mode, ok := modes[id]; ok && mode != task.Mode {
			return errors.New("amendment changes an approved execution mode; create a new plan")
		}
	}
	return nil
}

func (s *MarshalService) Escalate(ctx context.Context, runID, taskID, reason string) error {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	if err = unfinishedMarshalOperation(run); err != nil {
		return err
	}
	if reason == "" {
		return errors.New("escalation reason required")
	}
	if taskID != "" {
		i := taskIndex(run, taskID)
		if i < 0 {
			return model.ErrNotFound
		}
		run.Tasks[i].State = marshal.Escalated
		run.Tasks[i].EscalationReason = reason
	}
	pauseMarshal(&run, reason, "resolve the escalation or amend the plan", "")
	if err = s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	return s.record(ctx, runID, taskID, events.EventTypeMarshalEscalated, map[string]any{"reason": reason})
}

func (s *MarshalService) Resume(ctx context.Context, runID string) (marshal.Run, error) {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return run, err
	}
	if err := s.executionAdmission(ctx, runID, "", run); err != nil {
		return run, err
	}
	run, rev, err = s.recoverOperation(ctx, runID, run, rev)
	if err != nil {
		return run, err
	}
	if model, ok := s.Model.(interface{ SetMarshalConversationID(string) }); ok {
		decisions, err := s.Store.MarshalDecisions(ctx, runID)
		if err != nil {
			return run, err
		}
		for _, event := range decisions {
			if id, ok := event.Data["model_session_id"].(string); ok && id != "" {
				model.SetMarshalConversationID(id)
			}
		}
	}
	if run.State == marshal.AwaitingUser {
		if run.Pause == nil || run.Pause.ResumeState == "" {
			return run, marshalPauseError(run)
		}
		run.State = run.Pause.ResumeState
		run.Pause = nil
		if err = s.save(ctx, runID, run, rev); err != nil {
			return run, err
		}
		rev++
	}
	wm := worktree.New(s.Repository, s.Worktrees)
	for i := range run.Tasks {
		t := &run.Tasks[i]
		if t.ResultCommit != "" && t.State != marshal.Merged {
			_, err = wm.Resume(ctx, model.WorktreeRequest{TaskID: taskArtifactID(runID, *t), Branch: t.Branch, BaseCommit: t.ResultCommit})
			if err != nil {
				return run, err
			}
		}
		if t.State == marshal.Dispatched {
			_, err = wm.Prepare(ctx, model.WorktreeRequest{TaskID: taskArtifactID(runID, *t), Branch: t.Branch, BaseCommit: t.BaseCommit})
			if err != nil {
				pauseMarshal(&run, "interrupted worker worktree: "+err.Error(), "inspect retained worktree and amend the plan", "")
			} else if run.State != marshal.AwaitingUser {
				run.State = marshal.Reviewing
			}
			if _, err = s.finishMarshalReturn(ctx, runID, run, rev, i, marshal.Review{Reviewer: "marshal-runtime", Reasons: []string{"worker interrupted by restart"}}, nil); err != nil {
				return run, err
			}
			run, rev, err = s.load(ctx, runID)
			if err != nil {
				return run, err
			}
		}
	}
	return run, nil
}

func marshalTargetWorktree(ctx context.Context, repository, target string) (string, error) {
	output, err := gitMarshal(ctx, repository, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	path := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			path = strings.TrimPrefix(line, "worktree ")
		}
		if line == "branch "+target {
			return path, nil
		}
	}
	return "", nil
}

var marshalCommitPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

var marshalMergeAttr = regexp.MustCompile(`(^|\s)merge=`)

// marshalSetsMergeDriver reports whether branch changes a .gitattributes file,
// relative to the integration head, so that it names a merge driver.
func marshalSetsMergeDriver(ctx context.Context, dir, branch string) (bool, error) {
	names, err := gitMarshal(ctx, dir, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "HEAD..."+branch)
	if err != nil {
		return false, err
	}
	for _, name := range strings.Split(names, "\n") {
		if filepath.Base(strings.TrimSpace(name)) != ".gitattributes" {
			continue
		}
		content, err := gitMarshal(ctx, dir, "show", branch+":"+strings.TrimSpace(name))
		if err != nil {
			continue // deleted in the hand-in
		}
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && marshalMergeAttr.MatchString(line) {
				return true, nil
			}
		}
	}
	return false, nil
}

func zeroMarshalCharge() marshal.Charge {
	return marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}}
}

func (s *MarshalService) dispatchedTier(ctx context.Context, runID, taskID string) (marshal.Tier, error) {
	history, err := s.Store.MarshalDecisions(ctx, runID)
	if err != nil {
		return "", err
	}
	tier := marshal.Tier("")
	for _, event := range history {
		if event.TaskID != taskID || event.Type != events.EventTypeMarshalTaskDispatched {
			continue
		}
		if value, ok := event.Data["tier"].(string); ok {
			tier = marshal.Tier(value)
		}
	}
	if tier != marshal.Standard && tier != marshal.Ultra {
		return "", errors.New("dispatch tier is missing")
	}
	return tier, nil
}

func (s *MarshalService) requireIndependentVerifier(ctx context.Context, run marshal.Run) error {
	if run.Tier != marshal.Ultra {
		return nil
	}
	if s.VerifierProvider == nil || s.IndependentVerify == nil || s.ModelProvider == "" {
		return errors.New("independent verifier is unavailable")
	}
	provider, err := s.VerifierProvider(ctx, run)
	if err != nil {
		return err
	}
	// Independence comes from the verifier's own fresh session, not from its
	// provider: with a single installed provider the Marshal's serves again.
	if provider == "" {
		return errors.New("independent verifier is unavailable")
	}
	return nil
}
