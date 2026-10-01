package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// TaskCommand contains task data, never a caller-supplied actor or role.
// Import accepts one exact task per envelope; batches use distinct keys.
type TaskCommand struct {
	Operation string
	Title     string
	AgentID   string
	Task      *model.Task
}

func (r *Runtime) CommandTask(ctx context.Context, e CommandEnvelope, input TaskCommand) (model.Task, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || e.ProjectID != r.ProjectIdentity() {
		return model.Task{}, authz.ErrDenied
	}
	action := "task.control"
	switch input.Operation {
	case "create", "import":
		action = "task.create"
	case "assign":
		action = "task.assign"
	case "pause", "resume", "cancel", "retry":
	default:
		return model.Task{}, model.ErrInvalid
	}
	if e.SessionID == "" || e.TargetID == "" || e.ExpectedVersion < 0 || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 {
		return model.Task{}, model.ErrInvalid
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: action}
	decision, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return model.Task{}, err
	}
	data, err := json.Marshal(struct {
		Envelope CommandEnvelope
		Input    TaskCommand
	}{e, input})
	if err != nil {
		return model.Task{}, err
	}
	digest := sha256.Sum256(data)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	record := store.CommandRecord{ProjectID: e.ProjectID, Actor: p.ID(), Key: hex.EncodeToString(key[:]), Operation: "task." + input.Operation, SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: hex.EncodeToString(digest[:]), CapabilityGrantID: decision.CapabilityGrantID}
	r.taskMu.Lock()
	defer r.taskMu.Unlock()
	if version, found, err := r.store.CommandResult(ctx, record); err != nil {
		return model.Task{}, err
	} else if found {
		if input.Operation == "pause" || input.Operation == "cancel" {
			observed, err := r.store.GetTask(ctx, e.TargetID)
			if err != nil {
				return model.Task{}, err
			}
			if observed.ControlState == "pause-requested" || observed.ControlState == "cancelled" {
				if cancel := r.taskRuns[e.TargetID]; cancel != nil {
					cancel()
				}
			}
		}
		return r.store.TaskCommandSnapshot(ctx, e.TargetID, version)
	}
	if input.Operation == "resume" {
		if err := r.checkTaskRuntimeApprovals(ctx, e.TargetID); err != nil {
			return model.Task{}, err
		}
	}
	result, err := r.store.ApplyTaskCommand(store.WithCommand(ctx, record), store.TaskMutation{Operation: input.Operation, Title: input.Title, AgentID: input.AgentID, Task: input.Task, Running: r.taskRuns[e.TargetID] != nil})
	if err != nil {
		if version, found, replayErr := r.store.CommandResult(ctx, record); replayErr != nil {
			return model.Task{}, replayErr
		} else if found {
			return r.store.TaskCommandSnapshot(ctx, e.TargetID, version)
		}
		return model.Task{}, err
	}
	if input.Operation == "pause" || input.Operation == "cancel" {
		if cancel := r.taskRuns[e.TargetID]; cancel != nil {
			cancel()
		}
	}
	return result, nil
}

// superviseTask owns one live provider turn. The durable gate is polled as well
// as signalled locally, so a different in-process control Runtime can request
// settlement without putting operator commands on the worker socket.
func (r *Runtime) superviseTask(ctx context.Context, id string) (context.Context, func() error, error) {
	if binding, ok := ctx.Value(taskSupervisorKey{}).(taskSupervisorBinding); ok && binding.runtime == r && binding.id == id {
		return ctx, func() error { return nil }, nil
	}
	r.taskMu.Lock()
	defer r.taskMu.Unlock()
	task, err := r.store.GetTask(ctx, id)
	if err != nil {
		return ctx, nil, err
	}
	if task.ControlState != "" || task.Status == model.TaskCancelled || task.Status == model.TaskMerged || task.Status == model.TaskSuperseded {
		return ctx, nil, model.ErrConflict
	}
	if r.taskRuns == nil {
		r.taskRuns = make(map[string]context.CancelFunc)
	}
	if r.taskRuns[id] != nil {
		return ctx, nil, model.ErrConflict
	}
	token, err := model.NewID("SUPERVISION-")
	if err != nil {
		return ctx, nil, err
	}
	if err := r.store.BeginTaskSupervision(ctx, id, token); err != nil {
		return ctx, nil, err
	}
	child, cancel := context.WithCancel(context.WithValue(ctx, taskSupervisorKey{}, taskSupervisorBinding{r, id}))
	r.taskRuns[id] = cancel
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-child.Done():
				return
			case <-ticker.C:
				task, err := r.store.GetTask(child, id)
				if err == nil && (task.ControlState == "pause-requested" || task.ControlState == "cancelled" || task.Status == model.TaskCancelled) {
					cancel()
					return
				}
			}
		}
	}()
	return child, func() error {
		cancel()
		<-stopped
		r.taskMu.Lock()
		defer r.taskMu.Unlock()
		delete(r.taskRuns, id)
		if err := r.store.EndTaskSupervision(context.Background(), id, token); err != nil {
			return err
		}
		return r.store.SettleTaskPause(context.Background(), id)
	}, nil
}

