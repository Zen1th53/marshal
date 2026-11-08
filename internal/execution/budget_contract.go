package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/budget"
	"github.com/Zen1th53/marshal/internal/model"
)

var ErrBudgetSettlement = errors.New("budget settlement failed")

type goalCallReserver interface {
	ReserveGoalCall(context.Context, string, string, int64, budget.CallRequest) (bool, error)
}
type goalCallReleaser interface {
	ReleaseGoalCall(context.Context, string, string, int64, string, bool) error
}
type goalCallSettler interface {
	SettleGoalCall(context.Context, string, string, int64, string, budget.CallSettlement) error
}

type goalDispatchBarrier interface {
	BeginGoalDispatch(context.Context, string, string, int64) (func(), error)
}

func (e *Engine) dispatchBudgetedTask(ctx context.Context, run ExecutionRun, t TaskExecution, pkg ConstraintPackage, path string, h WorkerHarness) (TaskResult, error) {
	if err := e.ValidateRunGoal(ctx, run); err != nil {
		return TaskResult{}, err
	}
	var remainingDuration time.Duration
	if run.GoalID != "" {
		target := run.SessionID
		if target == "" {
			target = run.GoalID
		}
		g, err := e.goalReader.GetActiveGoalContract(ctx, target)
		if err != nil {
			return TaskResult{}, err
		}
		if reserver, ok := e.goalReader.(goalCallReserver); ok {
			elapsed := time.Duration(0)
			if !run.StartedAt.IsZero() {
				elapsed = time.Since(run.StartedAt)
			}
			// Handoffs are canonical typed handoff records, not guessed provider/role
			// changes. A run hands off once for each new durable record.
			newHandoffs := len(run.Handoffs) - run.BudgetConsumed.Handoffs
			if newHandoffs < 0 {
				newHandoffs = 0
			}
			admitted, err := reserver.ReserveGoalCall(ctx, run.SessionID, run.GoalID, run.GoalRevision, budget.CallRequest{Key: fmt.Sprintf("%s/%s/%d", run.RunID, t.TaskID, t.Attempts), Process: budget.ProcessBuild, RunID: run.RunID, Duration: elapsed, Handoffs: newHandoffs, Retry: t.Attempts > 1})
			if err != nil {
				return TaskResult{}, err
			}
			if !admitted {
				return TaskResult{}, fmt.Errorf("%w: call intent was already admitted; reconciliation required", ErrRunBlocked)
			}
		} else if g.BudgetLimits != nil {
			return TaskResult{}, fmt.Errorf("%w: durable budget authority is unavailable", ErrRunBlocked)
		}
		if g.BudgetLimits != nil && g.BudgetLimits.MaxDuration > 0 {
			used := time.Duration(0)
			if reader, ok := e.goalReader.(interface {
				GoalBudgetConsumed(context.Context, string, string, int64) (model.ConsumedBudget, error)
			}); ok {
				consumed, err := reader.GoalBudgetConsumed(ctx, run.SessionID, run.GoalID, run.GoalRevision)
				if err != nil {
					return TaskResult{}, err
				}
				used = consumed.Duration
			} else if !run.StartedAt.IsZero() {
				used = time.Since(run.StartedAt)
			}
			remainingDuration = g.BudgetLimits.MaxDuration - used
			if remainingDuration <= 0 {
				return TaskResult{}, fmt.Errorf("%s: duration reached", model.ReasonBudgetExhaustedWallClock)
			}
		}
	}
	// Preparation may read/write SQLite. Acquire operationMu only at the
	// backend launch boundary, always before opening the dispatch transaction.
	turnCtx, cancel := context.WithCancel(ctx)
	if remainingDuration > 0 {
		cancel()
		turnCtx, cancel = context.WithTimeout(ctx, remainingDuration)
	}
	defer cancel()
	e.operationMu.Lock()
	if e.turnCancels == nil {
		e.turnCancels = map[string]context.CancelFunc{}
	}
	e.turnCancels[run.RunID] = cancel
	e.operationMu.Unlock()
	turnCtx = WithWorkerLaunchAdmission(turnCtx, func(launchCtx context.Context) (func(), error) {
		e.operationMu.Lock()
		latest, err := e.store.GetRun(launchCtx, run.RunID)
		if err != nil || latest.State != RunRunning {
			e.operationMu.Unlock()
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%w: run stopped before dispatch", ErrRunBlocked)
		}
		release := func() {}
		if barrier, ok := e.goalReader.(goalDispatchBarrier); ok && run.GoalID != "" {
			release, err = barrier.BeginGoalDispatch(launchCtx, run.SessionID, run.GoalID, run.GoalRevision)
			if err != nil {
				e.operationMu.Unlock()
				return nil, err
			}
		}
		var once sync.Once
		return func() { once.Do(func() { release(); e.operationMu.Unlock() }) }, nil
	})
	type response struct {
		result TaskResult
		err    error
	}
	execute := func() response {
		if deferred, ok := h.(DeferredLaunchHarness); ok && deferred.DefersWorkerLaunch() {
			result, err := h.Execute(turnCtx, t, pkg, path)
			return response{result, err}
		}
		release, err := BeginWorkerLaunch(turnCtx)
		if err != nil {
			return response{err: err}
		}
		defer release()
		result, err := h.Execute(WithWorkerLaunchAcknowledgement(turnCtx, release), t, pkg, path)
		return response{result, err}
	}
	responseValue := execute()
	callKey := fmt.Sprintf("%s/%s/%d", run.RunID, t.TaskID, t.Attempts)
	if releaser, ok := e.goalReader.(goalCallReleaser); ok && run.GoalID != "" && responseValue.err == nil && e.ownsProviderTransportAdmission(responseValue.result, run, t) {
		// An exact zero-call transport review reached no model: refund the
		// durable admitted call instead of settling it as consumed usage.
		if err := releaser.ReleaseGoalCall(context.WithoutCancel(ctx), run.SessionID, run.GoalID, run.GoalRevision, callKey, t.Attempts > 1); err != nil {
			responseValue.err = fmt.Errorf("%w: %v", ErrBudgetSettlement, err)
		}
		e.operationMu.Lock()
		delete(e.turnCancels, run.RunID)
		e.operationMu.Unlock()
		cancel()
		return responseValue.result, responseValue.err
	}
	if settler, ok := e.goalReader.(goalCallSettler); ok && run.GoalID != "" {
		elapsed := time.Duration(0)
		if !run.StartedAt.IsZero() {
			elapsed = time.Since(run.StartedAt)
		}
		if err := settler.SettleGoalCall(context.WithoutCancel(ctx), run.SessionID, run.GoalID, run.GoalRevision, fmt.Sprintf("%s/%s/%d", run.RunID, t.TaskID, t.Attempts), budget.CallSettlement{RunID: run.RunID, Duration: elapsed}); err != nil {
			responseValue.err = fmt.Errorf("%w: %v", ErrBudgetSettlement, err)
		}
	}
	e.operationMu.Lock()
	delete(e.turnCancels, run.RunID)
	e.operationMu.Unlock()
	cancel()
	return responseValue.result, responseValue.err
}

