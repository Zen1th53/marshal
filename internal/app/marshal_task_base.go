package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/worktree"
)

// ErrStaleBase identifies a launch or recovery refusal caused by a changed
// delivery target, unproven or overlapping integration advance, changed
// dependency head, or missing/invalid approved base binding.
var ErrStaleBase = errors.New("stale_base")

// LaunchBaseRefusalError carries the observed approved and current refs,
// private integration head, and recovery route for a stale_base refusal.
type LaunchBaseRefusalError struct {
	Code            string
	TaskID          string
	ApprovedRef     string
	ApprovedCommit  string
	CurrentRef      string
	CurrentCommit   string
	IntegrationRef  string
	IntegrationHead string
	Route           string
	Reason          string
	NextCommands    []string
}

func (e *LaunchBaseRefusalError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{e.Reason}
	if e.ApprovedRef != "" || e.ApprovedCommit != "" {
		parts = append(parts, fmt.Sprintf("approved %s=%s", firstNonEmpty(e.ApprovedRef, "delivery-ref"), firstNonEmpty(e.ApprovedCommit, "unknown")))
	}
	if e.CurrentRef != "" || e.CurrentCommit != "" {
		parts = append(parts, fmt.Sprintf("current %s=%s", firstNonEmpty(e.CurrentRef, "delivery-ref"), firstNonEmpty(e.CurrentCommit, "missing")))
	}
	if e.IntegrationHead != "" && e.IntegrationHead != e.CurrentCommit {
		parts = append(parts, fmt.Sprintf("integration %s=%s", firstNonEmpty(e.IntegrationRef, "integration"), e.IntegrationHead))
	}
	if e.Route != "" {
		parts = append(parts, "route: "+e.Route)
	}
	return strings.Join(parts, "; ")
}

func (e *LaunchBaseRefusalError) Unwrap() []error {
	if e == nil {
		return nil
	}
	return []error{
		ErrStaleBase,
		&DispatchAdmissionError{Admission: DispatchAdmission{
			Allowed: false,
			Code:    AdmissionStaleBase,
			Reason:  e.Error(),
			TaskID:  safeAdmissionTaskID(e.TaskID),
		}},
	}
}

func newStaleBaseRefusal(effects marshal.RefusalEffects, details LaunchBaseRefusalError) error {
	if details.Code == "" {
		details.Code = marshal.RefusalCodeStaleBase
	}
	if len(details.NextCommands) == 0 {
		details.NextCommands = []string{"/marshal amend", "/marshal use-plan"}
	}
	return marshal.NewRefusalError(marshal.RefusalReceipt{
		Code:         marshal.RefusalCodeStaleBase,
		Effects:      effects,
		NextCommands: details.NextCommands,
		Message:      details.Error(),
	}, &details)
}

func withStaleBaseEffects(err error, effects marshal.RefusalEffects) error {
	if err == nil {
		return nil
	}
	var baseErr *LaunchBaseRefusalError
	if errors.As(err, &baseErr) && baseErr != nil {
		cp := *baseErr
		return newStaleBaseRefusal(effects, cp)
	}
	if receipt, ok := marshal.RefusalOf(err); ok {
		receipt.Effects = effects
		return marshal.NewRefusalError(receipt, err)
	}
	return marshal.NewRefusalError(marshal.RefusalReceipt{
		Code:         marshal.RefusalCodeStaleBase,
		Effects:      effects,
		NextCommands: []string{"/marshal amend", "/marshal use-plan"},
		Message:      err.Error(),
	}, errors.Join(ErrStaleBase, err))
}

func staleBaseResolutions(err error) []string {
	if receipt, ok := marshal.RefusalOf(err); ok && len(receipt.NextCommands) > 0 {
		return append([]string(nil), receipt.NextCommands...)
	}
	return []string{"review and approve a plan on the current delivery base (/marshal amend or /marshal use-plan)"}
}

func launchBasePinFromOp(op *marshal.LifecycleOperation, taskID string) *marshal.LaunchBasePin {
	if op == nil || op.Kind != "launch" || op.TaskID != taskID {
		return nil
	}
	return op.LaunchBase
}

