//go:build !governed

package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// PrepareNativeMarshalDraft replaces model-supplied planning identity with a
// Process 04 plan derived from the active Goal and current probed harnesses.
// It does not write state; the caller commits the plan and reviewable run in
// the one-shot native planning-read transaction.
func (r *Runtime) PrepareNativeMarshalDraft(ctx context.Context, sessionID string, draft MarshalDraft, modes ...plan.Mode) (MarshalDraft, error) {
	if r == nil || r.store == nil {
		return MarshalDraft{}, model.ErrUnavailable
	}
	goal, err := r.store.GetActiveGoalContract(ctx, sessionID)
	if err != nil {
		return MarshalDraft{}, err
	}
	assessment, err := assessmentFromGoal(goal.Assessment)
	if err != nil {
		return MarshalDraft{}, err
	}
	mode := plan.ModeStandard
	if len(modes) > 0 {
		if len(modes) != 1 || modes[0] != plan.ModeStandard && modes[0] != plan.ModeUltra {
			return MarshalDraft{}, fmt.Errorf("%w: invalid native Marshal planning mode", model.ErrInvalid)
		}
		mode = modes[0]
	}
	packet, err := r.acceptedArchitectureContext(ctx, r.layout.Root)
	if err != nil {
		return MarshalDraft{}, err
	}
	ownerLines, pinned, err := applyOwnerBuildersPinned(&draft, packet)
	if err != nil {
		return MarshalDraft{}, err
	}
	request := CreatePlanRequest{SessionID: sessionID, ProjectID: projectid.ID(r.ProjectIdentity()),
		Assessment: assessment, Tasks: draft.Plan.Tasks, Mode: mode,
		Scope: append([]string(nil), goal.Scope...), Checks: draft.Plan.Checks,
		TaskProviders: make(map[string]string, len(draft.Tasks))}
	reassignments, err := r.prepareWorkerReassignments(ctx, &draft, pinned)
	if err != nil {
		return MarshalDraft{}, err
	}
	seen := make(map[string]bool, len(draft.Tasks))
	for _, task := range draft.Tasks {
		if task.Mode != "governed" {
			return MarshalDraft{}, fmt.Errorf("%w: native Marshal plan requires governed workers for every task", model.ErrInvalid)
		}
		provider := normalizeProvider(task.Worker)
		if provider == "" || len(task.Criteria) != 1 {
			return MarshalDraft{}, fmt.Errorf("%w: task %s requires an exact provider and one criterion for checks", model.ErrInvalid, task.PlanTaskID)
		}
		request.TaskProviders[task.PlanTaskID] = provider
		if seen[provider] {
			continue
		}
		seen[provider] = true
		profile, installed, e := r.currentHarnessProfile(ctx, provider)
		if e != nil {
			return MarshalDraft{}, fmt.Errorf("%w: %v", model.ErrInvalid, e)
		}
		capacity := goalintake.UnknownCapacity(provider, true)
		request.Candidates = append(request.Candidates, goalintake.Candidate{Provider: provider, Model: profile.DefaultModel, Capacity: capacity, Governance: constitution.GovernanceVerified})
		request.HarnessCandidates = append(request.HarnessCandidates, plan.HarnessCandidate{Profile: *profile, InstalledVersion: installed, Provider: provider, Capacity: capacity})
	}
	built, err := r.Plans().Build(ctx, request)
	if err != nil {
		return MarshalDraft{}, err
	}
	if blockers := planExecutionBlockers(built); built.State != plan.StateReady || len(blockers) != 0 {
		return MarshalDraft{}, fmt.Errorf("%w: Process 04 plan is not ready (state %s): %s", plan.ErrPlanInvalid, built.State, strings.Join(planBlockerReport(built.BlockedBy, blockers), "; "))
	}
	if built.RequiredProjectContract == nil {
		return MarshalDraft{}, projectcontext.ErrReviewPending
	}
	// RevisionReason is part of the plan persisted atomically with the run.
	// Preparing a draft must never publish a committed reassignment event.
	for _, line := range ownerLines {
		built.RevisionReason += line + "\n"
	}
	for _, decision := range reassignments {
		decision.SelectedModel = built.Routes[decision.Task].Model
		built.RevisionReason += decision.String() + "\n"
	}
	draft.Plan = built
	return draft, nil
}

// currentVerifiedWorker reports whether provider has a current verified
// harness profile for the installed CLI, the same test plan import applies.
func (r *Runtime) currentVerifiedWorker(ctx context.Context, provider string) bool {
	return r.workerIneligibilityReason(ctx, provider) == ""
}

func (r *Runtime) workerIneligibilityReason(ctx context.Context, provider string) string {
	if provider == "agy" && !privateAGYEnabled() {
		return "AGY is unavailable in this edition"
	}
	if reason, limited := r.providerLimited(provider); limited {
		return "provider cooldown: " + reason
	}
	profile, err := r.store.GetHarnessProfile(ctx, marshalHarnessName(provider))
	if err != nil || profile == nil {
		return "no recorded harness profile"
	}
	if strings.TrimSpace(profile.DefaultModel) == "" || strings.TrimSpace(profile.ProbeEvidenceID) == "" {
		return "harness profile has no verified default model or probe evidence"
	}
	assessment := harness.AssessGovernance(*profile, installedCLIVersion(ctx, provider), time.Now().UTC())
	if assessment.State != constitution.GovernanceVerified {
		return strings.Join(assessment.Reasons, "; ")
	}
	return ""
}

