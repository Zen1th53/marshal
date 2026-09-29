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
	"github.com/Zen1th53/marshal/internal/worktree"
)

type MarshalDispatch struct {
	Driver  driver.Driver
	Handle  *driver.Handle
	TaskID  string
	Started time.Time
}

func (s *MarshalService) Dispatch(ctx context.Context, runID, taskID, brief string) (MarshalDispatch, error) {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return MarshalDispatch{}, err
	}
	if run.State != marshal.Approved && run.State != marshal.Dispatching && run.State != marshal.Reviewing && run.State != marshal.Merging {
		return MarshalDispatch{}, errors.New("dispatch is paused")
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
	if d == nil {
		return MarshalDispatch{}, fmt.Errorf("no driver for %s", t.Worker)
	}
	if d.Mode() != t.Mode {
		return MarshalDispatch{}, fmt.Errorf("worker %s has %s driver, task requires %s", t.Worker, d.Mode(), t.Mode)
	}
	if run.Process05Bound && t.Mode == marshal.Governed && t.ResultCommit == "" && i > 0 {
		integration := filepath.Join(s.Worktrees, "TASK-"+runID+"-integration")
		if head, headErr := gitMarshal(ctx, integration, "rev-parse", "HEAD"); headErr == nil {
			t.BaseCommit = head
		} else if run.Tasks[i-1].State == marshal.Merged {
			return MarshalDispatch{}, fmt.Errorf("governed task base is unavailable: %w", headErr)
		}
	}
	wt := worktree.New(s.Repository, s.Worktrees)
	request := model.WorktreeRequest{TaskID: worktreeTaskID(runID, taskID), Branch: t.Branch, BaseCommit: t.BaseCommit}
	var tree model.Worktree
	if t.ResultCommit != "" {
		request.BaseCommit = t.ResultCommit
		tree, err = wt.Resume(ctx, request)
	} else {
		tree, err = wt.Prepare(ctx, request)
	}
	if err != nil {
		return MarshalDispatch{}, err
	}
	handle, err := d.Launch(ctx, driver.Request{Task: *t, Worktree: tree.Path, Brief: brief})
	if err != nil {
		return MarshalDispatch{}, err
	}
	t.State = marshal.Dispatched
	run.State = marshal.Dispatching
	run.Tier = policy.Tier
	if err = s.save(ctx, runID, run, rev); err != nil {
		_ = d.Cancel(handle)
		return MarshalDispatch{}, err
	}
	// The brief is recorded as sent, so what a worker was told can be read
	// back beside what it handed in.
	briefSum := sha256.Sum256([]byte(brief))
	if err = s.record(ctx, runID, taskID, events.EventTypeMarshalTaskDispatched, map[string]any{"tier": string(policy.Tier), "worker": t.Worker, "brief": brief, "brief_sha256": hex.EncodeToString(briefSum[:])}); err != nil {
		_ = d.Cancel(handle)
		return MarshalDispatch{}, err
	}
	if _, err = s.Charge(ctx, runID, taskID, "dispatch", marshal.Charge{Tokens: marshal.Amount{}, Money: marshal.Amount{}}); err != nil {
		_ = d.Cancel(handle)
		return MarshalDispatch{}, err
	}
	return MarshalDispatch{d, handle, taskID, s.clock()}, nil
}
func worktreeTaskID(runID, taskID string) string {
	return "TASK-" + strings.NewReplacer("/", "-", " ", "-").Replace(runID+"-"+taskID)
}