// retainCancelledTaskResult keeps evidence and admitted-call consumption while
// the lifecycle remains CANCELLED. A late successful worker cannot complete it.
func (e *Engine) retainCancelledTaskResult(run *ExecutionRun, id string, result TaskResult) error {
	task := run.Tasks[id]
	task.State = TaskCancelled
	ended := time.Now().UTC()
	task.CompletedAt = &ended
	var captureErr error
	for _, evidence := range result.EvidenceList {
		evidence.RunID = run.RunID
		recorded, err := e.oracle.RecordEvidence(evidence)
		if err != nil {
			captureErr = errors.Join(captureErr, err)
			continue
		}
		task.CollectedEvidence = append(task.CollectedEvidence, recorded.EvidenceID)
		run.Evidence = append(run.Evidence, EvidenceRef{EvidenceID: recorded.EvidenceID, TaskID: id, Type: "tool_output", Digest: recorded.OutputDigest, Status: recorded.Status, CapturedAt: recorded.CreatedAt})
	}
	for _, claim := range result.Claims {
		claim.RunID = run.RunID
		if _, err := e.oracle.RecordClaim(claim); err != nil {
			captureErr = errors.Join(captureErr, err)
		}
	}
	run.Tasks[id] = task
	run.BudgetConsumed.ModelCalls++
	if task.Attempts > 1 {
		run.BudgetConsumed.Retries++
	}
	run.BudgetConsumed.Handoffs = len(run.Handoffs)
	if result.InputTokens >= 0 && result.OutputTokens >= 0 {
		run.BudgetConsumed.InputTokens += result.InputTokens
		run.BudgetConsumed.OutputTokens += result.OutputTokens
	}
	return captureErr
}
