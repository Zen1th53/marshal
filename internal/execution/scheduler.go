package execution

import (
	"fmt"
	"sort"
	"time"
)

// Scheduler coordinates deterministic task readiness, bounded parallelism, and resource safety.
type Scheduler struct {
	maxConcurrency int
	leases         *LeaseManager
}

// NewScheduler creates a new execution Scheduler.
func NewScheduler(maxConcurrency int, lm *LeaseManager) *Scheduler {
	if maxConcurrency <= 0 {
		maxConcurrency = 4
	}
	return &Scheduler{
		maxConcurrency: maxConcurrency,
		leases:         lm,
	}
}

// RefreshTaskReadiness evaluates the DAG dependencies of all tasks in the run.
// Tasks whose dependencies are all in COMPLETED_PENDING_VERIFY transition from PENDING to READY.
func (s *Scheduler) RefreshTaskReadiness(run *ExecutionRun, now time.Time) ([]string, error) {
	if run == nil {
		return nil, fmt.Errorf("%w: nil run", ErrRunInvalid)
	}

	var newlyReady []string

	for id, task := range run.Tasks {
		if task.State != TaskPending {
			continue
		}

		depsMet := true
		for _, depID := range task.Dependencies {
			depTask, exists := run.Tasks[depID]
			if !exists {
				// Dependent task not in plan
				depsMet = false
				break
			}
			if depTask.State != TaskCompletedPendingVerify {
				depsMet = false
				break
			}
		}

		if depsMet {
			if err := TransitionTask(&task, TaskReady, now); err != nil {
				return nil, err
			}
			run.Tasks[id] = task
			newlyReady = append(newlyReady, id)
		}
	}

	sort.Strings(newlyReady)
	return newlyReady, nil
}

// NextSchedulableTasks returns the next batch of tasks ready to execute, respecting
// dependency satisfaction, concurrency bounds, and resource conflict freedom.
func (s *Scheduler) NextSchedulableTasks(run *ExecutionRun, now time.Time) ([]TaskExecution, error) {
	if run == nil {
		return nil, fmt.Errorf("%w: nil run", ErrRunInvalid)
	}

	// 1. Refresh dependencies
	if _, err := s.RefreshTaskReadiness(run, now); err != nil {
		return nil, err
	}

	// Count currently active/running tasks
	activeCount := 0
	var activeResourceLocks [][]string
	for _, task := range run.Tasks {
		if task.State == TaskRunning || task.State == TaskWaitingTool || task.State == TaskAssigned {
			activeCount++
			if len(task.TargetFiles) > 0 {
				activeResourceLocks = append(activeResourceLocks, task.TargetFiles)
			}
		}
	}

	availableSlots := s.maxConcurrency - activeCount
	if availableSlots <= 0 {
		return nil, nil // Concurrency limit reached
	}

	// 2. Gather READY tasks
	var candidates []TaskExecution
	for _, task := range run.Tasks {
		if task.State == TaskReady {
			candidates = append(candidates, task)
		}
	}

	// Deterministic sorting: tasks with more dependencies/mutations first, then task ID
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Mutates != candidates[j].Mutates {
			return candidates[i].Mutates
		}
		return candidates[i].TaskID < candidates[j].TaskID
	})

	var scheduled []TaskExecution
	for _, candidate := range candidates {
		if len(scheduled) >= availableSlots {
			break
		}

		// Resource conflict check against currently running tasks
		conflict := false
		if candidate.Mutates && len(candidate.TargetFiles) > 0 {
			for _, runningLocks := range activeResourceLocks {
				if ResourcesOverlap(candidate.TargetFiles, runningLocks) {
					conflict = true
					break
				}
			}
		}

		if conflict {
			continue // Postpone this task until conflicting tasks finish
		}

		scheduled = append(scheduled, candidate)
		if candidate.Mutates && len(candidate.TargetFiles) > 0 {
			activeResourceLocks = append(activeResourceLocks, candidate.TargetFiles)
		}
	}

	return scheduled, nil
}
