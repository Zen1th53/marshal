package app

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/store"
)

// Legacy imported tasks may have no binding. A persisted binding must always
// match the canonical goal head, including its project and termination state.
func (r *Runtime) validateTaskGoalDispatch(ctx context.Context, taskID string) (store.TaskBinding, error) {
	binding, err := r.store.TaskExecutionBinding(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.TaskBinding{}, nil
	}
	if err != nil {
		return binding, err
	}
	if binding.GoalID == "" {
		return binding, nil
	}
	return binding, r.ValidateGoalDispatch(ctx, r.ProjectIdentity(), binding.SessionID, binding.GoalID, binding.GoalRevision)
}

func (r *Runtime) beginTaskWorkerLaunch(ctx context.Context, taskID string) (func(), error) {
	binding, err := r.validateTaskGoalDispatch(ctx, taskID)
	if err != nil {
		return nil, err
	}
	// Process 05 owns the operation lock and the same goal barrier. Opening a
	// second SQLite transaction here would wait on our own single connection.
	if execution.HasWorkerLaunchAdmission(ctx) {
		return execution.BeginWorkerLaunch(ctx)
	}
	if binding.GoalID == "" {
		return func() {}, nil
	}
	return r.store.BeginGoalDispatch(ctx, binding.SessionID, binding.GoalID, binding.GoalRevision)
}

type providerTurnKey struct{}

// taskLaunchRunner sits inside policy/capability/sandbox preparation. A worker's
// first output or post-start heartbeat acknowledges actual process launch.
// On start failure or a backend without acknowledgements, return releases it.
type taskLaunchRunner struct {
	runtime *Runtime
	taskID  string
	base    adapter.ProcessRunner
}

func (r *taskLaunchRunner) Run(ctx context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	if active, _ := ctx.Value(providerTurnKey{}).(bool); !active {
		return r.base.Run(ctx, command)
	}
	configureWorkerStall(ctx, &command)
	release, err := r.runtime.beginTaskWorkerLaunch(ctx, r.taskID)
	if err != nil {
		return adapter.ProcessResult{}, err
	}
	var once sync.Once
	ack := func() { once.Do(release) }
	defer ack()
	observer := command.OutputObserver
	command.OutputObserver = func(stream string, data []byte) {
		ack()
		if observer != nil {
			observer(stream, data)
		}
	}
	heartbeat, interval := command.Heartbeat, command.HeartbeatInterval
	lastHeartbeat := time.Now()
	command.HeartbeatInterval = 50 * time.Millisecond
	command.Heartbeat = func() {
		ack()
		if heartbeat != nil && interval > 0 && time.Since(lastHeartbeat) >= interval {
			lastHeartbeat = time.Now()
			heartbeat()
		}
	}
	return r.base.Run(ctx, command)
}

type observedTaskProvider struct {
	adapter.Adapter
	runtime          *Runtime
	provider, taskID string
	runnerAdmission  bool
}

func (r *Runtime) observedTaskAdapter(inner adapter.Adapter, provider, taskID string, runnerAdmission bool) adapter.Adapter {
	// Discovery-only runtimes have no dispatch or observation authority.
	if r == nil || r.store == nil {
		return inner
	}
	return &observedTaskProvider{Adapter: inner, runtime: r, provider: provider, taskID: taskID, runnerAdmission: runnerAdmission}
}
func (p *observedTaskProvider) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	if !p.runnerAdmission {
		release, err := p.runtime.beginTaskWorkerLaunch(ctx, p.taskID)
		if err != nil {
			return adapter.Result{}, err
		}
		var once sync.Once
		ack := func() { once.Do(release) }
		defer ack()
		ctx = execution.WithWorkerLaunchAcknowledgement(ctx, ack)
	}
	started := time.Now()
	result, err := p.Adapter.Run(context.WithValue(ctx, providerTurnKey{}, true), request)
	// Release before recording observations (which use the same SQLite store).
	execution.AcknowledgeWorkerLaunch(ctx)
	if observationErr := p.runtime.observeProviderResult(p.provider, result, time.Since(started)); observationErr != nil {
		err = errors.Join(err, observationErr)
	}
	return result, err
}
