package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT129RuntimeMemoryServiceAPI(t *testing.T) {
	svc := app.NewMemoryService()
	ctx := context.Background()
	now := time.Now().UTC()

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
	status, err := svc.Status(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Version != "2.0.0" || !status.Healthy {
		t.Fatalf("unexpected status: %+v", status)
	}

	// 2. Remember Candidate Record
	candidate, err := svc.Remember(ctx, adminPrincipal, app.RememberRequest{
		ProjectID: "PROJ-1",
		Title:     "SQLite WAL Configuration",
		Body:      "PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;",
		ScopeID:   "scope-1",
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
		MemoryID: candidate.ID,
		ScopeID:  "scope-1",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if promoted.Lifecycle != model.MemoryDurable {
		t.Fatalf("expected durable lifecycle after promotion, got: %s", promoted.Lifecycle)
	}

	// 4. Unauthorized Promotion Attempt by Read-Only Agent
	_, err = svc.Promote(ctx, readOnlyPrincipal, app.PromoteRequest{
		MemoryID: candidate.ID,
		ScopeID:  "scope-1",
	})
	if !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for read-only promotion, got: %v", err)
	}

	// 5. Recall
	recallRes, err := svc.Recall(ctx, readOnlyPrincipal, app.RecallRequest{
		ProjectID:       "PROJ-1",
		Query:           "SQLite WAL Configuration",
		AllowedScopeIDs: []string{"scope-1"},
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
	_, err = svc.Status(cancelCtx, "PROJ-1")
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	_ = now
}
