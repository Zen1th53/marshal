//go:build !governed

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
)

type providerTransportAdmission struct {
	mu              sync.Mutex
	released        bool
	engine          *Engine
	run, task       string
	parent, binding string
	revision        int64
	digest          [32]byte
}

// ProviderTransportAdmissionPause seals only a currently owned canonical
// pre-provider task invocation. Public TaskResult fields cannot claim no call.
// Outside an active invocation it remains an ordinary, charged pause.
func (e *Engine) ProviderTransportAdmissionPause(ctx context.Context, task TaskExecution, req ApprovalRequest) TaskResult {
	result := TaskResult{TaskID: task.TaskID, NeedsApproval: true, ApprovalReq: &req}
	if ctx.Err() != nil || req.OperationType != "PROVIDER_TRANSPORT" || req.TargetResource == "" || req.Scope == "" || req.CurrentState == "" || task.NativeTurn != nil {
		return result
	}
	if task.AssignedHarness != "codex" && task.AssignedHarness != "claude" && task.AssignedHarness != "opencode" && task.AssignedHarness != "agy" {
		return result
	}
	e.operationMu.Lock()
	_, active := e.activeRuns[task.RunID]
	e.operationMu.Unlock()
	if !active {
		return result
	}
	run, err := e.store.GetRun(ctx, task.RunID)
	if err != nil || run.State != RunRunning {
		return result
	}
	actual, ok := run.Tasks[task.TaskID]
	if !ok || actual.State != TaskRunning || actual.NativeTurn != nil || actual.RunRevision != task.RunRevision || actual.AssignedHarness != task.AssignedHarness || actual.CanonicalTaskID != task.CanonicalTaskID {
		return result
	}
	body, _ := json.Marshal(req)
	result.transportAdmission = &providerTransportAdmission{engine: e, run: task.RunID, task: task.TaskID, parent: run.ParentRunID, binding: run.ParentBinding, revision: task.RunRevision, digest: sha256.Sum256(body)}
	return result
}
func (e *Engine) ownsProviderTransportAdmission(result TaskResult, run ExecutionRun, task TaskExecution) bool {
	marker := result.transportAdmission
	if result.TaskID != task.TaskID || task.NativeTurn != nil || result.Duration != 0 || result.Success || len(result.EvidenceList) != 0 || len(result.Claims) != 0 || len(result.FilesModified) != 0 || result.ErrorMessage != "" || marker == nil || marker.engine != e || marker.run != run.RunID || marker.parent != run.ParentRunID || marker.binding != run.ParentBinding || marker.task != task.TaskID || marker.revision != task.RunRevision || !result.NeedsApproval || result.ApprovalReq == nil || result.ApprovalReq.OperationType != "PROVIDER_TRANSPORT" || result.NativeTurn != nil || result.NativeApprovalID != "" || result.InputTokens != 0 || result.OutputTokens != 0 {
		return false
	}
	body, _ := json.Marshal(result.ApprovalReq)
	return sha256.Sum256(body) == marker.digest
}

// Only the exact private engine marker can release its own zero-call parent
// reservation. Attempts remain monotonic; crashes before this point keep the
// conservative reservation. No operator/provider command can refund a call.
func (e *Engine) releaseProviderTransportReservation(ctx context.Context, result TaskResult, run ExecutionRun, task TaskExecution) error {
	if !e.ownsProviderTransportAdmission(result, run, task) {
		return ErrRunConflict
	}
	marker := result.transportAdmission
	marker.mu.Lock()
	defer marker.mu.Unlock()
	if marker.released {
		return ErrRunConflict
	}
	if run.ParentRunID == "" {
		marker.released = true
		return nil
	}
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.laneCommitMu.Lock()
	defer e.laneCommitMu.Unlock()
	current, err := e.store.GetRun(ctx, run.RunID)
	if err != nil {
		return err
	}
	actual, ok := current.Tasks[task.TaskID]
	if !ok || current.State != RunRunning || actual.State != TaskRunning || actual.RunRevision != task.RunRevision || actual.Attempts != task.Attempts || current.ParentRunID != run.ParentRunID || current.LaneTaskID != run.LaneTaskID || current.ParentBinding != run.ParentBinding || actual.NativeTurn != nil {
		return ErrRunConflict
	}
	parent, err := e.store.GetRun(ctx, run.ParentRunID)
	if err != nil {
		return err
	}
	original, ok := parent.Tasks[task.TaskID]
	if !ok || parent.TaskLanes[task.TaskID] != run.RunID || run.ParentBinding != taskLaneBinding(parent, original, run.BaseCommit) || parent.State.IsTerminal() || parent.State == RunPaused || parent.ParallelCallsReserved < 1 {
		return ErrRunConflict
	}
	if err := e.ValidateRunGoal(ctx, parent); err != nil {
		return err
	}
	parent.ParallelCallsReserved--
	if run.BudgetConsumed.ModelCalls >= 1 {
		if parent.ParallelRetriesReserved < 1 {
			return ErrRunConflict
		}
		parent.ParallelRetriesReserved--
	}
	if err := e.persistLaneSnapshotLocked(ctx, &parent); err != nil {
		return err
	}
	marker.released = true
	return nil
}
