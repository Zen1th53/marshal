package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestLocalControlBoundary(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := context.Background()
	p, err := r.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	g := planGoal()
	g.ProjectID = p.ID
	if err := r.Store().SaveGoalContract(ctx, g, 1); err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: p.ID, SessionID: g.SessionID, TargetID: g.ID, ExpectedVersion: 2, IdempotencyKey: "proof-1"}
	if _, err := r.CommandReviseGoal(ctx, e, "fixed wording", "owner revision"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous: %v", err)
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(ctx)
	invalid := e
	invalid.IdempotencyKey = ""
	if _, err := r.CommandReviseGoal(ctx, invalid, "fixed wording", "owner revision"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("missing envelope key: %v", err)
	}
	wrongTarget := e
	wrongTarget.TargetID = "GOAL-other"
	if _, err := r.CommandReviseGoal(ctx, wrongTarget, "fixed wording", "owner revision"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("wrong target: %v", err)
	}
	wrong := e
	wrong.ProjectID = "other"
	if _, err := r.CommandReviseGoal(ctx, wrong, "fixed wording", "owner revision"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("wrong project: %v", err)
	}
	stale := e
	stale.ExpectedVersion = 1
	if _, err := r.CommandReviseGoal(ctx, stale, "fixed wording", "owner revision"); !errors.Is(err, model.ErrGoalConflict) {
		t.Fatalf("stale: %v", err)
	}
	next, err := r.CommandReviseGoal(ctx, e, "fixed wording", "owner revision")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := r.CommandReviseGoal(ctx, e, "fixed wording", "owner revision")
	if err != nil || replay.Revision != next.Revision || replay.Revision != 3 {
		t.Fatalf("replay %+v: %v", replay, err)
	}
	if _, err := r.CommandReviseGoal(ctx, e, "different intent", "owner revision"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("key reuse: %v", err)
	}
	read, err := r.Store().GetActiveGoalContract(ctx, g.SessionID)
	if err != nil || read.Revision != 3 || read.Confirmation != model.ConfirmationPending {
		t.Fatalf("readback %+v: %v", read, err)
	}
	later := e
	later.ExpectedVersion = 3
	later.IdempotencyKey = "proof-2"
	if result, err := r.CommandReviseGoal(ctx, later, "later wording", "later owner revision"); err != nil || result.Revision != 4 {
		t.Fatalf("later revision: %+v %v", result, err)
	}
	if result, err := r.CommandReviseGoal(ctx, e, "fixed wording", "owner revision"); err != nil || result.Revision != 3 || result.DesiredOutcome != "fixed wording" {
		t.Fatalf("replay substituted active revision: %+v %v", result, err)
	}
	// Contexts are memory-only and bound to one Runtime; reopening requires
	// trusted workspace composition and reuses the persisted scoped grant.
	root := r.ProjectRoot()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	if _, err := reopened.CommandReviseGoal(ctx, e, "fixed wording", "owner revision"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("foreign runtime context: %v", err)
	}
	local, err = reopened.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(context.Background())
	replay, err = reopened.CommandReviseGoal(ctx, e, "fixed wording", "owner revision")
	if err != nil || replay.Revision != 3 {
		t.Fatalf("restart replay: %+v %v", replay, err)
	}
	grants, err := reopened.Store().ListCapabilityGrants(ctx)
	if err != nil || len(grants) != 1 {
		t.Fatalf("grants: %+v %v", grants, err)
	}
	if err := reopened.Store().RevokeCapabilityGrant(ctx, grants[0].ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.CommandReviseGoal(ctx, e, "fixed wording", "owner revision"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("revoked grant replay: %v", err)
	}
	local, err = reopened.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.CommandReviseGoal(local.Context(context.Background()), e, "fixed wording", "owner revision"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("bootstrap resurrected grant: %v", err)
	}
}

func TestWorkerAndLegacyPathsCannotMintOperator(t *testing.T) {
	r := runtimeForPlan(t)
	project, err := r.Store().Project(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: project.ID, SessionID: "SESSION-plan", TargetID: "GOAL-plan", ExpectedVersion: 1, IdempotencyKey: "worker"}
	// Even an account identity is insufficient without the runtime's trusted
	// workspace handle. Worker protocol inputs cannot deserialize that handle.
	p, err := auth.LocalOwner(r.layout.RuntimeDir, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A server-resolved role still cannot substitute for a concrete grant.
	withoutGrant := (&LocalControl{runtime: r, principal: p}).Context(context.Background())
	if _, err := r.CommandReviseGoal(withoutGrant, e, "worker change", "operator"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("missing scoped capability: %v", err)
	}
	if _, err := r.CommandReviseGoal(p.Context(context.Background()), e, "worker change", "operator"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("account-only context: %v", err)
	}
	if _, err := r.ReviseGoal(context.Background(), e.SessionID, 1, "worker change", "operator"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("legacy bypass: %v", err)
	}
}