func (r *Runtime) claimForRun(ctx context.Context, task model.Task, request RunRequest) (ClaimResult, error) {
	if task.Status != model.TaskClaimed {
		return r.Claim(ctx, ClaimRequest{TaskID: task.ID, AgentID: request.AgentID, ExpectedRevision: request.ExpectedRevision})
	}
	if task.Revision != request.ExpectedRevision {
		return ClaimResult{}, model.ErrConflict
	}
	active, err := r.store.ActiveLease(ctx, task.ID)
	if err != nil {
		return ClaimResult{}, err
	}
	if active.AgentID != request.AgentID || !time.Now().UTC().Before(active.Lease.ExpiresAt) {
		return ClaimResult{}, model.ErrConflict
	}
	session, err := r.store.GetSession(ctx, active.Lease.SessionID)
	if err != nil {
		return ClaimResult{}, err
	}
	if session.Status != model.SessionActive || session.TaskID == nil || *session.TaskID != task.ID {
		return ClaimResult{}, model.ErrConflict
	}
	return ClaimResult{Lease: active.Lease, Session: session}, nil
}

type taskSupervisorKey struct{}
type taskSupervisorBinding struct {
	runtime *Runtime
	id      string
}
type taskSupervisedHarness struct {
	runtime *Runtime
	inner   execution.WorkerHarness
}

func (h *taskSupervisedHarness) Name() string { return h.inner.Name() }
func (h *taskSupervisedHarness) Execute(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (result execution.TaskResult, err error) {
	if h.runtime == nil || task.CanonicalTaskID == "" {
		return h.inner.Execute(ctx, task, pkg, worktree)
	}
	child, settle, err := h.runtime.superviseTask(ctx, task.CanonicalTaskID)
	if err != nil {
		return result, err
	}
	defer func() {
		if settleErr := settle(); settleErr != nil && err == nil {
			err = settleErr
		}
	}()
	return h.inner.Execute(child, task, pkg, worktree)
}

func (r *Runtime) checkTaskRuntimeApprovals(ctx context.Context, id string) error {
	if _, err := r.store.GetTask(ctx, id); err != nil {
		return err
	}
	binding, err := r.store.TaskExecutionBinding(ctx, id)
	if err != nil {
		return err
	}
	approvals, err := r.Execution().Engine().ApprovalManager().ListApprovals()
	if err != nil {
		return err
	}
	latest := map[string]execution.RuntimeApproval{}
	for _, a := range approvals {
		canonical := a.TaskID
		if a.PlanID != "" {
			canonical = canonicalPlanTaskID(a.PlanID, a.PlanVersion, a.TaskID)
		}
		if a.TaskID != id && canonical != id {
			continue
		}
		key := a.OperationType + "\x00" + a.TargetResource
		if old, ok := latest[key]; !ok || a.CreatedAt.After(old.CreatedAt) {
			latest[key] = a
		}
	}
	for _, a := range latest {
		if a.Status == execution.ApprovalConsumed {
			continue
		}
		if a.Status != execution.ApprovalApproved || a.HardViolation || (a.ExpiresAt != nil && !time.Now().UTC().Before(*a.ExpiresAt)) || (a.PlanID != "" && (a.PlanID != binding.PlanID || a.PlanVersion != binding.PlanVersion)) {
			return fmt.Errorf("%w: task runtime approval is unavailable or stale", model.ErrConflict)
		}
	}
	return nil
}
