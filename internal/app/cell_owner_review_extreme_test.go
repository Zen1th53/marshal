//go:build !governed

package app

import (
	"errors"
	"testing"
)

func TestReviewPrivateCellNonOwnerCannotReleaseApprovedCheck(t *testing.T) {
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
	e := CommandEnvelope{ProjectID: d.ProjectID, SessionID: session, TargetID: "cell:" + string(d.ID), IdempotencyKey: "owner-prepare"}
	d, err = r.CommandPrepareApprovedCell(ctx, e, task, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(t.Context(), r.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if other.IsLifecycleOwner() {
		t.Fatal("fixture second window unexpectedly owns lifecycle")
	}
	otherLocal, err := other.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	otherCtx := otherLocal.Context(t.Context())
	if _, err := other.PreviewApprovedCell(otherCtx, e.ProjectID, session, task, 0); err != nil {
		t.Fatalf("non-owner preview refused: %v", err)
	}
	if _, err := other.ReadApprovedCell(otherCtx, e.ProjectID, session, d.ID); err != nil {
		t.Fatalf("non-owner read refused: %v", err)
	}
	prepare := e
	prepare.IdempotencyKey = "non-owner-prepare"
	if _, err := other.CommandPrepareApprovedCell(otherCtx, prepare, task, 0); !errors.Is(err, ErrRuntimeOwned) {
		t.Fatalf("non-owner prepare accepted: %v", err)
	}
	e.ExpectedVersion = d.Revision
	e.IdempotencyKey = "non-owner-execute"
	result, err := other.CommandExecuteApprovedCell(otherLocal.Context(t.Context()), e)
	if !errors.Is(err, ErrRuntimeOwned) {
		t.Fatalf("non-owner window released cell despite runtime lifecycle lock: state=%s settled=%v ref=%+v error=%v", result.State, result.Settled, result.Process, err)
	}
	e.IdempotencyKey = "non-owner-destroy"
	if _, err := other.CommandDestroyApprovedCell(otherCtx, e); !errors.Is(err, ErrRuntimeOwned) {
		t.Fatalf("non-owner destroy accepted: %v", err)
	}
	retained, err := r.ReadApprovedCell(ctx, e.ProjectID, session, d.ID)
	if err != nil || retained.State != d.State || retained.Revision != d.Revision || retained.Process.PID != 0 {
		t.Fatalf("non-owner mutation changed descriptor: %+v %v", retained, err)
	}
}
