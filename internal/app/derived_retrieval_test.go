package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func testPrincipal(id string) authz.Principal {
	return authz.Principal{
		ID: id,
		Role: authz.Role{
			Name: "developer",
			Authorities: []authz.Authority{
				authz.AuthorityTaskPlan,
			},
		},
	}
}

func grantTaskMemoryAccess(t testing.TB, rt *Runtime, taskID string, principals ...authz.Principal) {
	t.Helper()
	for _, principal := range principals {
		agent := model.Agent{
			ID: principal.ID, ProjectID: "PROJECT-local", DisplayName: principal.ID,
			Role: model.RoleDeveloper, Status: model.AgentRegistered,
		}
		if err := rt.Store().RegisterAgent(context.Background(), agent); err != nil {
			t.Fatalf("register task memory principal: %v", err)
		}
		binding := authz.RoleBinding{
			ID: "memory-" + taskID + "-" + principal.ID, PrincipalID: principal.ID,
			Role: "task-member", ScopeID: taskID, BoundBy: "test-operator",
			BoundAt: time.Now().UTC(), PolicyDigest: "sha256:" + strings.Repeat("a", 64),
		}
		if err := rt.Store().PutRoleBinding(context.Background(), binding); err != nil {
			t.Fatalf("grant task memory access: %v", err)
		}
	}
}

func openTestMemoryService(t *testing.T) (*Runtime, *MemoryService) {
	t.Helper()
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt, rt.Memory()
}

type degradedProvider struct{}

func (d *degradedProvider) Name() string { return "degraded-mock" }
func (d *degradedProvider) QueryCandidates(ctx context.Context, projectID string, allowedScopeIDs []string, query string, limit int) ([]CandidateResult, error) {
	return nil, errors.New("simulated remote vector service timeout")
}

type blockingProvider struct{}