func cloneLaunchBasePin(pin *marshal.LaunchBasePin) *marshal.LaunchBasePin {
	if pin == nil {
		return nil
	}
	cp := *pin
	if len(pin.MergedDependencyHeads) > 0 {
		cp.MergedDependencyHeads = make(map[string]string, len(pin.MergedDependencyHeads))
		for k, v := range pin.MergedDependencyHeads {
			cp.MergedDependencyHeads[k] = v
		}
	}
	return &cp
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func (s *MarshalService) observeIntegrationHead(ctx context.Context, runID string, run marshal.Run) (head string, exists bool, err error) {
	ref := "refs/heads/" + integrationBranch(runID, run)
	dir := filepath.Join(s.Worktrees, integrationTaskID(runID, run))

	refHead, refErr := gitMarshal(ctx, s.Repository, "rev-parse", "--verify", ref+"^{commit}")
	refExists := refErr == nil && refHead != ""

	dirExists := false
	if _, statErr := os.Lstat(dir); statErr == nil {
		dirExists = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", false, fmt.Errorf("inspect integration worktree: %w", statErr)
	}

	if dirExists {
		dirHead, dirErr := gitMarshal(ctx, dir, "rev-parse", "HEAD")
		if dirErr != nil {
			return "", true, fmt.Errorf("read integration worktree head: %w", dirErr)
		}
		status, statusErr := gitMarshalStdout(ctx, dir, "status", "--porcelain=v1", "--untracked-files=all")
		if statusErr != nil {
			return "", true, fmt.Errorf("inspect integration worktree status: %w", statusErr)
		}
		if len(hostgit.SourceStatusChanges(status)) > 0 {
			return "", true, errors.New("private integration worktree has uncommitted changes")
		}
		if refExists && refHead != dirHead {
			return "", true, fmt.Errorf("private integration worktree head %s differs from %s (%s)", dirHead, ref, refHead)
		}
		return strings.TrimSpace(dirHead), true, nil
	}
	if refExists {
		return strings.TrimSpace(refHead), true, nil
	}
	return "", false, nil
}

// resolveDispatchTaskBase keeps briefing freshness and dispatch's execution
// base on the same read-only calculation. It does not update the approved run.
func (s *MarshalService) resolveDispatchTaskBase(ctx context.Context, runID string, run marshal.Run, task marshal.Task) (marshal.Task, error) {
	fresh := (task.ResultCommit == "" && task.FailedAttemptCommit == "") || task.State == marshal.Reassigned || reworkInTaskLane(run, task)
	if !fresh {
		return task, nil
	}
	head, exists, err := s.observeIntegrationHead(ctx, runID, run)
	if err != nil {
		if len(task.DependsOn) > 0 {
			return task, fmt.Errorf("task base is unavailable: %w", err)
		}
		return task, nil
	}
	if exists && head != "" {
		if len(task.DependsOn) > 0 || (run.Process05Bound && !run.Process05Parallel && task.Mode == marshal.Governed && taskIndex(run, task.PlanTaskID) > 0) || head != run.BaseCommit {
			task.BaseCommit = head
		}
		return task, nil
	}
	if len(task.DependsOn) > 0 {
		return task, errors.New("task base is unavailable: private integration head is missing")
	}
	i := taskIndex(run, task.PlanTaskID)
	if (run.Process05Bound && !run.Process05Parallel) && task.Mode == marshal.Governed && task.ResultCommit == "" && i > 0 && run.Tasks[i-1].State == marshal.Merged {
		return task, errors.New("governed task base is unavailable: private integration head is missing")
	}
	return task, nil
}

// unusedLaunchBase permits a refresh only before any attempt or retained output.
func (s *MarshalService) unusedLaunchBase(ctx context.Context, runID string, run marshal.Run, task marshal.Task, expected *marshal.LaunchBasePin) bool {
	if expected != nil || task.State != marshal.Queued || task.ResultCommit != "" || task.FailedAttemptCommit != "" || taskReturns(task) != 0 || task.EvidenceAttemptBase != 0 || task.FreshSessionRetries != 0 || task.QuarantinedRef != "" || task.StaleProcessExitUnverified || s.dispatchLive(runID, task.PlanTaskID) {
		return false
	}
	if run.Operation != nil && run.Operation.Kind == "launch" && run.Operation.TaskID == task.PlanTaskID {
		return false
	}
	history, err := s.Store.MarshalDecisions(ctx, runID)
	if err != nil {
		return false
	}
	for _, event := range history {
		if event.TaskID == task.PlanTaskID && event.Type == events.EventTypeMarshalTaskDispatched {
			return false
		}
	}
	dir := filepath.Join(s.Worktrees, taskArtifactID(runID, task))
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return false
		}
		head, err := gitMarshal(ctx, dir, "rev-parse", "HEAD")
		if err != nil || (head != task.BaseCommit && head != run.BaseCommit) {
			return false
		}
		status, err := gitMarshalStdout(ctx, dir, "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil || strings.TrimSpace(status) != "" {
			return false
		}
	}
	if task.Branch != "" {
		head, err := gitMarshal(ctx, s.Repository, "rev-parse", "--verify", "refs/heads/"+task.Branch+"^{commit}")
		if err == nil && head != task.BaseCommit && head != run.BaseCommit {
			return false
		}
	}
	return true
}

// AdmitLaunchBase validates the approved delivery ref, repository, plan/scope,
// authority, governing inputs, required context, merged dependencies, and
// private integration head before worktree preparation or launch. Both direct
// dispatch and shared admission paths call this gate.
func (s *MarshalService) AdmitLaunchBase(ctx context.Context, runID string, run marshal.Run, task marshal.Task, expected *marshal.LaunchBasePin) (marshal.LaunchBasePin, error) {
	return s.admitLaunchBase(ctx, runID, run, task, expected)
}

