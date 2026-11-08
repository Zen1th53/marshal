//go:build !governed

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/cell"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/workerterminal"
)

// Active references exist only while this runtime owns a joined worker. A
// descriptor restored after restart is never treated as an owned process.
type approvedCellKey struct {
	runtime *Runtime
	id      cell.CellID
}
type approvedCellActive struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Package-private observer supports deterministic cancellation fault tests.
// It is absent from all commands/wire inputs and conveys no caller authority.
type approvedCellIntentObserverKey struct{}

var approvedCellWorkers sync.Map

func closeApprovedCellWorkers(r *Runtime) error {
	var owned []*approvedCellActive
	approvedCellWorkers.Range(func(k, v any) bool {
		if k.(approvedCellKey).runtime == r {
			active := v.(*approvedCellActive)
			active.cancel()
			owned = append(owned, active)
		}
		return true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, active := range owned {
		select {
		case <-active.done:
		case <-ctx.Done():
			return fmt.Errorf("%w: owned cell cleanup did not join", model.ErrUnavailable)
		}
	}
	return nil
}

func approvedCellGit(ctx context.Context, dir, query string) (string, error) {
	if query != "--show-toplevel" && query != "--git-common-dir" {
		return "", model.ErrInvalid
	}
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, err := hostgit.Command(readCtx, dir, "rev-parse", query)
	if err != nil {
		return "", err
	}
	out := &marshalProofWriter{limit: 4096, cancel: cancel}
	diagnostic := &marshalProofWriter{limit: 4096, cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = diagnostic
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cell worktree observation failed: %w", err)
	}
	return out.buffer.String(), nil
}

func (r *Runtime) cellPrincipal(ctx context.Context, project string) error {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != project || r.ProjectIdentity() != project {
		return authz.ErrDenied
	}
	return nil
}

// approvedCellBinding accepts a selector, never a new command, environment,
// secret reference or caller-selected workspace. Only direct approved tasks
// with an existing canonical worktree are currently supported.
func (r *Runtime) approvedCellBinding(ctx context.Context, project, session, taskID string, index int) (cell.ApprovedCheckDescriptor, string, error) {
	var d cell.ApprovedCheckDescriptor
	if index < 0 || index >= 128 {
		return d, "", model.ErrInvalid
	}
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return d, "", err
	}
	if task.Worktree == nil || task.Revision < 1 || task.ControlState != "" || task.Status == model.TaskMerged || task.Status == model.TaskCancelled || task.Status == model.TaskSuperseded || task.Status == model.TaskBlocked {
		return d, "", model.ErrConflict
	}
	binding, err := r.store.TaskExecutionBinding(ctx, taskID)
	if err != nil {
		return d, "", err
	}
	if binding.SessionID != session {
		return d, "", authz.ErrDenied
	}
	goal, err := r.store.GetActiveGoalContract(ctx, session)
	if err != nil {
		return d, "", err
	}
	if goal.ProjectID != project || goal.ID != binding.GoalID || goal.Revision != binding.GoalRevision || !goal.Confirmation.Settled() {
		return d, "", authz.ErrDenied
	}
	p, err := r.store.GetActivePlan(ctx, projectid.ID(project))
	if err != nil {
		return d, "", err
	}
	if p.ID != binding.PlanID || p.Version != binding.PlanVersion || p.State != plan.StateApproved || !p.Goal.Matches(goal) || p.ApprovedScope == nil || p.ApprovalScopeDigest != p.ApprovedScope.Digest() {
		return d, "", authz.ErrDenied
	}
	planTaskID := ""
	for _, pt := range p.Tasks {
		if canonicalPlanTaskID(p.ID, p.Version, pt.ID) == task.ID {
			planTaskID = pt.ID
			break
		}
	}
	if planTaskID == "" {
		return d, "", authz.ErrDenied
	}
	checks, approved := p.Checks[planTaskID], p.ApprovedScope.Checks[planTaskID]
	if index >= len(checks) || index >= len(approved) || checks[index] != approved[index] || strings.TrimSpace(checks[index]) == "" || len(checks[index]) > 65536 || strings.ContainsRune(checks[index], '\x00') {
		return d, "", authz.ErrDenied
	}
	approvedTask := false
	for _, pt := range p.ApprovedScope.Tasks {
		if pt.ID == planTaskID {
			approvedTask = true
			break
		}
	}
	if !approvedTask {
		return d, "", authz.ErrDenied
	}
	workspace, err := filepath.EvalSymlinks(*task.Worktree)
	if err != nil {
		return d, "", err
	}
	parent, err := filepath.EvalSymlinks(r.layout.Worktrees)
	if err != nil {
		return d, "", err
	}
	rel, err := filepath.Rel(parent, workspace)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !filepath.IsAbs(workspace) || workspace != filepath.Clean(*task.Worktree) {
		return d, "", cell.ErrScopeEscape
	}
	top, err := approvedCellGit(ctx, workspace, "--show-toplevel")
	if err != nil || strings.TrimSpace(top) != workspace {
		return d, "", cell.ErrScopeEscape
	}
	common, err := approvedCellGit(ctx, workspace, "--git-common-dir")
	if err != nil {
		return d, "", err
	}
	expectedCommon, err := approvedCellGit(ctx, r.ProjectRoot(), "--git-common-dir")
	if err != nil {
		return d, "", err
	}
	resolve := func(dir, value string) (string, error) {
		value = strings.TrimSpace(value)
		if !filepath.IsAbs(value) {
			value = filepath.Join(dir, value)
		}
		return filepath.EvalSymlinks(value)
	}
	actualGit, err := resolve(workspace, common)
	if err != nil {
		return d, "", err
	}
	projectGit, err := resolve(r.ProjectRoot(), expectedCommon)
	if err != nil || actualGit != projectGit {
		return d, "", cell.ErrScopeEscape
	}
	if err := r.checkTaskRuntimeApprovals(ctx, task.ID); err != nil {
		return d, "", err
	}
	checkSum := sha256.Sum256([]byte(checks[index]))
	scopeBytes, _ := json.Marshal(struct {
		Project, Session, Task, Goal, Plan, PlanTask, Scope, Workspace, Check string
		TaskRevision, GoalRevision, PlanVersion                               int64
		Index                                                                 int
	}{project, session, task.ID, goal.ID, p.ID, planTaskID, p.ApprovalScopeDigest, workspace, hex.EncodeToString(checkSum[:]), task.Revision, goal.Revision, p.Version, index})
	scopeSum := sha256.Sum256(scopeBytes)
	spec := cell.Spec{TaskID: cell.TaskID(task.ID), Workspace: workspace, Backend: cell.BackendBubblewrap, NetworkProfile: "deny-by-default", Capabilities: []string{"approved-check-binding:" + hex.EncodeToString(scopeSum[:])}}
	id, err := cell.CellIdentity(spec)
	if err != nil {
		return d, "", err
	}
	d = cell.ApprovedCheckDescriptor{ID: id, ProjectID: project, SessionID: session, TaskID: task.ID, TaskRevision: task.Revision, GoalID: goal.ID, GoalRevision: goal.Revision, PlanID: p.ID, PlanVersion: p.Version, PlanTaskID: planTaskID, CheckIndex: index, CheckDigest: hex.EncodeToString(checkSum[:]), ApprovalScopeDigest: p.ApprovalScopeDigest, Spec: spec, Revision: 1, State: cell.StateReady}
	return d, checks[index], d.Validate()
}

func (r *Runtime) PreviewApprovedCell(ctx context.Context, project, session, task string, index int) (cell.ApprovedCheckDescriptor, error) {
	if err := r.cellPrincipal(ctx, project); err != nil {
		return cell.ApprovedCheckDescriptor{}, err
	}
	_, _, err := r.checkpointCommand(ctx, CommandEnvelope{ProjectID: project, SessionID: session, TargetID: "cell-selector:" + task, IdempotencyKey: "preview-cell-selector"}, "cell.prepare", index)
	if err != nil {
		return cell.ApprovedCheckDescriptor{}, err
	}
	d, _, err := r.approvedCellBinding(ctx, project, session, task, index)
	return d, err
}

func (r *Runtime) cellCommand(ctx context.Context, e CommandEnvelope, action string, input any) (store.CommandRecord, cell.ApprovedCheckDescriptor, bool, error) {
	var d cell.ApprovedCheckDescriptor
	rec, _, err := r.checkpointCommand(ctx, e, action, input)
	if err != nil {
		return rec, d, false, err
	}
	id, ok := strings.CutPrefix(e.TargetID, "cell:")
	if !ok || id == "" || e.ExpectedVersion < 0 {
		return rec, d, false, model.ErrInvalid
	}
	version, found, err := r.store.ApprovedCellCommandResult(ctx, rec)
	if err != nil {
		return rec, d, false, err
	}
	if found {
		d, err = r.store.GetApprovedCell(ctx, e.ProjectID, e.SessionID, cell.CellID(id), version)
		if err == nil {
			_, _, err = r.recheckApprovedCell(ctx, d)
		}
		return rec, d, true, err
	}
	if action != "cell.prepare" {
		d, err = r.store.GetApprovedCell(ctx, e.ProjectID, e.SessionID, cell.CellID(id), 0)
		if err != nil {
			return rec, d, false, err
		}
		if d.Revision != e.ExpectedVersion {
			return rec, d, false, model.ErrConflict
		}
		_, _, err = r.recheckApprovedCell(ctx, d)
		if err != nil {
			return rec, d, false, err
		}
	}
	return rec, d, false, nil
}

func (r *Runtime) recheckApprovedCell(ctx context.Context, d cell.ApprovedCheckDescriptor) (cell.ApprovedCheckDescriptor, string, error) {
	fresh, command, err := r.approvedCellBinding(ctx, d.ProjectID, d.SessionID, d.TaskID, d.CheckIndex)
	if err != nil {
		return fresh, "", err
	}
	expected := d
	expected.Revision = 1
	expected.State = cell.StateReady
	expected.Process = processgroup.Reference{}
	expected.Settled = false
	expected.ExitCode = nil
	if !reflect.DeepEqual(fresh, expected) {
		return fresh, "", model.ErrConflict
	}
	return fresh, command, nil
}

func (r *Runtime) ReadApprovedCell(ctx context.Context, project, session string, id cell.CellID) (cell.ApprovedCheckDescriptor, error) {
	if err := r.cellPrincipal(ctx, project); err != nil {
		return cell.ApprovedCheckDescriptor{}, err
	}
	if _, _, err := r.checkpointCommand(ctx, CommandEnvelope{ProjectID: project, SessionID: session, TargetID: "cell:" + string(id), IdempotencyKey: "read-cell-descriptor"}, "cell.prepare", nil); err != nil {
		return cell.ApprovedCheckDescriptor{}, err
	}
	d, err := r.store.GetApprovedCell(ctx, project, session, id, 0)
	if err != nil {
		return d, err
	}
	_, _, err = r.recheckApprovedCell(ctx, d)
	return d, err
}

func (r *Runtime) CommandPrepareApprovedCell(ctx context.Context, e CommandEnvelope, task string, index int) (cell.ApprovedCheckDescriptor, error) {
	if err := r.requireLifecycleOwner(); err != nil {
		return cell.ApprovedCheckDescriptor{}, err
	}
	rec, replay, found, err := r.cellCommand(ctx, e, "cell.prepare", struct {
		Task  string
		Index int
	}{task, index})
	if err != nil {
		return replay, err
	}
	if found {
		return replay, nil
	}
	if e.ExpectedVersion != 0 {
		return replay, model.ErrConflict
	}
	d, command, err := r.approvedCellBinding(ctx, e.ProjectID, e.SessionID, task, index)
	if err != nil {
		return d, err
	}
	if e.TargetID != "cell:"+string(d.ID) {
		return d, model.ErrConflict
	}
	backend, err := cell.NewApprovedCheckBackend(d.Spec, command, func(context.Context, cell.Handle, []string) (cell.ExecResult, error) {
		return cell.ExecResult{}, model.ErrUnavailable
	}, func(context.Context, cell.Handle) error { return nil })
	if err != nil {
		return d, err
	}
	if _, err := backend.Prepare(ctx, d.Spec); err != nil {
		return d, err
	}
	if err := r.store.CommitApprovedCell(ctx, rec, d, 0, false); err != nil {
		return d, err
	}
	return d, nil
}

// Execute is one-shot. Only the existing governed verification sandbox runs
// the approved command. A held supervisor cannot release its child until its
// exact process reference and a fresh authorization/scope CAS are durable.
func (r *Runtime) CommandExecuteApprovedCell(ctx context.Context, e CommandEnvelope) (cell.ApprovedCheckDescriptor, error) {
	if err := r.requireLifecycleOwner(); err != nil {
		return cell.ApprovedCheckDescriptor{}, err
	}
	rec, d, found, err := r.cellCommand(ctx, e, "cell.exec", nil)
	if err != nil {
		return d, err
	}
	if found {
		return d, nil
	}
	if d.State != cell.StateReady || d.Process.PID != 0 || (!r.egressEnforcementAvailable() && !sandbox.UnconfinedFallbackAllowed()) {
		return d, model.ErrUnavailable
	}
	_, command, err := r.recheckApprovedCell(ctx, d)
	if err != nil {
		return d, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	active := &approvedCellActive{cancel: cancel, done: make(chan struct{})}
	key := approvedCellKey{r, d.ID}
	r.ownerMu.Lock()
	if r.closed {
		r.ownerMu.Unlock()
		return d, model.ErrUnavailable
	}
	if r.ownerLock == nil {
		r.ownerMu.Unlock()
		return d, ErrRuntimeOwned
	}
	_, loaded := approvedCellWorkers.LoadOrStore(key, active)
	r.ownerMu.Unlock()
	if loaded {
		return d, model.ErrConflict
	}
	defer func() { close(active.done); approvedCellWorkers.Delete(key) }()
	d.Revision++
	d.State = cell.StateRunning
	if err := r.store.CommitApprovedCell(ctx, rec, d, e.ExpectedVersion, false); err != nil {
		return d, err
	}
	if observe, ok := ctx.Value(approvedCellIntentObserverKey{}).(func()); ok {
		observe()
	}
	joined := false
	entered := false
	backend, err := cell.NewApprovedCheckBackend(d.Spec, command, func(callCtx context.Context, _ cell.Handle, argv []string) (cell.ExecResult, error) {
		entered = true
		held := workerterminal.WithHeldLifecycle(callCtx, func(ref processgroup.Reference) error {
			if ref.PID <= 0 || ref.Start == "" {
				return model.ErrUnavailable
			}
			if _, _, err := r.checkpointCommand(callCtx, e, "cell.exec", nil); err != nil {
				return err
			}
			if _, _, err := r.recheckApprovedCell(callCtx, d); err != nil {
				return err
			}
			next := d
			next.Revision++
			next.Process = ref
			if err := r.store.CommitApprovedCell(callCtx, rec, next, d.Revision, true); err != nil {
				return err
			}
			d = next
			return nil
		})
		result := r.runGovernedCheck(held, "cell:"+string(d.ID), d.TaskID, rec.Actor, d.Spec.Workspace, argv[2])
		joined = true
		if result.ExitCode < 0 {
			return cell.ExecResult{}, model.ErrUnavailable
		}
		return cell.ExecResult{ExitCode: result.ExitCode}, nil
	}, func(context.Context, cell.Handle) error {
		if entered && !joined {
			return model.ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return d, err
	}
	handle, err := backend.Prepare(runCtx, d.Spec)
	var result cell.ExecResult
	runErr := err
	if err == nil {
		result, runErr = backend.Exec(runCtx, handle, cell.ExecRequest{Command: []string{"/bin/sh", "-c", command}})
	} else {
		// This backend's Prepare allocates only a descriptor and never launches
		// a process. An owned cancellation before Exec therefore proves no
		// child was released, and can revoke the intent durably.
		handle = cell.Handle{ID: d.ID, TaskID: d.Spec.TaskID, Backend: d.Spec.Backend, Workspace: d.Spec.Workspace}
	}
	// Cancellation/refusal cleanup is joined by the runner. Retain no output
	// prose from project code; only bounded typed exit/identity metadata.
	finalCtx, finalCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer finalCancel()
	if err := backend.Destroy(finalCtx, handle); err != nil {
		return d, err
	}
	if _, _, err := r.checkpointCommand(finalCtx, e, "cell.exec", nil); err != nil {
		return d, err
	}
	if _, _, err := r.recheckApprovedCell(finalCtx, d); err != nil {
		return d, err
	}
	next := d
	next.Revision++
	next.State = cell.StateDestroyed
	next.Settled = true
	if runErr == nil {
		code := result.ExitCode
		next.ExitCode = &code
	}
	if err := r.store.CommitApprovedCell(finalCtx, rec, next, d.Revision, true); err != nil {
		return d, err
	}
	return next, runErr
}

// Destroy revokes an unused descriptor or cancels an owned execution and waits
// for its runner to join. An unsettled descriptor without current ownership is
// UNKNOWN after restart; absence/reuse of a PID is never proof of cleanup.
func (r *Runtime) CommandDestroyApprovedCell(ctx context.Context, e CommandEnvelope) (cell.ApprovedCheckDescriptor, error) {
	if err := r.requireLifecycleOwner(); err != nil {
		return cell.ApprovedCheckDescriptor{}, err
	}
	rec, d, found, err := r.cellCommand(ctx, e, "cell.destroy", nil)
	if err != nil {
		return d, err
	}
	if found {
		return d, nil
	}
	if d.State == cell.StateRunning {
		value, owned := approvedCellWorkers.Load(approvedCellKey{r, d.ID})
		if !owned {
			return d, fmt.Errorf("%w: process settlement unknown after restart", model.ErrUnavailable)
		}
		active := value.(*approvedCellActive)
		active.cancel()
		select {
		case <-active.done:
		case <-ctx.Done():
			return d, ctx.Err()
		}
		d, err = r.store.GetApprovedCell(ctx, e.ProjectID, e.SessionID, d.ID, 0)
		if err != nil {
			return d, err
		}
		if _, _, err := r.checkpointCommand(ctx, e, "cell.destroy", nil); err != nil {
			return d, err
		}
		if _, _, err := r.recheckApprovedCell(ctx, d); err != nil {
			return d, err
		}
	}
	if d.State == cell.StateDestroyed && d.Settled {
		return d, r.store.RecordApprovedCellSettlement(ctx, rec, d)
	}
	if d.State != cell.StateReady || d.Process.PID != 0 {
		return d, model.ErrUnavailable
	}
	backend, err := cell.NewApprovedCheckBackend(d.Spec, "unused-descriptor", func(context.Context, cell.Handle, []string) (cell.ExecResult, error) {
		return cell.ExecResult{}, model.ErrUnavailable
	}, func(context.Context, cell.Handle) error { return nil })
	if err != nil {
		return d, err
	}
	handle, err := backend.Prepare(ctx, d.Spec)
	if err != nil {
		return d, err
	}
	if err := backend.Destroy(ctx, handle); err != nil {
		return d, err
	}
	d.Revision++
	d.State = cell.StateDestroyed
	d.Settled = true
	if err := r.store.CommitApprovedCell(ctx, rec, d, e.ExpectedVersion, false); err != nil {
		return d, err
	}
	return d, nil
}
