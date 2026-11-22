package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

// The final check must come from a provider that did none of the run's
// tasks. These helpers keep at least one provider that can do that check off
// worker tasks, so planning and failover never use up the last one.

// finalCheckerGaps lists why provider cannot be the final independent
// checker, apart from doing the run's tasks. limits also counts a quota or
// rate limit it reported recently; that passes, so the reservation ignores it.
func (r *Runtime) finalCheckerGaps(provider string, installed []string, limits bool) []string {
	var reasons []string
	if !slices.Contains(installed, provider) {
		reasons = append(reasons, "not installed")
	}
	if !sessionmcp.MarshalProvider(provider) {
		reasons = append(reasons, "Marshal MCP unavailable")
	}
	if limits {
		if reason, limited := r.providerLimited(provider); limited {
			reasons = append(reasons, "limited: "+reason)
		}
	}
	if reason := requiredVerifierAttributionGap(provider); reason != "" {
		reasons = append(reasons, reason)
	}
	return reasons
}

// finalCheckerCandidates lists the providers that could do the final check
// if they did none of the run's tasks, in provider order.
func finalCheckerCandidates() []string {
	installed := installedMarshalProviders()
	var out []string
	for _, provider := range MarshalProviders() {
		if len((*Runtime)(nil).finalCheckerGaps(provider, installed, false)) == 0 {
			out = append(out, provider)
		}
	}
	return out
}

// takesLastChecker reports whether giving task taskID of tasks to provider
// would leave no candidate free of every task.
func takesLastChecker(candidates []string, tasks []marshal.Task, taskID, provider string) bool {
	if len(candidates) == 0 || provider == "" || !sameProviderAsWorker(provider, candidates...) {
		return false
	}
	workers := []string{provider}
	for _, t := range tasks {
		if t.PlanTaskID != taskID {
			workers = append(workers, t.Worker)
		}
	}
	for _, candidate := range candidates {
		if !sameProviderAsWorker(candidate, workers...) {
			return false
		}
	}
	return true
}

// checkerReservationApplies reports whether run ends in an independent
// final check: every run in the private edition, an ULTRA run otherwise.
func (s *MarshalService) checkerReservationApplies(run marshal.Run) bool {
	return s != nil && s.CheckerCandidates != nil && (run.Tier == marshal.Ultra || s.requiredCanonicalVerification != nil)
}

// reservedChecker reports whether provider must not take task taskID of run
// because it is the last provider left that can do the run's final check.
func (s *MarshalService) reservedChecker(run marshal.Run, taskID, provider string) bool {
	if !s.checkerReservationApplies(run) {
		return false
	}
	return takesLastChecker(s.CheckerCandidates(), run.Tasks, taskID, provider)
}

// checkerFreeOrder is order without the providers task taskID must not
// move to, so a failover never takes the last final checker.
func (s *MarshalService) checkerFreeOrder(run marshal.Run, taskID string, order []string) []string {
	out := make([]string, 0, len(order))
	for _, provider := range order {
		if !s.reservedChecker(run, taskID, provider) {
			out = append(out, provider)
		}
	}
	return out
}

// checkerKeptNote names, for a failover that found no provider, the one
// left out of order because it is kept free for the final check; "" when
// none was.
func (s *MarshalService) checkerKeptNote(run marshal.Run, taskID string, order []string) string {
	for _, provider := range order {
		if s.reservedChecker(run, taskID, provider) {
			return " " + checkerName(provider) + " is kept free for this run's final check."
		}
	}
	return ""
}

// checkerAlreadyLost reports whether providers that already did work on run,
// on tasks other than taskID, cover every final checker: then keeping
// another task off one cannot save the final check.
func checkerAlreadyLost(candidates []string, run marshal.Run, taskID string) bool {
	var worked []string
	for _, t := range run.Tasks {
		if t.PlanTaskID == taskID {
			continue
		}
		switch t.State {
		case marshal.Dispatched, marshal.HandedIn, marshal.Accepted, marshal.Returned, marshal.Merged:
			worked = append(worked, t.Worker)
		default:
			if t.ResultCommit != "" {
				worked = append(worked, t.Worker)
			}
		}
	}
	for _, candidate := range candidates {
		if !sameProviderAsWorker(candidate, worked...) {
			return false
		}
	}
	return true
}