func (s *MarshalService) admitLaunchBase(ctx context.Context, runID string, run marshal.Run, task marshal.Task, expected *marshal.LaunchBasePin) (marshal.LaunchBasePin, error) {
	integrationRef := "refs/heads/" + integrationBranch(runID, run)
	binding, err := s.resolveApprovedDeliveryBinding(ctx, run, true)
	if err != nil {
		route := "review and approve a plan on the current delivery base"
		next := []string{"/marshal amend", "/marshal use-plan"}
		if isLegacyMarshalRun(run) {
			route = "approve a recovery plan for this legacy run"
		}
		return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
			TaskID:         task.PlanTaskID,
			ApprovedRef:    run.TargetRef,
			ApprovedCommit: run.BaseCommit,
			IntegrationRef: integrationRef,
			Route:          route,
			Reason:         "stale_base: " + err.Error(),
			NextCommands:   next,
		})
	}
	if !marshalCommitPattern.MatchString(binding.BaseCommit) {
		return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
			TaskID:         task.PlanTaskID,
			ApprovedRef:    binding.TargetRef,
			ApprovedCommit: binding.BaseCommit,
			IntegrationRef: integrationRef,
			Route:          "review and approve a plan on a valid delivery base",
			Reason:         "stale_base: approved base commit is missing or invalid",
			NextCommands:   []string{"/marshal amend", "/marshal use-plan"},
		})
	}
	if _, err := gitMarshal(ctx, s.Repository, "cat-file", "-e", binding.BaseCommit+"^{commit}"); err != nil {
		return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
			TaskID:         task.PlanTaskID,
			ApprovedRef:    binding.TargetRef,
			ApprovedCommit: binding.BaseCommit,
			IntegrationRef: integrationRef,
			Route:          "review and approve a plan on the current delivery base",
			Reason:         "stale_base: approved base commit is not present in repository",
			NextCommands:   []string{"/marshal amend", "/marshal use-plan"},
		})
	}
	// Require the approved delivery ref to resolve exactly to immutable
	// run.BaseCommit, matching closeRun's delivery target check. Any target
	// change, even a disjoint descendant, requires review and approval on the
	// new delivery base.
	deliveryHead, err := gitMarshal(ctx, s.Repository, "rev-parse", "--verify", binding.TargetRef+"^{commit}")
	if err != nil {
		return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
			TaskID:         task.PlanTaskID,
			ApprovedRef:    binding.TargetRef,
			ApprovedCommit: binding.BaseCommit,
			CurrentRef:     binding.TargetRef,
			IntegrationRef: integrationRef,
			Route:          "review and approve a plan on the current delivery base",
			Reason:         fmt.Sprintf("stale_base: approved delivery ref %s could not be resolved", binding.TargetRef),
			NextCommands:   []string{"/marshal amend", "/marshal use-plan"},
		})
	}
	if deliveryHead != binding.BaseCommit {
		return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
			TaskID:         task.PlanTaskID,
			ApprovedRef:    binding.TargetRef,
			ApprovedCommit: binding.BaseCommit,
			CurrentRef:     binding.TargetRef,
			CurrentCommit:  deliveryHead,
			IntegrationRef: integrationRef,
			Route:          "replan or review and approve on the new delivery base",
			Reason:         "stale_base: approved delivery base changed; review and approve on the new delivery base",
			NextCommands:   []string{"/marshal amend", "/marshal use-plan"},
		})
	}

	// Verify plan task scope, checks, and governed authority.
	if err := s.validateLaunchPlanAuthority(ctx, run, task, binding); err != nil {
		return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
			TaskID:         task.PlanTaskID,
			ApprovedRef:    binding.TargetRef,
			ApprovedCommit: binding.BaseCommit,
			CurrentRef:     binding.TargetRef,
			CurrentCommit:  deliveryHead,
			IntegrationRef: integrationRef,
			Route:          "replan and approve updated scope or authority",
			Reason:         "stale_base: " + err.Error(),
			NextCommands:   []string{"/marshal amend", "/marshal use-plan"},
		})
	}

	// Verify current governing inputs and required context.
	if run.GoverningDigest != "" {
		digest, gErr := governingDigest(s.Repository)
		if gErr != nil || digest != run.GoverningDigest {
			return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
				TaskID:         task.PlanTaskID,
				ApprovedRef:    binding.TargetRef,
				ApprovedCommit: binding.BaseCommit,
				CurrentRef:     binding.TargetRef,
				CurrentCommit:  deliveryHead,
				IntegrationRef: integrationRef,
				Route:          "review governing file changes and approve a new plan",
				Reason:         "stale_base: governing files changed since approval",
				NextCommands:   []string{"/marshal status", "/marshal use-plan"},
			})
		}
	}
	if s.requiredContextDispatch != nil {
		if err := s.requiredContextDispatch(ctx, run, task); err != nil {
			return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
				TaskID:         task.PlanTaskID,
				ApprovedRef:    binding.TargetRef,
				ApprovedCommit: binding.BaseCommit,
				CurrentRef:     binding.TargetRef,
				CurrentCommit:  deliveryHead,
				IntegrationRef: integrationRef,
				Route:          "refresh required project context and re-approve if needed",
				Reason:         "stale_base: required project context unavailable: " + err.Error(),
				NextCommands:   []string{"/marshal status", "/marshal use-plan"},
			})
		}
	}

	// Observe private integration ref and worktree separately from delivery ref.
	integrationHead, hasIntegration, err := s.observeIntegrationHead(ctx, runID, run)
	if err != nil {
		return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
			TaskID:         task.PlanTaskID,
			ApprovedRef:    binding.TargetRef,
			ApprovedCommit: binding.BaseCommit,
			CurrentRef:     binding.TargetRef,
			CurrentCommit:  deliveryHead,
			IntegrationRef: integrationRef,
			Route:          "inspect private integration worktree and refresh or replan",
			Reason:         "stale_base: " + err.Error(),
			NextCommands:   []string{"/marshal status", "/marshal amend"},
		})
	}

	// Collect transitive dependencies and verify exact merged dependency heads.
	depTasks, mergedDepHeads, err := s.verifyMergedDependencies(ctx, run, task, integrationHead, hasIntegration)
	if err != nil {
		return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
			TaskID:          task.PlanTaskID,
			ApprovedRef:     binding.TargetRef,
			ApprovedCommit:  binding.BaseCommit,
			CurrentRef:      binding.TargetRef,
			CurrentCommit:   deliveryHead,
			IntegrationRef:  integrationRef,
			IntegrationHead: integrationHead,
			Route:           "scoped refresh or replan for changed dependency state",
			Reason:          "stale_base: " + err.Error(),
			NextCommands:    []string{"/marshal amend", "/marshal use-plan"},
		})
	}

	// Check against a saved launch intent pin when recovering or re-checking.
	if expected != nil {
		if err := verifyExpectedLaunchPin(expected, binding, deliveryHead, mergedDepHeads); err != nil {
			return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
				TaskID:          task.PlanTaskID,
				ApprovedRef:     binding.TargetRef,
				ApprovedCommit:  binding.BaseCommit,
				CurrentRef:      binding.TargetRef,
				CurrentCommit:   deliveryHead,
				IntegrationRef:  integrationRef,
				IntegrationHead: integrationHead,
				Route:           "scoped refresh or replan before relaunch",
				Reason:          "stale_base: " + err.Error(),
				NextCommands:    []string{"/marshal amend", "/marshal use-plan"},
			})
		}
	}

	fresh := (task.ResultCommit == "" && task.FailedAttemptCommit == "") || task.State == marshal.Reassigned || reworkInTaskLane(run, task)
	disjointAdmitted := false
	admittedBase := binding.BaseCommit

	if hasIntegration && integrationHead != "" && integrationHead != binding.BaseCommit {
		mergeConflictResume := !fresh && task.BaseCommit == integrationHead
		canRefresh := s.unusedLaunchBase(ctx, runID, run, task, expected)
		hasNonDepAdvance, err := s.verifyPrivateIntegrationAdvance(ctx, binding.BaseCommit, integrationHead, run, task, depTasks, mergedDepHeads, mergeConflictResume, canRefresh)
		if err != nil {
			return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
				TaskID:          task.PlanTaskID,
				ApprovedRef:     binding.TargetRef,
				ApprovedCommit:  binding.BaseCommit,
				CurrentRef:      binding.TargetRef,
				CurrentCommit:   deliveryHead,
				IntegrationRef:  integrationRef,
				IntegrationHead: integrationHead,
				Route:           "scoped refresh or replan (declare dependency or narrow file scope)",
				Reason:          "stale_base: " + err.Error(),
				NextCommands:    []string{"/marshal amend", "/marshal use-plan"},
			})
		}
		disjointAdmitted = hasNonDepAdvance
		if fresh {
			// Relaunches keep the base bound to their retained worktree or lane.
			if !canRefresh && task.BaseCommit != "" && task.BaseCommit != integrationHead {
				if _, ancErr := gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", task.BaseCommit, integrationHead); ancErr != nil {
					return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
						TaskID:          task.PlanTaskID,
						ApprovedRef:     binding.TargetRef,
						ApprovedCommit:  binding.BaseCommit,
						CurrentRef:      binding.TargetRef,
						CurrentCommit:   deliveryHead,
						IntegrationRef:  integrationRef,
						IntegrationHead: integrationHead,
						Route:           "replan or refresh task base",
						Reason:          "stale_base: retained task base is not an ancestor of private integration head",
						NextCommands:    []string{"/marshal amend", "/marshal use-plan"},
					})
				}
				admittedBase = task.BaseCommit
			} else {
				admittedBase = integrationHead
			}
		} else {
			if task.BaseCommit != "" {
				if _, ancErr := gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", task.BaseCommit, integrationHead); ancErr != nil {
					return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
						TaskID:          task.PlanTaskID,
						ApprovedRef:     binding.TargetRef,
						ApprovedCommit:  binding.BaseCommit,
						CurrentRef:      binding.TargetRef,
						CurrentCommit:   deliveryHead,
						IntegrationRef:  integrationRef,
						IntegrationHead: integrationHead,
						Route:           "replan or refresh task base",
						Reason:          "stale_base: resumed task base is not an ancestor of private integration head",
						NextCommands:    []string{"/marshal amend", "/marshal use-plan"},
					})
				}
				admittedBase = task.BaseCommit
			}
		}
	} else {
		if task.BaseCommit != "" && task.BaseCommit != binding.BaseCommit {
			return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
				TaskID:          task.PlanTaskID,
				ApprovedRef:     binding.TargetRef,
				ApprovedCommit:  binding.BaseCommit,
				CurrentRef:      binding.TargetRef,
				CurrentCommit:   deliveryHead,
				IntegrationRef:  integrationRef,
				IntegrationHead: integrationHead,
				Route:           "scoped refresh or replan",
				Reason:          fmt.Sprintf("stale_base: task base %s differs from approved base %s", task.BaseCommit, binding.BaseCommit),
				NextCommands:    []string{"/marshal amend", "/marshal use-plan"},
			})
		}
		if i := taskIndex(run, task.PlanTaskID); (run.Process05Bound && !run.Process05Parallel) && task.Mode == marshal.Governed && task.ResultCommit == "" && i > 0 && run.Tasks[i-1].State == marshal.Merged && !hasIntegration {
			return marshal.LaunchBasePin{}, newStaleBaseRefusal(marshal.EffectsNone, LaunchBaseRefusalError{
				TaskID:         task.PlanTaskID,
				ApprovedRef:    binding.TargetRef,
				ApprovedCommit: binding.BaseCommit,
				CurrentRef:     binding.TargetRef,
				CurrentCommit:  deliveryHead,
				IntegrationRef: integrationRef,
				Route:          "inspect private integration head or replan",
				Reason:         "stale_base: governed task base is unavailable because private integration head is missing",
				NextCommands:   []string{"/marshal status", "/marshal use-plan"},
			})
		}
		admittedBase = binding.BaseCommit
	}

	return marshal.LaunchBasePin{
		Repository:              binding.Repository,
		DeliveryRef:             binding.TargetRef,
		DeliveryCommit:          deliveryHead,
		ApprovedBaseCommit:      binding.BaseCommit,
		IntegrationRef:          integrationRef,
		IntegrationHead:         integrationHead,
		AdmittedBaseCommit:      admittedBase,
		MergedDependencyHeads:   mergedDepHeads,
		DisjointAdvanceAdmitted: disjointAdmitted,
	}, nil
}