// prepareWorkerReassignments keeps a plan moving when a planned worker
// provider has no current verified harness (for example its quota is spent
// and verification failed): Extreme never waits for the owner to re-verify.
// Each such task moves to the next verified provider in workerFallbackOrder;
// cross-provider review still runs with a different provider, chosen by the
// runtime at review time. Every move travels in the draft plan until import.
// With no verified alternative the task keeps its worker and plan import
// refuses as before.
// WorkerReassignment preserves the requested and selected identities for import.
// The plan ID/version and task ID correlate this reason with the imported run.
type WorkerReassignment struct {
	Task, RequestedProvider, RequestedModel, SelectedProvider, SelectedModel, Reason string
}

func (d WorkerReassignment) String() string {
	return fmt.Sprintf("task %q worker reassigned: requested provider=%q model=%q; selected provider=%q model=%q; reason=%q", d.Task, d.RequestedProvider, d.RequestedModel, d.SelectedProvider, d.SelectedModel, d.Reason)
}

// A task the Marshal gave to the last provider able to do the run's final
// check also moves, to another verified provider, with that reason; one the
// owner pinned there stays, and dispatch stops it for the owner.
func (r *Runtime) prepareWorkerReassignments(ctx context.Context, draft *MarshalDraft, pinned map[string]bool) ([]WorkerReassignment, error) {
	var decisions []WorkerReassignment
	// Own the task slice: a failed preparation must not mutate the caller's draft.
	draft.Tasks = append([]marshal.Task(nil), draft.Tasks...)
	original := append([]marshal.Task(nil), draft.Tasks...)
	verified := func(provider string) bool { return r.currentVerifiedWorker(ctx, provider) }
	record := func(task, from, to, reason string) error {
		profile, err := r.store.GetHarnessProfile(ctx, marshalHarnessName(to))
		if err != nil {
			return err
		}
		requested := from
		for _, task0 := range original {
			if task0.PlanTaskID == task {
				requested = task0.Worker
				break
			}
		}
		decisions = append(decisions, WorkerReassignment{Task: task, RequestedProvider: requested, RequestedModel: draft.Plan.Routes[task].Model, SelectedProvider: to, SelectedModel: profile.DefaultModel, Reason: reason})
		return nil
	}
	candidates := finalCheckerCandidates()
	err := reassignDraftWorkers(draft, verified, func(task, from, to string) error {
		return record(task, from, to, r.workerIneligibilityReason(ctx, from))
	}, func(task, provider string) bool { return takesLastChecker(candidates, draft.Tasks, task, provider) })
	if err != nil {
		return decisions, err
	}
	err = keepCheckerOffDraft(draft, candidates, pinned, verified, record)
	return decisions, err
}

// keepCheckerOffDraft moves each task the Marshal gave to the last provider
// able to do the final check onto the first verified provider, in
// workerFallbackOrder, that leaves one free; record reports each move before
// it is made. A task pinned by the owner, or with no such provider, stays.
func keepCheckerOffDraft(draft *MarshalDraft, candidates []string, pinned map[string]bool, verified func(string) bool, record func(task, from, to, reason string) error) error {
	for i, task := range draft.Tasks {
		if pinned[task.PlanTaskID] || !takesLastChecker(candidates, draft.Tasks, task.PlanTaskID, task.Worker) {
			continue
		}
		from := reassignProviderName(task.Worker)
		for _, to := range workerFallbackOrder {
			if to == from || !verified(to) || takesLastChecker(candidates, draft.Tasks, task.PlanTaskID, to) {
				continue
			}
			reason := checkerName(from) + " is kept free for the final check, which must come from an AI tool that did none of the tasks"
			if err := record(task.PlanTaskID, from, to, reason); err != nil {
				return err
			}
			draft.Tasks[i].Worker = to
			break
		}
	}
	return nil
}

func assessmentFromGoal(saved map[string]string) (goalintake.Assessment, error) {
	var a goalintake.Assessment
	fields := map[string]*goalintake.Level{
		"complexity": &a.Complexity, "ambiguity": &a.Ambiguity,
		"blast_radius": &a.BlastRadius, "privilege": &a.Privilege,
		"reversibility": &a.Reversibility, "external_effects": &a.ExternalEffects,
		"data_sensitivity": &a.DataSensitivity, "dependency_depth": &a.DependencyDepth,
		"verification_difficulty": &a.VerificationDifficulty,
		"operational_criticality": &a.OperationalCriticality,
	}
	for name, destination := range fields {
		level := goalintake.Level(saved[name])
		switch level {
		case goalintake.LevelNone, goalintake.LevelLow, goalintake.LevelMed, goalintake.LevelHigh, goalintake.LevelUnknown:
			*destination = level
		default:
			return a, fmt.Errorf("%w: active Goal lacks canonical %s assessment; confirm Goal again", model.ErrInvalid, name)
		}
	}
	return a, nil
}
