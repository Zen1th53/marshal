//go:build !governed

package store

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/model"
	"testing"
	"time"
)

func TestOperatorMemoryMutationUsesExactSQLTimestampsAndRefusesMalformed(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.InitProject(ctx, model.Project{ID: "PROJECT-full-memory-row", Repository: "fixture", DefaultBranch: "main", PackVersion: "1.0"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Hour)
	rec := model.MemoryRecordV2{ID: "MEM-full-row", ProjectID: "PROJECT-full-memory-row", Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Authority: model.AuthorityAgent, Scope: "project", ScopeID: "PROJECT-full-memory-row", Body: "Explicit retained review source", Revision: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now, ObservedAt: now, IngestedAt: now, ValidFrom: now, Source: model.MemorySource{Kind: "runtime_service"}}
	if err := s.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("retain original fixture")
	_, err := s.UpdateMemory(WithMemoryMutationFullSnapshot(ctx), rec.ProjectID, rec.ID, 1, func(row *model.MemoryRecordV2) error {
		if !row.CreatedAt.Equal(rec.CreatedAt) || !row.UpdatedAt.Equal(rec.UpdatedAt) {
			t.Fatal("mutator did not receive actual SQL timestamps")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal("full-snapshot rollback missing", err)
	}
	for _, column := range []string{"created_at", "updated_at"} {
		if _, err = s.db.ExecContext(ctx, "UPDATE memory_records_v2 SET "+column+"='malformed' WHERE memory_id=?", rec.ID); err != nil {
			t.Fatal(err)
		}
		called := false
		if _, err = s.UpdateMemory(WithMemoryMutationFullSnapshot(ctx), rec.ProjectID, rec.ID, 1, func(*model.MemoryRecordV2) error { called = true; return nil }); err == nil || called {
			t.Fatal("malformed timestamp reached promotion mutator", column, err)
		}
		var revision int64
		if err = s.db.QueryRowContext(ctx, "SELECT revision FROM memory_records_v2 WHERE memory_id=?", rec.ID).Scan(&revision); err != nil || revision != 1 {
			t.Fatal("malformed snapshot changed canonical revision", err)
		}
		value := rec.CreatedAt
		if column == "updated_at" {
			value = rec.UpdatedAt
		}
		if _, err = s.db.ExecContext(ctx, "UPDATE memory_records_v2 SET "+column+"=? WHERE memory_id=?", value.Format(time.RFC3339Nano), rec.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.ReadMemoryReviewPage(context.Background(), rec.ProjectID, "owner", "", 0); err == nil {
		t.Fatal("unbounded review page accepted")
	}
}
