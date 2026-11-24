//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	providerspec "github.com/Zen1th53/marshal/internal/adapter/provider"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func normalizeProvider(p string) string {
	if spec, ok := providerspec.ByHarness(p); ok {
		return spec.ID
	}
	return strings.ToLower(strings.TrimSpace(p))
}

func extremeProviderLimits(s marshal.Settings) (map[string]int, int) {
	return marshal.EffectiveWorkerLimits(s)
}

// holdsWorkerSlot reports whether another task occupies a worker slot. A
// task parked on its own approval keeps its lane open, so it counts against
// the provider and global limits; otherwise dispatch opens lanes past the
// engine's lane capacity. The candidate never counts against itself, so an
// approved wait can continue in its own slot.
func holdsWorkerSlot(other, candidate marshal.Task) bool {
	if other.PlanTaskID == candidate.PlanTaskID {
		return false
	}
	return other.State == marshal.Dispatched || other.StaleProcessExitUnverified || (other.State == marshal.Queued && other.Process05WaitApprovalID != "")
}

func activeDispatchTasks(run marshal.Run, candidate marshal.Task, launched []MarshalDispatch) []marshal.Task {
	seen := make(map[string]bool, len(launched)+1)
	seen[candidate.PlanTaskID] = true
	var active []marshal.Task
	for _, other := range run.Tasks {
		if (other.State != marshal.Dispatched && !other.StaleProcessExitUnverified) || seen[other.PlanTaskID] {
			continue
		}
		seen[other.PlanTaskID] = true
		active = append(active, other)
	}
	for _, d := range launched {
		if seen[d.TaskID] {
			continue
		}
		seen[d.TaskID] = true
		if idx := taskIndex(run, d.TaskID); idx >= 0 {
			active = append(active, run.Tasks[idx])
		} else {
			active = append(active, marshal.Task{PlanTaskID: d.TaskID})
		}
	}
	return active
}

func extremeProviderLimit(run marshal.Run, limits map[string]int, normWorker string) int {
	limit, ok := limits[normWorker]
	if !ok || limit <= 0 {
		if run.Settings.UltraConcurrency > 0 {
			limit = run.Settings.UltraConcurrency
		} else {
			limit = 4
		}
	}
	return limit
}

