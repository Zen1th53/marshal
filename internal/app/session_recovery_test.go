package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestSessionRecoveryApplicationFailsClosedWithoutHooksOrLocalAuthority(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := context.Background()
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "session", TargetID: "recovery", IdempotencyKey: "checkpoint"}
	if _, err := r.WorkspaceRecovery(SessionRecoveryHooks{}).Checkpoint(ctx, e, model.SessionRecoveryBinding{}, nil, ""); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("missing hooks: %v", err)
	}
	hooks := SessionRecoveryHooks{Freeze: func(context.Context) (func(), error) { return func() {}, nil }, Observe: func(context.Context, model.SessionRecovery) (model.SessionRecoveryBinding, MemoryReconcileRequest, bool, error) {
		t.Fatal("unauthenticated caller reached observer")
		return model.SessionRecoveryBinding{}, MemoryReconcileRequest{}, false, nil
	}}
	if _, err := r.WorkspaceRecovery(hooks).Checkpoint(ctx, e, model.SessionRecoveryBinding{}, nil, ""); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("caller minted recovery authority: %v", err)
	}
}

// This test intentionally waits for I's canonical v94 registration. Worker
// service tests use the migration helper provisionally in the store package;
// that must never be misrepresented as production runtime migration evidence.
func TestSessionRecoveryApplicationAdoptionAfterMigrationRegistration(t *testing.T) {
	if store.LatestSchemaVersion < 94 {
		t.Skip("integration prerequisite: I must register memory v93 and recovery v94")
	}
	r := runtimeForPlan(t)
	ctx := context.Background()
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(ctx)
	g, err := r.store.GetActiveGoalContract(ctx, "SESSION-plan")
	if err != nil {
		t.Fatal(err)
	}
	g.ProjectID = r.ProjectIdentity()
	g.RequestDigest = model.SessionRecoveryDigest(g.OriginalRequest)
	g.Revision++
	if err = r.store.SaveGoalContract(ctx, g, g.Revision-1); err != nil {
		t.Fatal(err)
	}
	b := model.SessionRecoveryBinding{ProjectID: r.ProjectIdentity(), ProjectRoot: r.ProjectRoot(), WorktreeDigest: "fixture-worktree", ConstitutionVersion: constitution.Current.String(), PolicyDigest: "none", GoalID: g.ID, GoalRevision: g.Revision, OriginalIntentDigest: g.RequestDigest, GoalBudgetDigest: model.SessionRecoveryDigest(g.BudgetLimits), BudgetEvidenceDigest: "fixture-budget"}
	if policy, err := r.store.GetActivePolicy(ctx); err == nil {
		b.PolicyID = string(policy.Policy.ID)
		b.PolicyVersion = int64(policy.Policy.Version)
		b.PolicyDigest = string(policy.Binding.Digest)
	} else if !errors.Is(err, model.ErrNotFound) {
		t.Fatal(err)
	}
	var mu sync.Mutex
	hooks := SessionRecoveryHooks{Freeze: func(context.Context) (func(), error) { mu.Lock(); return mu.Unlock, nil }, Observe: func(context.Context, model.SessionRecovery) (model.SessionRecoveryBinding, MemoryReconcileRequest, bool, error) {
		return b, MemoryReconcileRequest{ProjectID: b.ProjectID}, true, nil
	}}
	service := r.WorkspaceRecovery(hooks)
	e := CommandEnvelope{ProjectID: b.ProjectID, SessionID: g.SessionID, TargetID: "recovery-context", IdempotencyKey: "checkpoint"}
	checkpoint, err := service.Checkpoint(ctx, e, b, nil, "")
	if err != nil {
		t.Fatalf("checkpoint (requires I's session.recover local grant): %v", err)
	}
	e.ExpectedVersion = checkpoint.Generation
	e.IdempotencyKey = "adopt"
	adopted, err := service.Adopt(ctx, e, checkpoint.Digest, "new-workspace")
	if err != nil {
		t.Fatal(err)
	}
	if adopted.State != "adopted" || adopted.AdoptedSessionID != "new-workspace" || adopted.Binding != checkpoint.Binding {
		t.Fatalf("adoption: %+v", adopted)
	}
	replay, err := service.Adopt(ctx, e, checkpoint.Digest, "new-workspace")
	if err != nil || replay.Digest != adopted.Digest {
		t.Fatalf("replay: %+v %v", replay, err)
	}
}