func (s *MarshalService) CollectHandIn(ctx context.Context, runID string, dispatch MarshalDispatch) (marshal.HandIn, error) {
	if dispatch.Driver == nil || dispatch.Handle == nil {
		return marshal.HandIn{}, errors.New("missing dispatch handle")
	}
	handin, err := dispatch.Driver.Wait(ctx, dispatch.Handle)
	if err != nil {
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
	if err = marshal.ValidateHandIn(*t, handin); err != nil {
		return handin, err
	}
	attempt := 1
	for _, n := range t.ReturnsByAgent {
		attempt += n
	}
	if _, err = s.Store.SetMarshalHandIn(ctx, runID, dispatch.TaskID, attempt, handin); err != nil {
		return handin, err
	}
	t.ResultCommit = handin.ResultCommit
	t.State = marshal.HandedIn
	run.State = marshal.Reviewing
	if err = s.save(ctx, runID, run, rev); err != nil {
		return handin, err
	}
	if err = s.record(ctx, runID, dispatch.TaskID, events.EventTypeMarshalTaskHandedIn, map[string]any{"result_commit": handin.ResultCommit}); err != nil {
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
	if len(budget.PlanExceeded) > 0 || len(budget.TaskExceeded) > 0 {
		run, rev, err = s.load(ctx, runID)
		if err != nil {
			return handin, err
		}
		if len(budget.PlanExceeded) > 0 {
			run.State = marshal.AwaitingUser
		} else {
			run.Tasks[i].State = marshal.Returned
			if run.Tasks[i].ReturnsByAgent == nil {
				run.Tasks[i].ReturnsByAgent = map[string]int{}
			}
			run.Tasks[i].ReturnsByAgent[run.Tasks[i].Worker]++
		}
		if err = s.save(ctx, runID, run, rev); err != nil {
			return handin, err
		}
		if len(budget.PlanExceeded) > 0 {
			return handin, s.record(ctx, runID, dispatch.TaskID, events.EventTypeMarshalEscalated, map[string]any{"reason": "plan budget exceeded"})
		}
		if err = s.record(ctx, runID, dispatch.TaskID, events.EventTypeMarshalTaskReturned, map[string]any{"reason": "task budget exceeded"}); err != nil {
			return handin, err
		}
		_, err = s.Charge(ctx, runID, dispatch.TaskID, "return", zeroMarshalCharge())
		return handin, err
	}
	return handin, nil
}

func (s *MarshalService) Review(ctx context.Context, runID, taskID string, charge marshal.Charge) (marshal.Verdict, error) {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return "", err
	}
	i := taskIndex(run, taskID)
	if i < 0 || run.Tasks[i].State != marshal.HandedIn {
		return "", errors.New("task has no hand-in")
	}
	t := &run.Tasks[i]
	attempt := 1
	for _, n := range t.ReturnsByAgent {
		attempt += n
	}
	stored, err := s.Store.GetMarshalHandIn(ctx, runID, taskID, attempt)
	if err != nil {
		return "", err
	}
	h := stored.Value
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
	met, total, _ := marshal.CriteriaMet(*t, h)
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
		userApproval, err = s.ApprovalActor(ctx, runID, taskID)
		if err != nil {
			return "", err
		}
	}
	verdict := constitution.EvaluateTaskAcceptance(constitution.Default(), envelope, state, constitution.TaskAcceptance{Mode: run.Settings.AcceptanceMode, MarshalVerdictAccept: proposal.Verdict == marshal.VerdictAccept, UserApprovalActor: userApproval, Executor: h.Worker, Reviewer: s.Reviewer, ResultCommit: h.ResultCommit, EvidenceCommit: h.ResultCommit, CriteriaMet: met, CriteriaTotal: total, IndependentReviewDone: !policy.CrossReviewRequired || crossReviewAccepted})
	proposal.Reviewer = s.Reviewer
	if _, err = s.Store.SetMarshalReview(ctx, runID, taskID, attempt, proposal); err != nil {
		return "", err
	}
	result := proposal.Verdict
	if !verdict.Outcome.Permits() {
		result = marshal.VerdictReturn
	}
	if result == marshal.VerdictAccept {
		t.State = marshal.Accepted
	} else {
		if t.ReturnsByAgent == nil {
			t.ReturnsByAgent = map[string]int{}
		}
		t.ReturnsByAgent[t.Worker]++
		result = marshal.NextAfterReturn(*t, run.Settings.ReworkLimit)
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
			run.State = marshal.AwaitingUser
		}
	}
	budget, err := s.Charge(ctx, runID, taskID, "review", charge)
	if err != nil {
		return "", err
	}
	if len(budget.PlanExceeded) > 0 {
		run.State = marshal.AwaitingUser
	}
	if (len(budget.TaskExceeded) > 0 || (run.Budget.Tokens.Task > 0 && containsMarshal(budget.Unknown, "tokens"))) && t.State == marshal.Accepted {
		t.State = marshal.Returned
		if t.ReturnsByAgent == nil {
			t.ReturnsByAgent = map[string]int{}
		}
		t.ReturnsByAgent[t.Worker]++
		result = marshal.VerdictReturn
	}
	if err = s.save(ctx, runID, run, rev); err != nil {
		return "", err
	}
	kind := events.EventTypeMarshalTaskReturned
	if result == marshal.VerdictAccept {
		kind = events.EventTypeMarshalTaskAccepted
	} else if result == marshal.VerdictReassign {
		kind = events.EventTypeMarshalTaskReassigned
	} else if result == marshal.VerdictEscalate {
		kind = events.EventTypeMarshalEscalated
	}
	eventData := map[string]any{"gate": string(verdict.Outcome), "reason": string(verdict.Reason)}
	for key, value := range crossReviewData {
		eventData[key] = value
	}
	if err = s.record(ctx, runID, taskID, kind, eventData); err != nil {
		return result, err
	}
	if result == marshal.VerdictReturn {
		_, err = s.Charge(ctx, runID, taskID, "return", zeroMarshalCharge())
	}
	return result, err
}

