//go:build !governed

package execution

// Ordinary task approval has no live native turn to resume. Preserve the
// exact approved lane as paused until the orchestration dispatch consumes it.
func privateTaskLaneApprovalReady(run *ExecutionRun, approve bool) {
	if run.ParentRunID == "" || !approve {
		return
	}
	task := run.Tasks[run.LaneTaskID]
	if task.State == TaskReady && task.NativeTurn == nil {
		run.State, run.CurrentPhase = RunPaused, PhasePaused
	}
}
