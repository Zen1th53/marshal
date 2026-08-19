package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT86StoreRejectsSecretsOnWrite(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	projID := "PROJ-T86"
	if err := st.InitProject(ctx, model.Project{
		ID: projID, Repository: "repo", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// Attempting to write a record with an embedded secret must be rejected by firewall
	secretRec := model.MemoryRecordV2{
		ID:         "MEM-SECRET-01",
		ProjectID:  projID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryCandidate,
		Authority:  model.AuthorityAgent,
		Title:      "Secret dump",
		Body:       "Here is an API key: ghp_1234567890abcdefghijklmnopqrstuvwxyzAB",
		Scope:      string(model.ScopeProject),
		ScopeID:    projID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "runtime", Reference: "run-sec"},
	}

	err := st.WriteMemoryV2(ctx, secretRec)
	if !errors.Is(err, security.ErrSecretDetected) {
		t.Fatalf("expected ErrSecretDetected from store firewall, got: %v", err)
	}

	// Verify nothing was persisted in SQLite
	_, err = st.GetMemoryV2(ctx, projID, "MEM-SECRET-01")
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for rejected secret memory, got: %v", err)
	}
}