func (s *MarshalService) gateInputs(ctx context.Context, runID, taskID string, run marshal.Run, domain constitution.Domain) (constitution.Envelope, constitution.RuntimeState, error) {
	now := s.clock()
	env := constitution.Envelope{DecisionID: runID + "-" + taskID + "-" + fmt.Sprint(now.UnixNano()), ConstitutionVersion: constitution.Current, Process: 6, ProjectID: s.ProjectID, SessionID: runID, Actor: "marshal-runtime", ActorRole: "orchestrator", Surface: constitution.SurfaceCore, Mode: constitution.ModeStandard, Domain: domain, Action: "marshal " + string(domain), Reversibility: constitution.ReversibleInternal, StateDigest: run.ApprovalScopeDigest, RequestedAt: now}
	if s.GateState == nil {
		return env, constitution.RuntimeState{}, errors.New("constitutional runtime state is unavailable")
	}
	state, err := s.GateState(ctx, runID, taskID)
	state.RuntimeConstitution = constitution.Current
	state.Now = now
	return env, state, err
}

// Charge records known and unknown usage in the durable decision stream.
func (s *MarshalService) Charge(ctx context.Context, runID, taskID, phase string, charge marshal.Charge) (marshal.BudgetResult, error) {
	run, _, err := s.load(ctx, runID)
	if err != nil {
		return marshal.BudgetResult{}, err
	}
	if phase == "" {
		return marshal.BudgetResult{}, errors.New("charge phase is required")
	}
	if err = s.record(ctx, runID, taskID, events.EventTypeMarshalUsageCharged, map[string]any{"charge_phase": phase, "usage_units": charge.Tokens.Value, "usage_known": charge.Tokens.Known, "wall_ns": charge.WallTime.Nanoseconds(), "money": charge.Money.Value, "money_known": charge.Money.Known}); err != nil {
		return marshal.BudgetResult{}, err
	}
	history, err := s.Store.MarshalDecisions(ctx, runID)
	if err != nil {
		return marshal.BudgetResult{}, err
	}
	task, planTotal := marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}}, marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}}
	add := func(to *marshal.Charge, e events.Event) {
		data := e.Data
		if data["charge_phase"] == nil {
			return
		}
		if k, _ := data["usage_known"].(bool); k {
			to.Tokens.Value += int64Number(data["usage_units"])
		} else {
			to.Tokens.Known = false
		}
		if k, _ := data["money_known"].(bool); k {
			to.Money.Value += int64Number(data["money"])
		} else {
			to.Money.Known = false
		}
		to.WallTime += time.Duration(int64Number(data["wall_ns"]))
	}
	for _, e := range history {
		add(&planTotal, e)
		if e.TaskID == taskID {
			add(&task, e)
		}
	}
	result := marshal.CheckBudget(run.Budget, task, planTotal)
	addExceeded := func(dst *[]string, name string, value, ceiling int64) {
		if ceiling <= 0 || value <= ceiling || containsMarshal(*dst, name) {
			return
		}
		*dst = append(*dst, name)
	}
	addExceeded(&result.TaskExceeded, "tokens", task.Tokens.Value, run.Budget.Tokens.Task)
	addExceeded(&result.PlanExceeded, "tokens", planTotal.Tokens.Value, run.Budget.Tokens.Plan)
	addExceeded(&result.TaskExceeded, "money", task.Money.Value, run.Budget.Money.Task)
	addExceeded(&result.PlanExceeded, "money", planTotal.Money.Value, run.Budget.Money.Plan)
	return result, nil
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

