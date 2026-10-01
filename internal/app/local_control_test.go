package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestLocalControlBoundary(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := context.Background()
	projectID := r.ProjectIdentity()
	g := planGoal()
	g.ProjectID = projectID
	if err := r.Store().SaveGoalContract(ctx, g, 1); err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: projectID, SessionID: g.SessionID, TargetID: g.ID, ExpectedVersion: 2, IdempotencyKey: "proof-1"}
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
	if err != nil || len(grants) != 17 {
		t.Fatalf("grants: %+v %v", grants, err)
	}
	var reviseGrant capability.GrantID
	for _, grant := range grants {
		if len(grant.Scope.Actions) == 1 && grant.Scope.Actions[0] == "goal.revise" {
			reviseGrant = grant.ID
		}
	}
	if reviseGrant == "" {
		t.Fatal("goal.revise grant missing")
	}
	if err := reopened.Store().RevokeCapabilityGrant(ctx, reviseGrant, time.Now().UTC()); err != nil {
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
	projectID := r.ProjectIdentity()
	e := CommandEnvelope{ProjectID: projectID, SessionID: "SESSION-plan", TargetID: "GOAL-plan", ExpectedVersion: 1, IdempotencyKey: "worker"}
	// Even an account identity is insufficient without the runtime's trusted
	// workspace handle. Worker protocol inputs cannot deserialize that handle.
	p, err := auth.LocalOwner(r.layout.RuntimeDir, projectID)
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

func TestLocalControlCanonicalBoundGoal(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	layout, err := Bootstrap(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	binding, found := projectid.LoadBinding(layout.RuntimeDir)
	if !found {
		t.Fatal("bootstrap did not establish a binding")
	}
	r, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	legacy, err := r.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.ProjectIdentity() != string(binding.ID) || legacy.ID == string(binding.ID) {
		t.Fatal("fixture does not distinguish canonical and legacy identity")
	}
	intake, err := goalintake.Form(goalintake.FormationRequest{Request: "Fix the documented typo.", ProjectID: binding.ID, SessionID: "SESSION-bound-local", Version: constitution.Current, Context: goalintake.RequestContext{Recoverable: true, ScopeKnown: true}})
	if err != nil {
		t.Fatal(err)
	}
	intake = goalintake.Approve(intake)
	g := planGoal()
	g.ID, g.SessionID, g.ProjectID = "GOAL-bound-local", intake.SessionID, string(intake.ProjectID)
	g.OriginalRequest, g.RequestDigest, g.ConstitutionVersion = intake.OriginalRequest, intake.RequestDigest, intake.Version.String()
	g.Constraints = intake.Constraints
	if err := r.Store().SaveGoalContract(ctx, g, 0); err != nil {
		t.Fatal(err)
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(ctx)
	if local.principal.ProjectID() != string(binding.ID) {
		t.Fatal("principal is not canonically scoped")
	}
	grants, err := r.Store().ListCapabilityGrants(ctx)
	if err != nil || len(grants) != 17 || string(grants[0].TaskID) != string(binding.ID) {
		t.Fatalf("canonical grant: %+v %v", grants, err)
	}
	for _, grant := range grants {
		if string(grant.TaskID) != string(binding.ID) {
			t.Fatalf("noncanonical grant: %+v", grant)
		}
	}
	e := CommandEnvelope{ProjectID: string(binding.ID), SessionID: g.SessionID, TargetID: g.ID, ExpectedVersion: 1, IdempotencyKey: "bound-proof"}
	next, err := r.CommandReviseGoal(ctx, e, "Fix only the README typo.", "owner clarification")
	if err != nil {
		t.Fatal(err)
	}
	read, err := r.Store().GetActiveGoalContract(ctx, g.SessionID)
	if err != nil || read.ProjectID != string(binding.ID) || read.Revision != 2 || read.DesiredOutcome != next.DesiredOutcome || read.Confirmation != model.ConfirmationPending {
		t.Fatalf("readback: %+v %v", read, err)
	}
	later := e
	later.ExpectedVersion, later.IdempotencyKey = 2, "bound-later"
	if _, err := r.CommandReviseGoal(ctx, later, "Later wording", "later clarification"); err != nil {
		t.Fatal(err)
	}
	replay, err := r.CommandReviseGoal(ctx, e, "Fix only the README typo.", "owner clarification")
	if err != nil || replay.Revision != next.Revision || replay.DesiredOutcome != next.DesiredOutcome {
		t.Fatalf("stored replay: %+v %v", replay, err)
	}
	wrong := e
	wrong.ProjectID = legacy.ID
	if _, err := r.CommandReviseGoal(ctx, wrong, "Fix only the README typo.", "owner clarification"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("legacy envelope: %v", err)
	}
}

func TestLocalControlUnboundLegacyFallback(t *testing.T) {
	r := runtimeForPlan(t)
	if err := os.Remove(filepath.Join(r.layout.RuntimeDir, projectid.BindingFileName)); err != nil {
		t.Fatal(err)
	}
	if r.ProjectIdentity() != localProjectID {
		t.Fatal("legacy fallback changed")
	}
	ctx := context.Background()
	g := planGoal()
	g.ProjectID = r.ProjectIdentity()
	if err := r.Store().SaveGoalContract(ctx, g, 1); err != nil {
		t.Fatal(err)
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: g.ProjectID, SessionID: g.SessionID, TargetID: g.ID, ExpectedVersion: 2, IdempotencyKey: "legacy-fallback"}
	result, err := r.CommandReviseGoal(local.Context(ctx), e, "Legacy wording", "owner clarification")
	if err != nil || result.Revision != 3 || result.ProjectID != localProjectID {
		t.Fatalf("legacy revision: %+v %v", result, err)
	}
}
