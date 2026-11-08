//go:build !governed

package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/cell"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

func approvedCellRuntime(t *testing.T, command string) (*Runtime, string, string) {
	t.Helper()
	r := runtimeForPlan(t)
	if !r.IsLifecycleOwner() {
		t.Fatal("approved-cell fixture must own the runtime lifecycle")
	}
	goal := planGoal()
	goal.ProjectID = r.ProjectIdentity()
	goal.Revision = 2
	if err := r.Store().SaveGoalContract(t.Context(), goal, 1); err != nil {
		t.Fatal(err)
	}
	req := planCreateRequest()
	req.ProjectID = projectid.ID(r.ProjectIdentity())
	req.Checks = map[string][]string{"fix": {command}}
	if _, err := r.Plans().Create(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	p, err := r.Plans().Approve(t.Context(), req.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	id := canonicalPlanTaskID(p.ID, p.Version, "fix")
	workspace := filepath.Join(r.layout.Worktrees, id)
	marshalGit(t, r.ProjectRoot(), "worktree", "add", "--detach", workspace, "HEAD")
	task := model.Task{ID: id, Title: "approved cell", Status: model.TaskReview, Risk: model.R1, Revision: 1, Worktree: &workspace}
	binding := store.TaskBinding{SessionID: goal.SessionID, GoalID: goal.ID, GoalRevision: goal.Revision, PlanID: p.ID, PlanVersion: p.Version}
	if _, err := r.Store().ImportTasks(store.WithTaskBinding(t.Context(), binding), []model.Task{task}); err != nil {
		t.Fatal(err)
	}
	return r, goal.SessionID, id
}

func TestPrivateCellRuntimeCloseJoinsBeforeReleasingOwnership(t *testing.T) {
	r, session, task := approvedCellRuntime(t, "sleep 15")
	if !r.egressEnforcementAvailable() {
		t.Skip("real confined prerequisites unavailable")
	}
	local, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(t.Context())
	d, err := r.PreviewApprovedCell(ctx, r.ProjectIdentity(), session, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: d.ProjectID, SessionID: session, TargetID: "cell:" + string(d.ID), IdempotencyKey: "prepare-close"}
	d, err = r.CommandPrepareApprovedCell(ctx, e, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = d.Revision
	e.IdempotencyKey = "execute-close"
	done := make(chan error, 1)
	go func() { _, err := r.CommandExecuteApprovedCell(ctx, e); done <- err }()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		d, err = r.ReadApprovedCell(ctx, e.ProjectID, session, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Process.PID > 0 {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatal(waitCtx.Err())
		case err := <-done:
			t.Fatalf("execution ended before supervised: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	closed := make(chan error, 1)
	go func() { closed <- r.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-waitCtx.Done():
		t.Fatal("runtime close did not join its owned check")
	}
	select {
	case <-done:
	default:
		t.Fatal("runtime close returned before command worker joined")
	}
	alive, err := processgroup.RunningReference(d.Process)
	if err != nil || alive {
		t.Fatalf("closed runtime retained live supervisor: %v %v", alive, err)
	}
	reopened, err := Open(t.Context(), r.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if !reopened.IsLifecycleOwner() {
		t.Fatal("settled runtime failed to release ownership")
	}
	retained, err := reopened.Store().GetApprovedCell(t.Context(), e.ProjectID, session, d.ID, 0)
	if err != nil || !retained.Settled || retained.State != cell.StateDestroyed || retained.Process != d.Process {
		t.Fatalf("close failed durable exact settlement: %+v %v", retained, err)
	}
}

func TestPrivateCellDestroyRunningWaitsForPinnedSupervisorJoin(t *testing.T) {
	r, session, task := approvedCellRuntime(t, "sleep 15")
	if !r.egressEnforcementAvailable() {
		t.Skip("real confined prerequisites unavailable")
	}
	local, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(t.Context())
	d, err := r.PreviewApprovedCell(ctx, r.ProjectIdentity(), session, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: d.ProjectID, SessionID: session, TargetID: "cell:" + string(d.ID), IdempotencyKey: "prepare-cancel"}
	d, err = r.CommandPrepareApprovedCell(ctx, e, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = 1
	e.IdempotencyKey = "execute-cancel"
	done := make(chan error, 1)
	go func() { _, err := r.CommandExecuteApprovedCell(ctx, e); done <- err }()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		d, err = r.ReadApprovedCell(ctx, e.ProjectID, session, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Process.PID > 0 {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatal(waitCtx.Err())
		case err := <-done:
			t.Fatalf("execution ended before supervised: %v", err)
		case <-time.After(time.Millisecond * 10):
		}
	}
	e.ExpectedVersion = d.Revision
	e.IdempotencyKey = "destroy-running"
	destroyed, err := r.CommandDestroyApprovedCell(waitCtx, e)
	if err != nil || !destroyed.Settled || destroyed.State != cell.StateDestroyed {
		t.Fatalf("running destroy %+v %v", destroyed, err)
	}
	select {
	case <-done:
	case <-waitCtx.Done():
		t.Fatal("destroy returned before owned execution joined")
	}
	alive, err := processgroup.RunningReference(destroyed.Process)
	if err != nil || alive {
		t.Fatalf("destroy retained live supervisor: %v %v", alive, err)
	}
}

func TestPrivateCellUnsettledDescriptorWithoutOwnershipStaysUnknown(t *testing.T) {
	r, session, task := approvedCellRuntime(t, "true")
	local, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(t.Context())
	d, err := r.PreviewApprovedCell(ctx, r.ProjectIdentity(), session, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: d.ProjectID, SessionID: session, TargetID: "cell:" + string(d.ID), IdempotencyKey: "prepare-orphan"}
	d, err = r.CommandPrepareApprovedCell(ctx, e, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = 1
	e.IdempotencyKey = "orphan-intent"
	rec, _, err := r.checkpointCommand(ctx, e, "cell.exec", nil)
	if err != nil {
		t.Fatal(err)
	}
	d.Revision++
	d.State = cell.StateRunning
	if err := r.Store().CommitApprovedCell(ctx, rec, d, 1, false); err != nil {
		t.Fatal(err)
	}
	root := r.ProjectRoot()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	local, err = r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(t.Context())
	e.ExpectedVersion = d.Revision
	e.IdempotencyKey = "destroy-orphan"
	result, err := r.CommandDestroyApprovedCell(ctx, e)
	if !errors.Is(err, model.ErrUnavailable) || result.Settled || result.State != cell.StateRunning {
		t.Fatalf("unowned cleanup fabricated %+v %v", result, err)
	}
}

func TestPrivateCellCancelledAfterIntentSettlesWithoutLaunchingChild(t *testing.T) {
	r, session, task := approvedCellRuntime(t, "true")
	if !r.egressEnforcementAvailable() {
		t.Skip("real confined prerequisites unavailable")
	}
	local, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(t.Context())
	d, err := r.PreviewApprovedCell(ctx, r.ProjectIdentity(), session, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: d.ProjectID, SessionID: session, TargetID: "cell:" + string(d.ID), IdempotencyKey: "prepare-held-cancel"}
	d, err = r.CommandPrepareApprovedCell(ctx, e, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, approvedCellIntentObserverKey{}, func() {
		value, ok := approvedCellWorkers.Load(approvedCellKey{r, d.ID})
		if !ok {
			t.Fatal("no owned execution intent")
		}
		value.(*approvedCellActive).cancel()
	})
	e.ExpectedVersion = 1
	e.IdempotencyKey = "execute-held-cancel"
	result, err := r.CommandExecuteApprovedCell(ctx, e)
	if !errors.Is(err, context.Canceled) || !result.Settled || result.State != cell.StateDestroyed || result.Process.PID != 0 || result.ExitCode != nil {
		t.Fatalf("known no-child cancellation left unknown state %+v %v", result, err)
	}
	retained, err := r.ReadApprovedCell(ctx, e.ProjectID, session, d.ID)
	if err != nil || retained.Revision != result.Revision || !retained.Settled {
		t.Fatalf("cancel result not durable %+v %v", retained, err)
	}
}

func TestPrivateCellPrepareDestroyReauthorizeAndRestart(t *testing.T) {
	r, session, task := approvedCellRuntime(t, "true")
	if _, err := r.PreviewApprovedCell(t.Context(), r.ProjectIdentity(), session, task, 0); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous prepare: %v", err)
	}
	local, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(t.Context())
	d, err := r.PreviewApprovedCell(ctx, r.ProjectIdentity(), session, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: session, TargetID: "cell:" + string(d.ID), ExpectedVersion: 0, IdempotencyKey: "cell-prepare"}
	prepared, err := r.CommandPrepareApprovedCell(ctx, e, task, 0)
	if err != nil || prepared.State != cell.StateReady || prepared.Process.PID != 0 {
		t.Fatalf("prepare %+v %v", prepared, err)
	}
	root := r.ProjectRoot()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if _, err := r.CommandDestroyApprovedCell(ctx, CommandEnvelope{ProjectID: e.ProjectID, SessionID: session, TargetID: e.TargetID, ExpectedVersion: 1, IdempotencyKey: "cell-destroy"}); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("old runtime authority survived reopen: %v", err)
	}
	local, err = r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(t.Context())
	e.ExpectedVersion = 1
	e.IdempotencyKey = "cell-destroy"
	bad := e
	bad.SessionID = "foreign-session"
	if _, err := r.CommandDestroyApprovedCell(ctx, bad); err == nil {
		t.Fatal("foreign destroy accepted")
	}
	destroyed, err := r.CommandDestroyApprovedCell(ctx, e)
	if err != nil || !destroyed.Settled || destroyed.State != cell.StateDestroyed {
		t.Fatalf("destroy %+v %v", destroyed, err)
	}
	replay, err := r.CommandDestroyApprovedCell(ctx, e)
	if err != nil || replay.Revision != destroyed.Revision {
		t.Fatalf("replay %+v %v", replay, err)
	}
	grant, found, err := r.Store().FindCapabilityGrantByIdempotencyKey(ctx, "local-cell-destroy:"+e.ProjectID+":"+local.principal.ID())
	if err != nil || !found {
		t.Fatal(grant, found, err)
	}
	if err := r.Store().RevokeCapabilityGrant(ctx, capability.GrantID(grant.ID), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommandDestroyApprovedCell(ctx, e); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("revoked replay accepted: %v", err)
	}
}

func TestPrivateCellExecRealConfinedOneShotAndDurableSupervisor(t *testing.T) {
	r, session, task := approvedCellRuntime(t, "test ! -w README.md && printf cell-check")
	if !r.egressEnforcementAvailable() {
		t.Skip("real confined check prerequisites unavailable")
	}
	local, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(t.Context())
	d, err := r.PreviewApprovedCell(ctx, r.ProjectIdentity(), session, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: d.ProjectID, SessionID: session, TargetID: "cell:" + string(d.ID), IdempotencyKey: "prepare-real"}
	d, err = r.CommandPrepareApprovedCell(ctx, e, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = d.Revision
	e.IdempotencyKey = "execute-real"
	settled, err := r.CommandExecuteApprovedCell(ctx, e)
	if err != nil || !settled.Settled || settled.ExitCode == nil || *settled.ExitCode != 0 || settled.Process.PID == 0 || settled.Process.Start == "" {
		t.Fatalf("actual confined settlement %+v %v", settled, err)
	}
	replay, err := r.CommandExecuteApprovedCell(ctx, e)
	if err != nil || replay.Process != settled.Process || replay.Revision != settled.Revision {
		t.Fatalf("execution replay launched another child: %+v %v", replay, err)
	}
	e.IdempotencyKey = "second-execution"
	e.ExpectedVersion = settled.Revision
	if _, err := r.CommandExecuteApprovedCell(ctx, e); err == nil {
		t.Fatal("one-shot descriptor executed twice")
	}
}
