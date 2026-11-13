//go:build governed

package app

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func canLaunchCandidate(gate marshal.CapabilityGate, run marshal.Run, t marshal.Task, launched []MarshalDispatch) (bool, bool) {
	policy := marshal.TierPolicy(gate, run.Settings)
	if len(launched) >= policy.Concurrency {
		return false, true
	}
	return true, false
}

func shouldStopAfterLaunch(run marshal.Run, t marshal.Task) bool {
	return run.Process05Bound && t.Mode == marshal.Governed
}

func checkDispatchAdmission(gate marshal.CapabilityGate, run marshal.Run, t marshal.Task, taskIndex int) error {
	policy := marshal.TierPolicy(gate, run.Settings)
	active := 0
	for _, other := range run.Tasks {
		if other.State == marshal.Dispatched {
			active++
		}
	}
	if active >= policy.Concurrency {
		return errors.New("dispatch concurrency reached")
	}
	if run.Process05Bound && t.Mode == marshal.Governed && active > 0 {
		return errors.New("governed plan tasks dispatch one at a time")
	}
	if run.Process05Bound && t.Mode == marshal.Governed {
		for j := 0; j < taskIndex; j++ {
			if run.Tasks[j].State != marshal.Merged {
				return errors.New("earlier governed task must be merged before dispatch")
			}
		}
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

// independentCrossReviewRequired refuses a cross-review by the worker's own
// provider instead of falling back to it.
const independentCrossReviewRequired = false