// refuseDispatchToReservedChecker stops a task, before its worker starts,
// whose worker is the last provider able to do the run's final check: the
// owner pinned it there, or no other provider could take it. The task waits
// for the owner to move it; MARSHAL never drops the final check's
// independence to keep going.
func (s *MarshalService) refuseDispatchToReservedChecker(ctx context.Context, runID string, run marshal.Run, rev int64, index int) error {
	t := run.Tasks[index]
	if !s.reservedChecker(run, t.PlanTaskID, t.Worker) || checkerAlreadyLost(s.CheckerCandidates(), run, t.PlanTaskID) {
		return nil
	}
	name := checkerName(t.Worker)
	reason := fmt.Sprintf("Task %s is set to %s, but %s is the only AI tool left that can do this run's final check, and that check must come from an AI tool that did none of the tasks. MARSHAL did not start the task.", t.PlanTaskID, name, name)
	resolution := fmt.Sprintf("Install another AI tool to do task %s, keeping %s free for the final check, then move the task to it with /marshal reassign %s <worker> and press Enter.", t.PlanTaskID, name, t.PlanTaskID)
	if other := s.otherWorkerFor(run, t); other != "" {
		resolution = fmt.Sprintf("Move task %s to another AI tool, for example /marshal reassign %s %s, then press Enter. %s stays free for the final check.", t.PlanTaskID, t.PlanTaskID, other, name)
	}
	detail := "worker " + t.Worker + " is the last provider that can do the final independent check"
	run.Tasks[index].State = marshal.Escalated
	run.Tasks[index].EscalationReason = reason
	run.Tasks[index].EscalationCode = marshal.CodeReservedChecker
	pauseMarshal(&run, marshal.Pause{Reason: reason, Detail: detail, Code: marshal.CodeReservedChecker, Action: marshal.PauseResume, TaskID: t.PlanTaskID, Resolutions: []string{resolution}})
	ctx = context.WithoutCancel(ctx)
	if err := s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	if err := s.record(ctx, runID, t.PlanTaskID, events.EventTypeMarshalEscalated, map[string]any{"reason": detail, "summary": reason, "reserved_checker": t.Worker}); err != nil {
		return err
	}
	if s.RecordActivity != nil {
		s.RecordActivity(reason)
	}
	return fmt.Errorf("dispatch paused: %s", detail)
}

// reservedCheckerRefusal is the error for moving task taskID onto provider,
// the last provider left that can do the run's final check.
func reservedCheckerRefusal(taskID, provider string) error {
	name := checkerName(provider)
	return fmt.Errorf("%s is kept free for this run's final check, which must come from an AI tool that did none of the tasks; it is the only one left that can do it. Move task %s to another AI tool", name, taskID)
}

// FinalCheckerBriefing tells a Marshal planning a run which providers to
// keep off its tasks so the final check can run, or "" when none need be.
// workers are the providers it may assign; ultra is the run's tier.
func FinalCheckerBriefing(workers []string, ultra bool) string {
	if !ultra && !finalCheckEveryRun {
		return ""
	}
	candidates := finalCheckerCandidates()
	if len(candidates) == 0 {
		return ""
	}
	for _, candidate := range candidates {
		if !sameProviderAsWorker(candidate, workers...) {
			// A candidate the Marshal cannot assign stays free anyway.
			return ""
		}
	}
	var others []string
	for _, worker := range workers {
		if !sameProviderAsWorker(worker, candidates...) {
			others = append(others, worker)
		}
	}
	if len(candidates) == 1 {
		line := fmt.Sprintf("Do not assign any task to %s: it is reserved as this run's final checker (%s), and the final check must come from an AI tool that did none of the tasks.", candidates[0], checkerName(candidates[0]))
		if len(others) > 0 {
			return line + " Assign every task to: " + strings.Join(others, ", ") + ". A task that names " + candidates[0] + " is moved to another worker."
		}
		return line + " No other worker is installed, so the run will pause before its first task."
	}
	return fmt.Sprintf("Leave at least one of %s with no task: the final check must come from an AI tool that did none of the tasks.", strings.Join(candidates, ", "))
}

// checkerName is a provider's name as the owner knows it, e.g. "Claude Code".
func checkerName(provider string) string {
	switch key := providerHealthKey(provider); key {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "agy":
		return "Antigravity"
	case "opencode":
		return "OpenCode"
	default:
		return ProviderDisplayName(key)
	}
}
