//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func normalizeProvider(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	switch p {
	case "antigravity", "agy":
		return "agy"
	case "claude-code", "claude":
		return "claude"
	case "codex":
		return "codex"
	case "opencode":
		return "opencode"
	default:
		return p
	}
}

func extremeProviderLimits(s marshal.Settings) (map[string]int, int) {
	limits := map[string]int{
		"codex":    4,
		"claude":   2,
		"agy":      4,
		"opencode": 4,
	}
	globalCap := 12
	if s.GlobalCap > 0 {
		globalCap = s.GlobalCap
	}
	if s.ProviderLimits.Codex > 0 {
		limits["codex"] = s.ProviderLimits.Codex
	}
	if s.ProviderLimits.Claude > 0 {
		limits["claude"] = s.ProviderLimits.Claude
	}
	if s.ProviderLimits.Agy > 0 {
		limits["agy"] = s.ProviderLimits.Agy
	}
	if s.ProviderLimits.OpenCode > 0 {
		limits["opencode"] = s.ProviderLimits.OpenCode
	}
	return limits, globalCap
}

// Missing, root, escaping and wildcard scopes cannot establish independence.
// Treat them as repository-wide until concrete file scopes are available.
func broadFileScope(path string) bool {
	path = strings.TrimSpace(path)
	clean := filepath.Clean(path)
	return path == "" || clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsAny(path, "*?[")
}

func pathsOverlap(a, b string) bool {
	if broadFileScope(a) || broadFileScope(b) {
		return true
	}
	a, b = filepath.Clean(strings.TrimSpace(a)), filepath.Clean(strings.TrimSpace(b))
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func taskFilesOverlap(files1, files2 []string) bool {
	if len(files1) == 0 || len(files2) == 0 {
		return true
	}
	for _, f1 := range files1 {
		for _, f2 := range files2 {
			if pathsOverlap(f1, f2) {
				return true
			}
		}
	}
	return false
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
	return other.State == marshal.Dispatched || (other.State == marshal.Queued && other.Process05WaitApprovalID != "")
}

func canLaunchCandidate(gate marshal.CapabilityGate, run marshal.Run, t marshal.Task, launched []MarshalDispatch) (bool, bool) {
	if marshal.TierPolicy(gate, run.Settings).Tier != marshal.Ultra {
		policy := marshal.TierPolicy(gate, run.Settings)
		if len(launched) >= policy.Concurrency {
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

	limit, ok := limits[normWorker]
	if !ok || limit <= 0 {
		if run.Settings.UltraConcurrency > 0 {
			limit = run.Settings.UltraConcurrency
		} else {
			limit = 4
		}
	}
	if activeProvider >= limit {
		return false, false
	}

	for _, other := range run.Tasks {
		if other.State == marshal.Dispatched {
			if taskFilesOverlap(t.Files, other.Files) {
				return false, false
			}
		}
	}
	for _, d := range launched {
		idx := taskIndex(run, d.TaskID)
		if idx >= 0 {
			if taskFilesOverlap(t.Files, run.Tasks[idx].Files) {
				return false, false
			}
		}
	}

	return true, false
}

func shouldStopAfterLaunch(run marshal.Run, t marshal.Task) bool {
	return run.Process05Bound && t.Mode == marshal.Governed && !run.Process05Parallel
}

func checkDispatchAdmission(gate marshal.CapabilityGate, run marshal.Run, t marshal.Task, taskIndex int) error {
	// Only an immutable approved ULTRA plan uses isolated task lanes. Older and
	// Standard Process05 runs retain their serial dispatch and merge ordering.
	if serialProcess05Base(gate, run) && t.Mode == marshal.Governed {
		for _, other := range run.Tasks {
			if other.State == marshal.Dispatched {
				return errors.New("governed plan tasks dispatch one at a time")
			}
		}
		for j := 0; j < taskIndex; j++ {
			if run.Tasks[j].State != marshal.Merged {
				return errors.New("earlier governed task must be merged before dispatch")
			}
		}
	}

	if marshal.TierPolicy(gate, run.Settings).Tier != marshal.Ultra {
		active := 0
		for _, other := range run.Tasks {
			if other.State == marshal.Dispatched {
				active++
			}
		}
		if active >= 1 {
			return errors.New("dispatch concurrency reached")
		}
		return nil
	}

	limits, globalCap := extremeProviderLimits(run.Settings)
	activeTotal := 0
	activeProvider := 0
	normWorker := normalizeProvider(t.Worker)

	for _, other := range run.Tasks {
		if other.State == marshal.Dispatched {
			activeTotal++
			if normalizeProvider(other.Worker) == normWorker {
				activeProvider++
			}
			if taskFilesOverlap(t.Files, other.Files) {
				return errors.New("conflicting file scope already active")
			}
		}
	}

	if activeTotal >= globalCap {
		return errors.New("dispatch global cap reached")
	}

	limit, ok := limits[normWorker]
	if !ok || limit <= 0 {
		if run.Settings.UltraConcurrency > 0 {
			limit = run.Settings.UltraConcurrency
		} else {
			limit = 4
		}
	}
	if activeProvider >= limit {
		return fmt.Errorf("provider %s dispatch limit reached", t.Worker)
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

// independentCrossReviewRequired refuses a cross-review by the worker's own
// provider instead of falling back to it.
const independentCrossReviewRequired = true