// EvaluateDispatchAdmission evaluates whether candidate task t can launch
// concurrently under gate, returning a typed serial blocker when it cannot.
func EvaluateDispatchAdmission(gate marshal.CapabilityGate, run marshal.Run, t marshal.Task, taskIdx int, launched ...MarshalDispatch) DispatchAdmission {
	taskID := safeAdmissionTaskID(t.PlanTaskID)
	if adm, blocked := unmergedDependencyAdmission(run, t); blocked {
		return adm
	}
	if taskIdx < 0 {
		taskIdx = taskIndex(run, t.PlanTaskID)
	}

	active := activeDispatchTasks(run, t, launched)
	activeIDs := make([]string, 0, len(active))
	for _, other := range active {
		activeIDs = append(activeIDs, other.PlanTaskID)
	}

	// Only an immutable approved ULTRA plan uses isolated task lanes. Older and
	// Standard Process05 runs retain their serial dispatch and merge ordering.
	if run.Process05Bound && !run.Process05Parallel && t.Mode == marshal.Governed {
		if len(active) > 0 {
			return DispatchAdmission{
				Allowed:         false,
				Code:            AdmissionSerialPlan,
				Reason:          "governed plan tasks dispatch one at a time",
				TaskID:          taskID,
				BlockingTaskIDs: safeAdmissionTaskIDs(activeIDs),
			}
		}
		if taskIdx > 0 {
			var earlier []string
			for j := 0; j < taskIdx && j < len(run.Tasks); j++ {
				if run.Tasks[j].State != marshal.Merged {
					earlier = append(earlier, run.Tasks[j].PlanTaskID)
				}
			}
			if len(earlier) > 0 {
				return DispatchAdmission{
					Allowed:         false,
					Code:            AdmissionSerialPlan,
					Reason:          "earlier governed task must be merged before dispatch",
					TaskID:          taskID,
					BlockingTaskIDs: safeAdmissionTaskIDs(earlier),
				}
			}
		}
	}

	if marshal.TierPolicy(gate, run.Settings).Tier != marshal.Ultra {
		if len(active) >= 1 {
			return DispatchAdmission{
				Allowed:         false,
				Code:            AdmissionStandardTier,
				Reason:          "dispatch concurrency reached (standard tier)",
				TaskID:          taskID,
				BlockingTaskIDs: safeAdmissionTaskIDs(activeIDs),
			}
		}
		return DispatchAdmission{Allowed: true, TaskID: taskID}
	}

	limits, globalCap := extremeProviderLimits(run.Settings)
	activeProvider := 0
	normWorker := normalizeProvider(t.Worker)
	var broadBlockers, overlapBlockers []string
	for _, other := range active {
		if normalizeProvider(other.Worker) == normWorker {
			activeProvider++
		}
		switch taskScopeConflict(t.Files, other.Files) {
		case AdmissionScopeBroad:
			broadBlockers = append(broadBlockers, other.PlanTaskID)
		case AdmissionScopeOverlap:
			overlapBlockers = append(overlapBlockers, other.PlanTaskID)
		}
	}

	if len(broadBlockers) > 0 {
		blocking := safeAdmissionTaskIDs(broadBlockers)
		reason := "broad file scope forces serial dispatch (use exact project-relative file paths for parallel dispatch)"
		if len(blocking) > 0 {
			reason = "broad file scope forces serial dispatch while " + strings.Join(blocking, ", ") + " active (use exact project-relative file paths for parallel dispatch)"
		}
		return DispatchAdmission{
			Allowed:         false,
			Code:            AdmissionScopeBroad,
			Reason:          reason,
			TaskID:          taskID,
			BlockingTaskIDs: blocking,
		}
	}
	if len(overlapBlockers) > 0 {
		blocking := safeAdmissionTaskIDs(overlapBlockers)
		reason := "conflicting file scope already active"
		if len(blocking) > 0 {
			reason += ": overlaps " + strings.Join(blocking, ", ")
		}
		return DispatchAdmission{
			Allowed:         false,
			Code:            AdmissionScopeOverlap,
			Reason:          reason,
			TaskID:          taskID,
			BlockingTaskIDs: blocking,
		}
	}

	if len(active) >= globalCap {
		return DispatchAdmission{
			Allowed:         false,
			Reason:          "dispatch global cap reached",
			TaskID:          taskID,
			BlockingTaskIDs: safeAdmissionTaskIDs(activeIDs),
		}
	}

	if activeProvider >= extremeProviderLimit(run, limits, normWorker) {
		return DispatchAdmission{
			Allowed:         false,
			Reason:          fmt.Sprintf("provider %s dispatch limit reached", t.Worker),
			TaskID:          taskID,
			BlockingTaskIDs: safeAdmissionTaskIDs(activeIDs),
		}
	}

	return DispatchAdmission{Allowed: true, TaskID: taskID}
}

