//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func activeDispatchTaskIDs(run marshal.Run, candidate marshal.Task, launched []MarshalDispatch) []string {
	seen := map[string]bool{candidate.PlanTaskID: true}
	var ids []string
	for _, other := range run.Tasks {
		if (other.State == marshal.Dispatched || other.StaleProcessExitUnverified) && !seen[other.PlanTaskID] {
			seen[other.PlanTaskID] = true
			ids = append(ids, other.PlanTaskID)
		}
	}
	for _, d := range launched {
		if !seen[d.TaskID] {
			seen[d.TaskID] = true
			ids = append(ids, d.TaskID)
		}
	}
	return ids
}

// EvaluateDispatchAdmission evaluates whether candidate task t can launch
// concurrently under gate in the governed edition, where Process 05 governed
// tasks always dispatch serially.
func EvaluateDispatchAdmission(gate marshal.CapabilityGate, run marshal.Run, t marshal.Task, taskIdx int, launched ...MarshalDispatch) DispatchAdmission {
	taskID := safeAdmissionTaskID(t.PlanTaskID)
	if adm, blocked := unmergedDependencyAdmission(run, t); blocked {
		return adm
	}
	if taskIdx < 0 {
		taskIdx = taskIndex(run, t.PlanTaskID)
	}
	activeIDs := activeDispatchTaskIDs(run, t, launched)
	if run.Process05Bound && t.Mode == marshal.Governed {
		if len(activeIDs) > 0 {
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
	policy := marshal.TierPolicy(gate, run.Settings)
	if len(activeIDs) >= policy.Concurrency {
		code := DispatchAdmissionCode("")
		if policy.Tier != marshal.Ultra {
			code = AdmissionStandardTier
		}
		return DispatchAdmission{
			Allowed:         false,
			Code:            code,
			Reason:          "dispatch concurrency reached",
			TaskID:          taskID,
			BlockingTaskIDs: safeAdmissionTaskIDs(activeIDs),
		}
	}
	return DispatchAdmission{Allowed: true, TaskID: taskID}
}

func canLaunchCandidate(gate marshal.CapabilityGate, run marshal.Run, t marshal.Task, launched []MarshalDispatch) (bool, bool) {
	adm := EvaluateDispatchAdmission(gate, run, t, taskIndex(run, t.PlanTaskID), launched...)
	if !adm.Allowed {
		return false, true
	}
	return true, false
}

func shouldStopAfterLaunch(run marshal.Run, t marshal.Task) bool {
	return run.Process05Bound && t.Mode == marshal.Governed
}

func checkDispatchAdmission(gate marshal.CapabilityGate, run marshal.Run, t marshal.Task, taskIdx int) error {
	adm := EvaluateDispatchAdmission(gate, run, t, taskIdx)
	if !adm.Allowed {
		return &DispatchAdmissionError{Admission: adm}
	}
	return nil
}

func (s *MarshalService) reviewCollectedDispatch(ctx context.Context, runID, taskID string) error {
	_, err := s.Review(ctx, runID, taskID, zeroMarshalCharge())
	return err
}

func serialProcess05Base(_ marshal.CapabilityGate, run marshal.Run) bool { return run.Process05Bound }

func shouldStopAfterLaunchGate(_ marshal.CapabilityGate, run marshal.Run, t marshal.Task) bool {
	return shouldStopAfterLaunch(run, t)
}

func pauseReturnedProcess05Task(_ *marshal.Run, _ marshal.Task) {}

// reworkLimitEscalationPause stops a task returned again after its
// reassignment; the person resolves the escalation.
func reworkLimitEscalationPause(marshal.Task) marshal.Pause {
	return marshal.Pause{Reason: "worker rework limit reached", Resolutions: []string{"resolve the escalation or amend the plan"}, Code: marshal.CodeReworkLimit, Action: marshal.PauseResume}
}

func reworkInTaskLane(_ marshal.Run, _ marshal.Task) bool { return false }

func prepareReturnedTaskRetry(_ marshal.Run, _ *marshal.Task) {}

func reworkFailedLaneWorker(_ marshal.Run, _ marshal.Task) bool { return false }

const isExtremeBuild = false

// retireWorkerReturns gives a worker a fresh return count on reassignment or
// retry.
func retireWorkerReturns(t *marshal.Task, worker string) { t.ReturnsByAgent[worker] = 0 }

// taskReturns counts every return a task has had in this run.
func taskReturns(t marshal.Task) int {
	n := 0
	for _, returns := range t.ReturnsByAgent {
		n += returns
	}
	return n
}

// governedProviderSwitchAllowed reports whether the operator may switch a
// governed Process 05 task to another provider within its plan version.
const governedProviderSwitchAllowed = false

// mergeInPlanOrder keeps governed runs merging in plan order.
func mergeInPlanOrder(marshal.Run) bool { return true }