func (s *MarshalService) Reassign(ctx context.Context, runID, taskID, worker string) error {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	i := taskIndex(run, taskID)
	if i < 0 || run.Tasks[i].State != marshal.Reassigned || worker == "" || worker == run.Tasks[i].Worker {
		return errors.New("invalid reassignment")
	}
	run.Tasks[i].Worker = worker
	if run.Tasks[i].ReturnsByAgent == nil {
		run.Tasks[i].ReturnsByAgent = map[string]int{}
	}
	run.Tasks[i].ReturnsByAgent[worker] = 0
	if err = s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	if err = s.record(ctx, runID, taskID, events.EventTypeMarshalTaskReassigned, map[string]any{"worker": worker}); err != nil {
		return err
	}
	_, err = s.Charge(ctx, runID, taskID, "reassign", marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}})
	return err
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
		if run.Tasks[j].State != marshal.Merged {
			return errors.New("merge order violation")
		}
	}
	branch := "marshal/" + runID + "/integration"
	dir := filepath.Join(s.Worktrees, "TASK-"+runID+"-integration")
	if i == 0 {
		if _, err = gitMarshal(ctx, s.Repository, "worktree", "add", "-b", branch, dir, run.BaseCommit); err != nil {
			return err
		}
	}
	// A merge driver named in .gitattributes runs a configured command during
	// the merge. A hand-in that adds one is returned instead of merged, so a
	// worker cannot choose code for the runtime to run.
	if sets, derr := marshalSetsMergeDriver(ctx, dir, run.Tasks[i].Branch); derr != nil {
		return derr
	} else if sets {
		err = errors.New("the hand-in declares a merge driver in .gitattributes")
	} else {
		_, err = gitMarshal(ctx, dir, "merge", "--no-ff", "--no-edit", run.Tasks[i].Branch)
	}
	if err != nil {
		_, _ = gitMarshal(ctx, dir, "merge", "--abort")
		head, headErr := gitMarshal(ctx, dir, "rev-parse", "HEAD")
		if headErr != nil {
			return headErr
		}
		run.Tasks[i].State = marshal.Returned
		if run.Tasks[i].ReturnsByAgent == nil {
			run.Tasks[i].ReturnsByAgent = map[string]int{}
		}
		run.Tasks[i].ReturnsByAgent[run.Tasks[i].Worker]++
		run.Tasks[i].BaseCommit = head
		run.State = marshal.Reviewing
		if saveErr := s.save(ctx, runID, run, rev); saveErr != nil {
			return saveErr
		}
		if err = s.record(ctx, runID, taskID, events.EventTypeMarshalTaskReturned, map[string]any{"reason": "merge conflict", "base_commit": head}); err != nil {
			return err
		}
		_, err = s.Charge(ctx, runID, taskID, "return", zeroMarshalCharge())
		return err
	}
	run.Tasks[i].State = marshal.Merged
	run.State = marshal.Merging
	all := true
	for _, t := range run.Tasks {
		if t.State != marshal.Merged {
			all = false
		}
	}
	if all {
		run.State = marshal.Verifying
	}
	if err = s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	return s.record(ctx, runID, taskID, events.EventTypeMarshalTaskMerged, nil)
}