func canLaunchCandidate(gate marshal.CapabilityGate, run marshal.Run, t marshal.Task, launched []MarshalDispatch) (bool, bool) {
	if run.Process05Bound && !run.Process05Parallel && t.Mode == marshal.Governed {
		if len(launched) > 0 {
			return false, true
		}
		for _, other := range run.Tasks {
			if holdsWorkerSlot(other, t) {
				return false, true
			}
		}
		if idx := taskIndex(run, t.PlanTaskID); idx > 0 {
			for j := 0; j < idx; j++ {
				if run.Tasks[j].State != marshal.Merged {
					return false, true
				}
			}
		}
	}

	// A refreshed run already contains launches from this round. Count only
	// reservations that have not yet appeared in the persisted task states.
	pending := make([]MarshalDispatch, 0, len(launched))
	for _, d := range launched {
		i := taskIndex(run, d.TaskID)
		if i < 0 || !holdsWorkerSlot(run.Tasks[i], t) {
			pending = append(pending, d)
		}
	}
	launched = pending
	if marshal.TierPolicy(gate, run.Settings).Tier != marshal.Ultra {
		policy := marshal.TierPolicy(gate, run.Settings)
		active := len(launched)
		for _, other := range run.Tasks {
			if holdsWorkerSlot(other, t) {
				active++
			}
		}
		if active >= policy.Concurrency {
			return false, true
		}
		return true, false
	}

	limits, globalCap := extremeProviderLimits(run.Settings)

	activeTotal := 0
	for _, other := range run.Tasks {
		if holdsWorkerSlot(other, t) {
			activeTotal++
		}
	}
	activeTotal += len(launched)

	if activeTotal >= globalCap {
		return false, true
	}

	normWorker := normalizeProvider(t.Worker)
	activeProvider := 0
	for _, other := range run.Tasks {
		if holdsWorkerSlot(other, t) && normalizeProvider(other.Worker) == normWorker {
			activeProvider++
		}
	}
	for _, d := range launched {
		idx := taskIndex(run, d.TaskID)
		if idx >= 0 && normalizeProvider(run.Tasks[idx].Worker) == normWorker {
			activeProvider++
		}
	}

	if activeProvider >= extremeProviderLimit(run, limits, normWorker) {
		return false, false
	}

	adm := EvaluateDispatchAdmission(gate, run, t, taskIndex(run, t.PlanTaskID), launched...)
	if !adm.Allowed {
		return false, false
	}

	return true, false
}

func shouldStopAfterLaunch(run marshal.Run, t marshal.Task) bool {
	return run.Process05Bound && t.Mode == marshal.Governed && !run.Process05Parallel
}

func checkDispatchAdmission(gate marshal.CapabilityGate, run marshal.Run, t marshal.Task, taskIdx int) error {
	adm := EvaluateDispatchAdmission(gate, run, t, taskIdx)
	if !adm.Allowed {
		return &DispatchAdmissionError{Admission: adm}
	}
	return nil
}

// CollectHandIn can successfully return a task instead of handing it in, for
// example after a validation failure or timeout. Only actual hand-ins are reviewed.
func (s *MarshalService) reviewCollectedDispatch(ctx context.Context, runID, taskID string) error {
	run, _, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	i := taskIndex(run, taskID)
	if i < 0 {
		return errors.New("collected task is missing")
	}
	if run.Tasks[i].State != marshal.HandedIn || run.State == marshal.AwaitingUser {
		return nil
	}
	_, err = s.Review(ctx, runID, taskID, unknownMarshalCharge())
	return err
}

func serialProcess05Base(gate marshal.CapabilityGate, run marshal.Run) bool {
	return run.Process05Bound && (!run.Process05Parallel || marshal.TierPolicy(gate, run.Settings).Tier != marshal.Ultra)
}

func shouldStopAfterLaunchGate(gate marshal.CapabilityGate, run marshal.Run, t marshal.Task) bool {
	return serialProcess05Base(gate, run) && t.Mode == marshal.Governed
}

// A returned governed task of an approved ULTRA plan reruns inside the same
// run: its next attempt opens a fresh task lane from the same approved task
// binding (execution.OpenTaskLaneRework), and every review, check and
// verification applies to it unchanged. A serial Process05 run, and a task
// whose worker reached the rework limit, still stop: the former for a newly
// approved plan, the latter for the person.
func pauseReturnedProcess05Task(run *marshal.Run, task marshal.Task) {
	if !run.Process05Bound || task.Mode != marshal.Governed || (task.State != marshal.Returned && task.State != marshal.Reassigned) {
		return
	}
	if reworkInTaskLane(*run, task) {
		return
	}
	if run.Process05Parallel {
		pauseMarshal(run, marshal.Pause{Reason: "governed task " + task.PlanTaskID + " reached its rework limit",
			Resolutions: []string{reworkLimitResolution(task.PlanTaskID)}, Code: marshal.CodeGovernedRework, Action: marshal.PauseUsePlan, TaskID: task.PlanTaskID})
		return
	}
	pauseMarshal(run, marshal.Pause{Reason: "returned governed task " + task.PlanTaskID + " requires a new approved Process 04 plan",
		Resolutions: []string{"review the retained failed checks and return reasons; create and approve a new plan, then use /marshal use-plan for fresh task authorization"}, Code: marshal.CodeGovernedRework, Action: marshal.PauseUsePlan, TaskID: task.PlanTaskID})
}