func (s *MarshalService) validateLaunchPlanAuthority(ctx context.Context, run marshal.Run, task marshal.Task, binding approvedDeliveryBinding) error {
	p := binding.Plan
	var approvedTask *plan.Task
	for i := range p.Tasks {
		if p.Tasks[i].ID == task.PlanTaskID {
			approvedTask = &p.Tasks[i]
			break
		}
	}
	if approvedTask == nil {
		return fmt.Errorf("task %s is not in approved plan %s v%d", task.PlanTaskID, p.ID, p.Version)
	}
	if !sameStrings(approvedTask.Paths, task.Files) || !sameStrings(approvedTask.DependsOn, task.DependsOn) || !sameStrings(approvedTask.Criteria, task.Criteria) {
		return fmt.Errorf("task %s scope or dependencies differ from approved plan", task.PlanTaskID)
	}
	commands := make([]string, 0, len(task.Checks))
	for _, check := range task.Checks {
		commands = append(commands, check.Command)
	}
	if !sameStrings(p.Checks[task.PlanTaskID], commands) {
		return fmt.Errorf("task %s checks differ from approved plan", task.PlanTaskID)
	}
	governedOnly := false
	allowNative := false
	for _, c := range p.HardConstraints {
		if c == "allowed-mode:governed" {
			governedOnly = true
		}
		if c == "allow-native-exception" {
			allowNative = true
		}
	}
	if governedOnly && !allowNative && task.Mode == marshal.Native {
		return fmt.Errorf("task %s native mode violates governed plan authority", task.PlanTaskID)
	}
	if run.Process05Bound && task.Mode == marshal.Governed && s.runtime != nil {
		planProjectID := s.CanonicalPlanProjectID()
		active, err := s.Store.GetActivePlan(ctx, planProjectID)
		if err != nil || active.ID != p.ID || active.Version != p.Version || active.State != plan.StateApproved {
			return errors.New("approved governed plan is no longer active")
		}
		goal, err := s.Store.GetGoalContract(ctx, p.Goal.GoalID, p.Goal.Revision)
		if err != nil {
			return errors.New("approved goal contract is unavailable")
		}
		activeGoal, err := s.Store.GetActiveGoalContract(ctx, goal.SessionID)
		if err != nil || activeGoal.ID != goal.ID || activeGoal.Revision != goal.Revision || !goal.Confirmation.Settled() {
			return errors.New("approved goal contract is no longer active and confirmed")
		}
		if s.GovernedDrivers != nil && s.GovernedDrivers[task.Worker] == nil {
			return fmt.Errorf("governed worker %s is not authorized in this runtime", task.Worker)
		}
	}
	return nil
}

