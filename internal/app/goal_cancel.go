package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

// GoalCancellation separates the durable cancellation from live work settling.
// StopError is a recoverable cleanup failure; it never undoes the cancelled head.
type GoalCancellation struct {
	Goal        model.GoalContract
	Termination model.GoalTermination
	Runs        []execution.RunControlResult
	Settling    bool
	StopError   string
}

func (r *Runtime) CommandCancelGoal(ctx context.Context, e CommandEnvelope, reason string) (GoalCancellation, error) {
	if strings.TrimSpace(reason) == "" || len(reason) > 4096 {
		return GoalCancellation{}, model.ErrInvalid
	}
	g, err := r.commandGoal(ctx, e, "goal.cancel", reason, func(ctx context.Context, _ string) (model.GoalContract, error) {
		return r.store.CancelGoalContract(ctx, e.SessionID, e.TargetID, e.ExpectedVersion, reason)
	})
	if err != nil {
		return GoalCancellation{}, err
	}
	term, err := r.store.GetGoalTermination(ctx, g.SessionID, g.ID, g.Revision)
	if err != nil {
		return GoalCancellation{}, err
	}
	result := GoalCancellation{Goal: g, Termination: *term}
	// Runtime.Run also owns provider turns independently of Process 05 runs.
	// Signal those exact bound tasks after the durable cancellation commits.
	r.taskMu.Lock()
	for taskID, cancel := range r.taskRuns {
		binding, bindingErr := r.store.TaskExecutionBinding(ctx, taskID)
		if bindingErr == nil && cancel != nil && binding.GoalID == g.ID && binding.SessionID == g.SessionID {
			cancel()
			result.Settling = true
		}
	}
	r.taskMu.Unlock()

	// Reconcile on replay as well, so a crash after SQLite commit but before
	// signalling a provider is recoverable without another goal revision.
	if r.Execution() == nil || r.Execution().Engine() == nil {
		result.Settling = true
		result.StopError = "execution liveness is unavailable"
		return result, nil
	}
	runs, err := r.Execution().Engine().ListRuns(ctx)
	if err != nil {
		result.StopError = err.Error()
		result.Settling = true
		return result, nil
	}
	for _, run := range runs {
		if run.GoalID != g.ID || run.SessionID != g.SessionID || string(run.ProjectID) != g.ProjectID {
			continue
		}
		engine := r.Execution().Engine()
		if !run.State.IsTerminal() {
			stopped, stopErr := engine.ControlRun(ctx, run.RunID, "cancel", run.Version)
			if stopErr != nil {
				result.StopError = fmt.Sprintf("run %s cancellation requires reconciliation: %v", run.RunID, stopErr)
				result.Settling = true
				continue
			}
			run = stopped.Run
		}
		r.cancelRunTurns(run)
		executing := engine.IsExecuting(run.RunID)
		for _, task := range run.Tasks {
			if task.State == execution.TaskRunning {
				executing = true
			}
		}
		result.Settling = result.Settling || executing
		result.Runs = append(result.Runs, execution.RunControlResult{Run: run, Executing: executing})
	}
	return result, nil
}

// ValidateGoalDispatch is shared with task/cell/recovery services. It rejects
// cancelled, unconfirmed, foreign and superseded goal bindings. This read is
// a guard, not dispatch admission: use BeginGoalDispatch through provider launch.
func (r *Runtime) ValidateGoalDispatch(ctx context.Context, project, session, id string, revision int64) error {
	if r == nil || r.store == nil {
		return model.ErrUnavailable
	}
	g, err := r.store.GetActiveGoalContract(ctx, session)
	if err != nil {
		return err
	}
	if g.SessionID != session || g.ProjectID != project || project != r.ProjectIdentity() || g.ID != id || g.Revision != revision || !g.Confirmation.Settled() {
		return fmt.Errorf("%w: goal is foreign, stale or %s", model.ErrGoalConflict, g.Confirmation)
	}
	term, err := r.store.GetGoalTermination(ctx, session, id, revision)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return err
	}
	if term != nil && term.State == model.StateCancelled {
		return fmt.Errorf("%w: goal is cancelled", model.ErrGoalConflict)
	}
	return nil
}
