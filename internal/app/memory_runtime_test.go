package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestT129RuntimeMemoryServiceAPI(t *testing.T) {
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	svc := app.NewMemoryService(runtime.Store())
	ctx := context.Background()
	now := time.Now().UTC()

	proj, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := proj.ID

	adminPrincipal := authz.Principal{
		ID: "admin-user",
		Role: authz.Role{
			Name:        "operator",
			Authorities: []authz.Authority{authz.AuthorityPolicyAdmin},
		},
	}

	readOnlyPrincipal := authz.Principal{
		ID: "agent-ro",
		Role: authz.Role{
			Name:        "reader",
			Authorities: []authz.Authority{authz.AuthorityTaskPlan},
		},
	}

	// 1. Service Status
	status, err := svc.Status(ctx, projectID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Version != "2.0.0" || !status.Healthy {
		t.Fatalf("unexpected status: %+v", status)
	}

	// 2. Remember Candidate Record
	candidate, err := svc.Remember(ctx, adminPrincipal, app.RememberRequest{
		ProjectID: projectID,
		Title:     "SQLite WAL Configuration",
		Body:      "PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;",
		ScopeID:   projectID,
		Kind:      model.MemoryKindDecision,
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if candidate.Lifecycle != model.MemoryCandidate {
		t.Fatalf("expected candidate lifecycle on write, got: %s", candidate.Lifecycle)
	}

	// 3. Promote Candidate (Authorized by Admin)
	promoted, err := svc.Promote(ctx, adminPrincipal, app.PromoteRequest{
		ProjectID: projectID,
		MemoryID:  candidate.ID,
		ScopeID:   projectID,
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if promoted.Lifecycle != model.MemoryDurable {
		t.Fatalf("expected durable lifecycle after promotion, got: %s", promoted.Lifecycle)
	}

	// 4. Unauthorized Promotion Attempt by Read-Only Agent
	_, err = svc.Promote(ctx, readOnlyPrincipal, app.PromoteRequest{
		ProjectID: projectID,
		MemoryID:  candidate.ID,
		ScopeID:   projectID,
	})
	if !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for read-only promotion, got: %v", err)
	}

	// 5. Recall
	recallRes, err := svc.Recall(ctx, readOnlyPrincipal, app.RecallRequest{
		ProjectID:       projectID,
		Query:           "SQLite WAL Configuration",
		AllowedScopeIDs: []string{projectID},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(recallRes.Results) == 0 || recallRes.Results[0].ID != candidate.ID {
		t.Fatalf("expected candidate in recall results, got: %+v", recallRes)
	}

	// 6. Context cancellation
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.Status(cancelCtx, projectID)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	_ = now
}