func (s *MarshalService) verifyMergedDependencies(ctx context.Context, run marshal.Run, task marshal.Task, integrationHead string, hasIntegration bool) ([]marshal.Task, map[string]string, error) {
	if len(task.DependsOn) == 0 {
		return nil, nil, nil
	}
	seen := map[string]bool{}
	queue := append([]string(nil), task.DependsOn...)
	var deps []marshal.Task
	heads := make(map[string]string, len(task.DependsOn))
	for len(queue) > 0 {
		depID := queue[0]
		queue = queue[1:]
		if seen[depID] {
			continue
		}
		seen[depID] = true
		idx := taskIndex(run, depID)
		if idx < 0 {
			return nil, nil, fmt.Errorf("dependency %s is missing from run", depID)
		}
		dep := run.Tasks[idx]
		if dep.State != marshal.Merged {
			return nil, nil, fmt.Errorf("dependency %s is %s, not merged", depID, dep.State)
		}
		if !marshalCommitPattern.MatchString(dep.ResultCommit) {
			return nil, nil, fmt.Errorf("dependency %s lacks a valid merged result commit", depID)
		}
		if _, err := gitMarshal(ctx, s.Repository, "cat-file", "-e", dep.ResultCommit+"^{commit}"); err != nil {
			return nil, nil, fmt.Errorf("dependency %s merged commit %s is unavailable", depID, dep.ResultCommit)
		}
		if !hasIntegration || integrationHead == "" {
			return nil, nil, fmt.Errorf("private integration head is missing for merged dependency %s", depID)
		}
		if _, err := gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", dep.ResultCommit, integrationHead); err != nil {
			return nil, nil, fmt.Errorf("dependency %s merged commit %s is not in private integration head %s", depID, dep.ResultCommit, integrationHead)
		}
		deps = append(deps, dep)
		heads[depID] = dep.ResultCommit
		queue = append(queue, dep.DependsOn...)
	}
	return deps, heads, nil
}