func (s *MarshalService) VerifyMerged(ctx context.Context, runID string, charge marshal.Charge) (verification.Decision, error) {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return verification.Blocked, err
	}
	if run.State != marshal.Verifying || s.Verify == nil {
		return verification.Blocked, errors.New("run is not ready for verification")
	}
	if err = s.requireIndependentVerifier(ctx, run); err != nil {
		return verification.Blocked, err
	}
	head, err := gitMarshal(ctx, filepath.Join(s.Worktrees, "TASK-"+runID+"-integration"), "rev-parse", "HEAD")
	if err != nil {
		return verification.Blocked, err
	}
	session, binding, err := s.Verify(ctx, run, head)
	if err != nil {
		return verification.Blocked, err
	}
	result := verification.Evaluate(session, binding, s.clock())
	if run.Tier == marshal.Ultra && result == verification.VerifiedComplete {
		if err = s.IndependentVerify(ctx, run, head, session); err != nil {
			return verification.Blocked, err
		}
	}
	budget, err := s.Charge(ctx, runID, "", "verification", charge)
	if err != nil {
		return result, err
	}
	if result != verification.VerifiedComplete || len(budget.PlanExceeded) > 0 {
		run.State = marshal.AwaitingUser
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
	if run.State != marshal.Verifying {
		return errors.New("run is not verified")
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
	dir := filepath.Join(s.Worktrees, "TASK-"+runID+"-integration")
	head, err := gitMarshal(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	session, binding, err := s.Verify(ctx, run, head)
	if err != nil || verification.Evaluate(session, binding, s.clock()) != verification.VerifiedComplete {
		return errors.New("integrated result is not verified")
	}
	if run.Tier == marshal.Ultra {
		if err = s.IndependentVerify(ctx, run, head, session); err != nil {
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
	checkpoint := "refs/marshal/" + runID + "/pre-close"
	if _, err = gitMarshal(ctx, s.Repository, "update-ref", checkpoint, old); err != nil {
		return err
	}
	env.Reversibility = constitution.ReversibleInternal
	env.CheckpointID = checkpoint + "@" + old
	userApproval := ""
	if run.Settings.AcceptanceMode != marshal.AcceptMarshal {
		if s.ApprovalActor == nil {
			return errors.New("user approval source is unavailable")
		}
		userApproval, err = s.ApprovalActor(ctx, runID, "close")
		if err != nil {
			return err
		}
	}
	gate := constitution.EvaluateMarshalClose(constitution.Default(), env, state, run, userApproval)
	if !gate.Outcome.Permits() {
		return fmt.Errorf("close gate: %s", gate.Reason)
	}
	if _, err = gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", old, head); err != nil {
		return errors.New("target cannot fast-forward")
	}
	checkedOut, err := marshalTargetWorktree(ctx, s.Repository, target)
	if err != nil {
		return err
	}
	if checkedOut != "" {
		return fmt.Errorf("target branch %s is checked out at %s; switch it away or fast-forward it before the run can close", project.DefaultBranch, checkedOut)
	}
	// The target moves in one compare-and-swap: update-ref succeeds only if
	// the target is still at the checkpointed commit, so no concurrent
	// advance can slip between a check and the move.
	if _, err = gitMarshal(ctx, s.Repository, "update-ref", target, head, old); err != nil {
		return fmt.Errorf("the target branch moved since the checkpoint was taken: %w", err)
	}
	run.State = marshal.Closed
	if err = s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	return s.record(ctx, runID, "", events.EventTypeMarshalRunClosed, map[string]any{"target": project.DefaultBranch, "commit": head, "checkpoint": checkpoint, "previous_commit": old})
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
		run.Tasks = d.Tasks
		for i := range run.Tasks {
			run.Tasks[i].State = marshal.Queued
			run.Tasks[i].BaseCommit = run.BaseCommit
			run.Tasks[i].ResultCommit = ""
			run.Tasks[i].Branch = "marshal/" + runID + "/" + run.Tasks[i].PlanTaskID
		}
		run.State = marshal.Drafting
		if run.CloseAuthorization != nil {
			run.CloseAuthorization.Voided = true
		}
	}
	if err = s.Store.SavePlan(ctx, amended, p.Version); err != nil {
		return run, err
	}
	run.PlanVersion = amended.Version
	if !major {
		run.ApprovalScopeDigest = marshalApprovalDigest(amended.ApprovalScopeDigest, run)
		previous := run.Tasks
		run.Tasks = d.Tasks
		for i := range run.Tasks {
			for _, old := range previous {
				if old.PlanTaskID == run.Tasks[i].PlanTaskID {
					run.Tasks[i].State = old.State
					run.Tasks[i].Branch = old.Branch
					run.Tasks[i].BaseCommit = old.BaseCommit
					run.Tasks[i].ResultCommit = old.ResultCommit
					run.Tasks[i].ReturnsByAgent = old.ReturnsByAgent
					break
				}
			}
		}
	}
	if err = s.save(ctx, runID, run, rev); err != nil {
		return run, err
	}
	return run, s.record(ctx, runID, "", events.EventTypeMarshalPlanAmended, map[string]any{"major": major})
}

func (s *MarshalService) Escalate(ctx context.Context, runID, taskID, reason string) error {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
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
	}
	run.State = marshal.AwaitingUser
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
	wm := worktree.New(s.Repository, s.Worktrees)
	changed := false
	for i := range run.Tasks {
		t := &run.Tasks[i]
		if t.ResultCommit != "" && t.State != marshal.Merged {
			_, err = wm.Resume(ctx, model.WorktreeRequest{TaskID: worktreeTaskID(runID, t.PlanTaskID), Branch: t.Branch, BaseCommit: t.ResultCommit})
			if err != nil {
				return run, err
			}
		}
		if t.State == marshal.Dispatched {
			_, err = wm.Prepare(ctx, model.WorktreeRequest{TaskID: worktreeTaskID(runID, t.PlanTaskID), Branch: t.Branch, BaseCommit: t.BaseCommit})
			if err != nil {
				run.State = marshal.AwaitingUser
			} else if run.State != marshal.AwaitingUser {
				run.State = marshal.Reviewing
			}
			t.State = marshal.Returned
			changed = true
		}
	}
	if changed {
		if err = s.save(ctx, runID, run, rev); err != nil {
			return run, err
		}
		for _, t := range run.Tasks {
			if t.State == marshal.Returned {
				if err = s.record(ctx, runID, t.PlanTaskID, events.EventTypeMarshalTaskReturned, map[string]any{"reason": "worker interrupted by restart"}); err != nil {
					return run, err
				}
			} else if t.State == marshal.Escalated {
				if err = s.record(ctx, runID, t.PlanTaskID, events.EventTypeMarshalEscalated, map[string]any{"reason": "unrecovered worker worktree"}); err != nil {
					return run, err
				}
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
	if provider == "" || strings.EqualFold(provider, s.ModelProvider) {
		return errors.New("independent verifier must use another provider")
	}
	return nil
}