func (blockingProvider) Name() string { return "blocking-provider" }
func (blockingProvider) QueryCandidates(ctx context.Context, _ string, _ []string, _ string, _ int) ([]CandidateResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestM10_DerivedIndexRebuildAndTombstone(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	p := testPrincipal("user-1")

	// 1. Remember 2 records
	r1, err := svc.Remember(ctx, p, RememberRequest{
		ProjectID: projectID,
		Title:     "SQLite WAL architecture",
		Body:      "SQLite WAL mode ensures single-writer multiple-reader concurrency",
		Kind:      model.MemoryKindSemantic,
		Scope:     model.ScopeProject,
	})
	if err != nil {
		t.Fatalf("remember r1: %v", err)
	}

	r2, err := svc.Remember(ctx, p, RememberRequest{
		ProjectID: projectID,
		Title:     "Bubblewrap rootless isolation",
		Body:      "Bubblewrap isolates build processes inside unprivileged user namespaces",
		Kind:      model.MemoryKindSemantic,
		Scope:     model.ScopeProject,
	})
	if err != nil {
		t.Fatalf("remember r2: %v", err)
	}

	// 2. Recall before tombstone
	res1, err := svc.Recall(ctx, p, RecallRequest{
		ProjectID: projectID,
		Query:     "Bubblewrap namespaces",
	})
	if err != nil {
		t.Fatalf("recall before tombstone: %v", err)
	}
	if len(res1.Results) == 0 || res1.Results[0].ID != r2.ID {
		t.Fatalf("expected r2 in recall, got %+v", res1.Results)
	}

	// 3. Tombstone r2
	_, err = rt.Store().UpdateMemory(ctx, projectID, r2.ID, r2.Revision, func(rec *model.MemoryRecordV2) error {
		rec.Lifecycle = model.MemoryTombstoned
		rec.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		t.Fatalf("tombstone r2: %v", err)
	}
	svc.InvalidateRecord(ctx, projectID, r2.ID, r2.ScopeID)

	// 4. Rebuild derived projections from SQLite
	if err := svc.RebuildProjections(ctx, projectID); err != nil {
		t.Fatalf("rebuild projections: %v", err)
	}

	// 5. Verify tombstoned r2 is NEVER returned through recall
	res2, err := svc.Recall(ctx, p, RecallRequest{
		ProjectID: projectID,
		Query:     "Bubblewrap namespaces",
	})
	if err != nil {
		t.Fatalf("recall after rebuild: %v", err)
	}
	for _, it := range res2.Results {
		if it.ID == r2.ID {
			t.Fatalf("tombstoned record returned in recall results after rebuild: %+v", res2)
		}
	}

	// 6. Active r1 still recallable
	res3, err := svc.Recall(ctx, p, RecallRequest{
		ProjectID: projectID,
		Query:     "SQLite WAL",
	})
	if err != nil {
		t.Fatalf("recall r1: %v", err)
	}
	if len(res3.Results) == 0 || res3.Results[0].ID != r1.ID {
		t.Fatalf("expected r1 in recall, got %+v", res3.Results)
	}
}

func TestM10_DegradedCandidateProviderGracefulFallback(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	p := testPrincipal("user-1")

	r1, err := svc.Remember(ctx, p, RememberRequest{
		ProjectID: projectID,
		Title:     "Deterministic Go Build Invariants",
		Body:      "Building Go binaries with CGO_ENABLED=0 guarantees reproducible output",
		Kind:      model.MemoryKindSemantic,
		Scope:     model.ScopeProject,
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}

	// Register a failing/degraded candidate provider
	svc.RegisterCandidateProvider(&degradedProvider{})

	// Recall should not fail; should gracefully fallback to lexical & exact match
	res, err := svc.Recall(ctx, p, RecallRequest{
		ProjectID: projectID,
		Query:     "reproducible output",
	})
	if err != nil {
		t.Fatalf("recall failed during provider degradation: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].ID != r1.ID {
		t.Fatalf("expected r1 to be recalled despite degraded provider: %+v", res)
	}
}

func TestM10_CandidateProviderTimeoutFallsBackToCanonicalRecall(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)
	p := testPrincipal("timeout-reader")
	rec, err := svc.Remember(ctx, p, RememberRequest{
		ProjectID: "PROJECT-local", Title: "bounded fallback", Body: "lexical recall remains available",
		Kind: model.MemoryKindSemantic, Scope: model.ScopeProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.RegisterCandidateProvider(blockingProvider{})
	started := time.Now()
	response, err := svc.Recall(ctx, p, RecallRequest{ProjectID: "PROJECT-local", Query: "bounded fallback"})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("degraded provider exceeded recall bound: %s", elapsed)
	}
	if len(response.Results) != 1 || response.Results[0].ID != rec.ID {
		t.Fatalf("canonical fallback failed: %+v", response)
	}
}

func TestM10_CacheCannotResurrectCanonicalTombstone(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	p := testPrincipal("cache-reader")
	rec, err := svc.Remember(ctx, p, RememberRequest{
		ProjectID: "PROJECT-local", Title: "cached tombstone", Body: "must remain deleted",
		Kind: model.MemoryKindSemantic, Scope: model.ScopeProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Recall(ctx, p, RecallRequest{ProjectID: "PROJECT-local", Query: "cached tombstone"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Store().TombstoneMemory(ctx, "PROJECT-local", rec.ID, rec.Revision, "test revocation"); err != nil {
		t.Fatal(err)
	}
	response, err := svc.Recall(ctx, p, RecallRequest{ProjectID: "PROJECT-local", Query: "cached tombstone"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 0 {
		t.Fatalf("cache resurrected tombstoned canonical row: %+v", response)
	}
}

func TestM10_DerivedIndexCorruptionRecoversFromCanonicalSQLite(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)
	rogue := model.MemoryRecordV2{
		ID: "MEM-ROGUE-DERIVED", ProjectID: "PROJECT-local", Title: "rogue projection marker",
		Body: "not canonical", Scope: string(model.ScopeProject), ScopeID: "PROJECT-local",
		Lifecycle: model.MemoryDurable,
	}
	if err := svc.lexicalIndex.IndexRecord(ctx, rogue); err != nil {
		t.Fatal(err)
	}
	before, err := svc.lexicalIndex.Search(ctx, "PROJECT-local", "rogue projection marker", 10)
	if err != nil || len(before) != 1 {
		t.Fatalf("failed to seed derived corruption: results=%+v err=%v", before, err)
	}
	if err := svc.RebuildProjections(ctx, "PROJECT-local"); err != nil {
		t.Fatal(err)
	}
	after, err := svc.lexicalIndex.Search(ctx, "PROJECT-local", "rogue projection marker", 10)
	if err != nil || len(after) != 0 {
		t.Fatalf("derived corruption survived canonical rebuild: results=%+v err=%v", after, err)
	}
}

func TestM10_CrossScopeIsolationAcrossTracks(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	p1 := testPrincipal("user-alice")
	p2 := testPrincipal("user-bob")

	// Alice creates an operator-private record
	now := time.Now().UTC()
	privRec := model.MemoryRecordV2{
		ID:         "MEM-PRIV-ALICE",
		ProjectID:  projectID,
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Confidence: model.ConfidenceVerified,
		Authority:  model.AuthorityOperator,
		Title:      "Alice Secret Architecture Keyphrase",
		Body:       "Confidential internal infrastructure blueprint",
		Scope:      string(model.ScopeOperatorPrivate),
		ScopeID:    "user-alice",
		ACLScope:   "user-alice",
		Source:     model.MemorySource{Kind: "operator", Reference: "user-alice"},
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := rt.Store().WriteMemoryV2(ctx, privRec); err != nil {
		t.Fatalf("write alice private: %v", err)
	}
	svc.IndexRecord(ctx, privRec)

	// Bob searches for exact keyphrase
	bobRes, err := svc.Recall(ctx, p2, RecallRequest{
		ProjectID: projectID,
		Query:     "Secret Architecture Keyphrase",
	})
	if err != nil {
		t.Fatalf("bob recall: %v", err)
	}
	if len(bobRes.Results) != 0 || len(bobRes.Receipt.Decisions) != 0 {
		t.Fatalf("Alice private memory leaked to Bob: %+v", bobRes)
	}

	// Alice can recall her record
	aliceRes, err := svc.Recall(ctx, p1, RecallRequest{
		ProjectID: projectID,
		Query:     "Secret Architecture Keyphrase",
	})
	if err != nil {
		t.Fatalf("alice recall: %v", err)
	}
	if len(aliceRes.Results) != 1 || aliceRes.Results[0].ID != privRec.ID {
		t.Fatalf("Alice could not recall her private memory: %+v", aliceRes)
	}
}