func verifyExpectedLaunchPin(expected *marshal.LaunchBasePin, binding approvedDeliveryBinding, deliveryHead string, mergedDepHeads map[string]string) error {
	if expected == nil {
		return nil
	}
	if expected.Repository != "" && expected.Repository != binding.Repository {
		return errors.New("saved launch intent repository differs from current repository")
	}
	if expected.DeliveryRef != "" && expected.DeliveryRef != binding.TargetRef {
		return fmt.Errorf("saved launch intent delivery ref %s differs from %s", expected.DeliveryRef, binding.TargetRef)
	}
	if expected.DeliveryCommit != "" && expected.DeliveryCommit != deliveryHead {
		return fmt.Errorf("saved launch intent delivery commit %s differs from current %s", expected.DeliveryCommit, deliveryHead)
	}
	if expected.ApprovedBaseCommit != "" && expected.ApprovedBaseCommit != binding.BaseCommit {
		return fmt.Errorf("saved launch intent base commit %s differs from approved %s", expected.ApprovedBaseCommit, binding.BaseCommit)
	}
	if !equalStringMap(expected.MergedDependencyHeads, mergedDepHeads) {
		return errors.New("merged dependency heads changed since launch intent was saved")
	}
	return nil
}

func (s *MarshalService) verifyPrivateIntegrationAdvance(ctx context.Context, baseCommit, integrationHead string, run marshal.Run, task marshal.Task, depTasks []marshal.Task, mergedDepHeads map[string]string, mergeConflictResume, allowRefresh bool) (bool, error) {
	if _, err := gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", baseCommit, integrationHead); err != nil {
		return false, fmt.Errorf("private integration head %s is not a descendant of approved base %s", integrationHead, baseCommit)
	}
	logOut, err := gitMarshalStdout(ctx, s.Repository, "log", "--first-parent", "--format=%H%x00%P%x00%s", baseCommit+".."+integrationHead)
	if err != nil {
		return false, fmt.Errorf("unable to verify private integration ancestry: %w", err)
	}
	if strings.TrimSpace(logOut) == "" {
		return false, errors.New("private integration advance proof is empty")
	}
	// Overlapping work may be incorporated only when the entire advance is
	// accounted for by recorded merges from this run. Disjoint advances retain
	// their existing changed-file proof below.
	provenRunAdvance := true
	for _, line := range strings.Split(strings.TrimSpace(logOut), "\n") {
		fields := strings.SplitN(line, "\x00", 3)
		if len(fields) != 3 || !strings.HasPrefix(fields[2], "marshal: merge ") {
			provenRunAdvance = false
			break
		}
		id := strings.TrimPrefix(fields[2], "marshal: merge ")
		i := taskIndex(run, id)
		parents := strings.Fields(fields[1])
		if i < 0 || id == task.PlanTaskID || run.Tasks[i].State != marshal.Merged || len(parents) != 2 || parents[1] != run.Tasks[i].ResultCommit {
			provenRunAdvance = false
			break
		}
	}
	incorporatedAdvance := provenRunAdvance && (allowRefresh || task.BaseCommit == integrationHead)
	depByID := make(map[string]marshal.Task, len(depTasks))
	depResultIDs := make(map[string]string, len(mergedDepHeads))
	for _, d := range depTasks {
		depByID[d.PlanTaskID] = d
		if r := mergedDepHeads[d.PlanTaskID]; r != "" {
			depResultIDs[r] = d.PlanTaskID
		}
	}
	seenDepMerge := map[string]bool{}
	hasNonDepAdvance := false

	for _, line := range strings.Split(logOut, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\x00", 3)
		if len(fields) != 3 {
			return false, errors.New("malformed private integration ancestry record")
		}
		sha := strings.TrimSpace(fields[0])
		parents := strings.Fields(fields[1])
		subject := strings.TrimSpace(fields[2])
		if len(parents) == 0 || len(parents) > 2 {
			return false, fmt.Errorf("unproven integration commit %s has %d parents", sha, len(parents))
		}
		p1 := parents[0]

		if strings.HasPrefix(subject, "marshal: merge ") {
			mergedID := strings.TrimSpace(strings.TrimPrefix(subject, "marshal: merge "))
			if _, isDep := depByID[mergedID]; isDep {
				if len(parents) != 2 {
					return false, fmt.Errorf("dependency %s merge commit %s lacks expected parents", mergedID, sha)
				}
				if parents[1] != mergedDepHeads[mergedID] {
					return false, fmt.Errorf("dependency %s merged commit %s differs from recorded result %s", mergedID, parents[1], mergedDepHeads[mergedID])
				}
				if seenDepMerge[mergedID] {
					return false, fmt.Errorf("dependency %s appears multiple times in private integration history", mergedID)
				}
				seenDepMerge[mergedID] = true
				continue
			}
		}
		if depID, ok := depResultIDs[sha]; ok {
			seenDepMerge[depID] = true
			continue
		}

		// Non-dependency advance commit on private integration.
		hasNonDepAdvance = true
		if mergeConflictResume {
			continue
		}
		if !incorporatedAdvance && hasBroadFileScope(task.Files) {
			return false, fmt.Errorf("cannot prove private integration advance %s is disjoint from broad task file scope", sha)
		}
		for _, dep := range depTasks {
			if !incorporatedAdvance && hasBroadFileScope(dep.Files) {
				return false, fmt.Errorf("cannot prove private integration advance %s is disjoint from broad dependency %s scope", sha, dep.PlanTaskID)
			}
		}
		changedRaw, diffErr := gitMarshalStdout(ctx, s.Repository, "diff", "--name-only", "--no-renames", "-z", p1, sha)
		if diffErr != nil {
			return false, fmt.Errorf("cannot verify changed files for private integration commit %s: %w", sha, diffErr)
		}
		var changedFiles []string
		for _, entry := range strings.Split(changedRaw, "\x00") {
			if entry != "" {
				changedFiles = append(changedFiles, entry)
			}
		}
		if len(changedFiles) == 0 {
			p1Tree, err1 := gitMarshal(ctx, s.Repository, "rev-parse", p1+"^{tree}")
			shaTree, err2 := gitMarshal(ctx, s.Repository, "rev-parse", sha+"^{tree}")
			if err1 != nil || err2 != nil || p1Tree != shaTree {
				return false, fmt.Errorf("missing changed-file proof for private integration commit %s", sha)
			}
			continue
		}
		for _, f := range changedFiles {
			if _, ok := canonicalScopePath(f); !ok {
				return false, fmt.Errorf("unproven path %q in private integration commit %s", f, sha)
			}
			if governingFile(f) || marshal.ReservedPath(f) {
				return false, fmt.Errorf("private integration commit %s modifies governing or reserved path %s", sha, f)
			}
			if incorporatedAdvance {
				continue
			}
			for _, tf := range task.Files {
				if pathsOverlap(f, tf) {
					return false, fmt.Errorf("private integration advance %s overlaps task file %s via %s", sha, tf, f)
				}
			}
			for _, dep := range depTasks {
				for _, df := range dep.Files {
					if pathsOverlap(f, df) {
						return false, fmt.Errorf("private integration advance %s modifies dependency %s file %s via %s", sha, dep.PlanTaskID, df, f)
					}
				}
			}
		}
	}

	// Verify every merged dependency that produced a new commit is accounted for
	// cleanly without a conflicting merge commit.
	for _, dep := range depTasks {
		result := mergedDepHeads[dep.PlanTaskID]
		if result == "" || result == baseCommit {
			continue
		}
		if !seenDepMerge[dep.PlanTaskID] {
			// Still allow a legitimate ancestor commit only when no conflicting
			// merge exists and ancestry was already verified above; require the
			// commit's changed files to stay within its own approved scope.
			if _, ancErr := gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", result, integrationHead); ancErr != nil {
				return false, fmt.Errorf("dependency %s merged commit %s is missing from private integration", dep.PlanTaskID, result)
			}
		}
	}
	return hasNonDepAdvance, nil
}

