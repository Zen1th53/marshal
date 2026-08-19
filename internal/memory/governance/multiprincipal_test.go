package governance_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/governance"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT154MultiPrincipalSharedMemoryGovernance(t *testing.T) {
	ctx := context.Background()
	gov := governance.NewMultiPrincipalGovernance()

	// 1. Store records across different tenant scopes
	recTenantA := model.MemoryRecordV2{
		ID:        "MEM-TENANT-A-01",
		Scope:     "tenant",
		ScopeID:   "tenant-A",
		Title:     "Confidential Project A Spec",
		Body:      "Secret algorithm details",
		Lifecycle: model.MemoryDurable,
	}

	recShared := model.MemoryRecordV2{
		ID:        "MEM-SHARED-01",
		Scope:     "shared",
		ScopeID:   "public-team",
		Title:     "Standard Go Linter Rules",
		Body:      "Use golangci-lint with govulncheck",
		Lifecycle: model.MemoryDurable,
	}

	gov.StoreRecord(recTenantA)
	gov.StoreRecord(recShared)

	// 2. Direct-ID guess by unauthorized principal (Tenant B trying to fetch Tenant A by ID)
	principalB := governance.Principal{ID: "agent-B", AllowedScopeIDs: []string{"tenant-B", "public-team"}}
	_, err := gov.GetMemoryByID(ctx, principalB, "MEM-TENANT-A-01")
	if !errors.Is(err, governance.ErrUnauthorizedMemoryAccess) {
		t.Fatalf("expected ErrUnauthorizedMemoryAccess for cross-tenant direct ID guess, got: %v", err)
	}

	// 3. Authorized fetch of shared record succeeds
	sharedRec, err := gov.GetMemoryByID(ctx, principalB, "MEM-SHARED-01")
	if err != nil || sharedRec.ID != "MEM-SHARED-01" {
		t.Fatalf("expected successful fetch of shared record, got: %+v (err: %v)", sharedRec, err)
	}

	// 4. Revocation / Deletion invalidates cache and prevents resurrection
	gov.RevokeRecord("MEM-SHARED-01")
	_, err = gov.GetMemoryByID(ctx, principalB, "MEM-SHARED-01")
	if !errors.Is(err, governance.ErrMemoryNotFoundOrRevoked) {
		t.Fatalf("expected ErrMemoryNotFoundOrRevoked after record revocation, got: %v", err)
	}
}