// reworkLimitResolution is the working way on from a rework-limit stop: the
// task moves to another worker inside this run, so its returns stay counted
// and the run keeps its merged and accepted work. /marshal use-plan would
// start every task of the plan again instead.
func reworkLimitResolution(taskID string) string {
	return "Move " + taskID + " to another worker: /marshal reassign " + taskID + " <worker>, then /marshal resume (the Marshal proposes {\"action\":\"reassign\",\"task_id\":\"" + taskID + "\",\"target_worker\":\"<worker>\"}). Its returns stay counted and the run keeps its merged and accepted work; /marshal use-plan would start every task again."
}

// reworkLimitEscalationPause stops a task returned again after its
// reassignment. The stop is the person's, and it names the way on: Enter
// gives the task one more attempt with its current worker (/marshal retry,
// then /marshal resume), whose next brief carries every return reason; or the
// task moves to another worker. Earlier returns stay counted, so a further
// return stops again. A plain resume would leave the task escalated.
func reworkLimitEscalationPause(t marshal.Task) marshal.Pause {
	return marshal.Pause{Reason: "worker rework limit reached for " + t.PlanTaskID,
		Resolutions: []string{"Press Enter to give " + t.PlanTaskID + " one more attempt with " + t.Worker + " (/marshal retry " + t.PlanTaskID + ", then /marshal resume), or move it to another worker with /marshal reassign " + t.PlanTaskID + " <worker>. Its return reasons go with it."},
		Code:        marshal.CodeReworkLimit, Action: marshal.PauseRetryTask, TaskID: t.PlanTaskID}
}

// reworkInTaskLane reports whether a returned task reruns in a fresh task lane
// of its run. It restarts from the approved base: the returned attempt stays
// reachable as evidence and its review reasons reach the next brief.
func reworkInTaskLane(run marshal.Run, t marshal.Task) bool {
	return run.Process05Bound && run.Process05Parallel && t.Mode == marshal.Governed && t.State == marshal.Returned
}

// taskLaneRework is the attempt number of a task's next lane: the returns it
// has had in this run. It never decreases, so a later attempt never reuses an
// earlier lane.
func taskLaneRework(t marshal.Task) int { return taskReturns(t) }

func prepareReturnedTaskRetry(run marshal.Run, t *marshal.Task) {
	if reworkInTaskLane(run, *t) && taskReturns(*t) == 0 {
		// An uncounted return must still move beyond the completed lane.
		t.LaneReworkBase++
	}
}

// taskReturns counts every return a task has had in this run, including
// returns a reassignment or retry retired, so attempt numbers never repeat.
func taskReturns(t marshal.Task) int {
	n := t.LaneReworkBase
	for _, returns := range t.ReturnsByAgent {
		n += returns
	}
	return n
}

// reworkFailedLaneWorker reports whether a failed worker's task goes back for
// rework in its run: only governed tasks of a parallel Process05 plan, whose
// next attempt opens a fresh lane.
func reworkFailedLaneWorker(run marshal.Run, t marshal.Task) bool {
	return run.Process05Bound && run.Process05Parallel && t.Mode == marshal.Governed
}

const isExtremeBuild = true

// retireWorkerReturns gives a worker a fresh return count on reassignment or
// retry. The retired returns stay counted for lane numbering.
func retireWorkerReturns(t *marshal.Task, worker string) {
	t.LaneReworkBase += t.ReturnsByAgent[worker]
	t.ReturnsByAgent[worker] = 0
}

// governedProviderSwitchAllowed reports whether the operator may switch a
// governed Process 05 task to another provider within its plan version.
const governedProviderSwitchAllowed = true

// mergeInPlanOrder keeps serial governed runs merging in plan order. Other
// runs merge each accepted task once its dependencies are merged, so an
// independent task never waits behind unrelated slow work.
func mergeInPlanOrder(run marshal.Run) bool {
	return run.Process05Bound && !run.Process05Parallel
}