func quarantinedTaskRef(runID string, run marshal.Run, t marshal.Task, attempt int) string {
	if run.ArtifactRevision > 0 {
		return fmt.Sprintf("refs/%s/%s/r%d/quarantined/%s/attempt-%d", statepath.RefName, runID, run.ArtifactRevision, t.PlanTaskID, attempt)
	}
	return fmt.Sprintf("refs/%s/%s/quarantined/%s/attempt-%d", statepath.RefName, runID, t.PlanTaskID, attempt)
}

// quarantineStaleTaskOutput preserves any output commit produced by an
// already-started attempt whose base became stale, without promoting it to a
// hand-in or overwriting prior evidence.
func (s *MarshalService) quarantineStaleTaskOutput(ctx context.Context, runID string, run marshal.Run, t *marshal.Task, opDir string) (string, error) {
	candidate := ""
	switch {
	case marshalCommitPattern.MatchString(t.ResultCommit):
		candidate = t.ResultCommit
	case marshalCommitPattern.MatchString(t.FailedAttemptCommit):
		candidate = t.FailedAttemptCommit
	}
	if candidate == "" {
		dir := opDir
		if dir == "" {
			dir = filepath.Join(s.Worktrees, taskArtifactID(runID, *t))
		}
		if _, err := os.Lstat(dir); err == nil {
			if head, err := gitMarshal(ctx, dir, "rev-parse", "HEAD"); err == nil && marshalCommitPattern.MatchString(head) {
				candidate = head
			}
		}
	}
	if candidate == "" && t.Branch != "" {
		if head, err := gitMarshal(ctx, s.Repository, "rev-parse", "--verify", "refs/heads/"+t.Branch+"^{commit}"); err == nil && marshalCommitPattern.MatchString(head) {
			candidate = head
		}
	}
	if candidate == "" || candidate == t.BaseCommit || candidate == run.BaseCommit {
		return "", nil
	}
	baseAttempt := 1 + t.EvidenceAttemptBase + taskReturns(*t)
	for offset := 0; offset < 64; offset++ {
		ref := quarantinedTaskRef(runID, run, *t, baseAttempt+offset)
		if existing, err := gitMarshal(ctx, s.Repository, "rev-parse", "--verify", ref); err == nil {
			if existing == candidate {
				t.QuarantinedRef = ref
				return ref, nil
			}
			continue
		}
		if _, err := gitMarshal(ctx, s.Repository, "update-ref", ref, candidate, strings.Repeat("0", len(candidate))); err != nil {
			return "", fmt.Errorf("quarantine stale output: %w", err)
		}
		t.QuarantinedRef = ref
		return ref, nil
	}
	return "", errors.New("quarantine stale output: exhausted quarantine attempt refs")
}

