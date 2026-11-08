package store

import (
	"errors"
	"github.com/Zen1th53/marshal/internal/model"
	"testing"
	"time"
)

func TestOrdinaryMemoryMutationSnapshotRetainsExistingBehavior(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.InitProject(ctx, model.Project{ID: "PROJECT-ordinary-memory-row", Repository: "fixture", DefaultBranch: "main", PackVersion: "1.0"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Hour)
	rec := model.MemoryRecordV2{ID: "MEM-ordinary-row", ProjectID: "PROJECT-ordinary-memory-row", Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Authority: model.AuthorityAgent, Scope: "project", ScopeID: "PROJECT-ordinary-memory-row", Body: "Ordinary mutation retains its original contract", Revision: 1, CreatedAt: now, UpdatedAt: now, ObservedAt: now, ValidFrom: now, Source: model.MemorySource{Kind: "runtime_service"}}
	if err := s.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("ordinary caller rollback")
	_, err := s.UpdateMemory(ctx, rec.ProjectID, rec.ID, 1, func(row *model.MemoryRecordV2) error {
		if !row.CreatedAt.Equal(rec.CreatedAt) || !row.UpdatedAt.IsZero() {
			t.Fatal("ordinary snapshot contract changed")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal("ordinary rollback changed", err)
	}
}