// verifyStartedProcessExited reconciles ownership of an already-started worker
// process and reports whether it has reached a verified exit.
func (s *MarshalService) verifyStartedProcessExited(ctx context.Context, runID string, run marshal.Run, t marshal.Task) (bool, error) {
	if err := s.requireLifecycleOwner(); err != nil {
		return false, err
	}
	if s.dispatchLive(runID, t.PlanTaskID) {
		return false, nil
	}
	if s.VerifyProcessExit != nil {
		exited, err := s.VerifyProcessExit(ctx, runID, t.PlanTaskID)
		if err != nil || !exited {
			return false, err
		}
	}
	if run.Process05Bound && run.Process05Parallel && t.Mode == marshal.Governed && s.process05CloseOrphanLane != nil {
		if err := s.process05CloseOrphanLane(ctx, run, t); err != nil {
			if errors.Is(err, execution.ErrRunBlocked) {
				return false, nil
			}
		}
	}
	return true, nil
}

// reconcileStartedStaleLaunch handles an already-started worker whose base or
// dependency became stale: it reconciles ownership, holds capacity until
// verified exit, and quarantines stale output once exited without promoting it
// or discarding prior approval/evidence.
func (s *MarshalService) reconcileStartedStaleLaunch(ctx context.Context, runID string, run marshal.Run, rev int64, taskIdx int, opDir string, staleErr error) (marshal.Run, int64, error) {
	t := &run.Tasks[taskIdx]
	exited, checkErr := s.verifyStartedProcessExited(ctx, runID, run, *t)
	if checkErr != nil || !exited {
		// Process exit is not verified yet: hold capacity and keep task dispatched.
		t.State = marshal.Dispatched
		t.StaleProcessExitUnverified = true
		reason := staleErr.Error() + "; holding capacity until started process exits"
		if checkErr != nil {
			reason += " (" + checkErr.Error() + ")"
		}
		pauseMarshal(&run, marshal.Pause{
			Reason:      reason,
			Resolutions: staleBaseResolutions(staleErr),
			Code:        marshal.CodeStaleBase,
			Action:      marshal.PauseUsePlan,
			TaskID:      t.PlanTaskID,
		})
		if err := s.save(ctx, runID, run, rev); err != nil {
			return run, rev, errors.Join(withStaleBaseEffects(staleErr, marshal.EffectsUnknown), err)
		}
		return run, rev + 1, withStaleBaseEffects(staleErr, marshal.EffectsUnknown)
	}

	quarantineRef, qErr := s.quarantineStaleTaskOutput(ctx, runID, run, t, opDir)
	t.StaleProcessExitUnverified = false
	t.ResultCommit = ""
	t.FailedAttemptCommit = ""
	t.State = marshal.Escalated
	t.EscalationCode = marshal.CodeStaleBase
	t.EscalationReason = staleErr.Error()
	run.Operation = nil
	pauseMarshal(&run, marshal.Pause{
		Reason:      staleErr.Error(),
		Resolutions: staleBaseResolutions(staleErr),
		Code:        marshal.CodeStaleBase,
		Action:      marshal.PauseUsePlan,
		TaskID:      t.PlanTaskID,
	})
	effects := marshal.EffectsUnknown
	if quarantineRef != "" {
		effects = marshal.EffectsPartial
	}
	refusal := withStaleBaseEffects(staleErr, effects)
	data := map[string]any{
		"code":   marshal.CodeStaleBase,
		"reason": staleErr.Error(),
	}
	if quarantineRef != "" {
		data["quarantined_ref"] = quarantineRef
	}
	ev, evErr := s.decisionEvent(runID, t.PlanTaskID, events.EventTypeMarshalEscalated, data)
	if evErr != nil {
		return run, rev, errors.Join(refusal, qErr, evErr)
	}
	if saveErr := s.Store.SaveMarshalState(ctx, s.ProjectID, runID, run, rev, &ev, nil, 0); saveErr != nil {
		return run, rev, errors.Join(refusal, qErr, saveErr)
	}
	if qErr != nil {
		return run, rev + 1, errors.Join(refusal, qErr)
	}
	return run, rev + 1, refusal
}

// advanceCleanUnusedTaskWorktree fast-forwards an existing clean task worktree
// that still sits at its previous empty base (no worker commits, no dirty
// changes) onto a newly admitted descendant base commit. Any dirty or moved
// worktree is left untouched so worktree.Prepare refuses it safely.
func advanceCleanUnusedTaskWorktree(ctx context.Context, wt *worktree.Manager, repository, dir, branch, prevBase, runBase, targetBase string) error {
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	state, err := wt.Inspect(ctx, dir)
	if err != nil {
		return nil
	}
	if state.Dirty || state.Branch != branch {
		return nil
	}
	if state.HEAD == targetBase || (state.HEAD != prevBase && state.HEAD != runBase) {
		return nil
	}
	if _, err := gitMarshal(ctx, repository, "merge-base", "--is-ancestor", state.HEAD, targetBase); err != nil {
		return nil
	}
	if _, err := gitMarshal(ctx, dir, "merge", "--ff-only", targetBase); err != nil {
		return fmt.Errorf("advance clean task worktree to admitted base: %w", err)
	}
	return nil
}
