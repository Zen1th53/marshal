package hooks_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/hooks"
	"github.com/Zen1th53/marshal/internal/model"
)

type mockMemoryFinder struct {
	records []model.MemoryRecordV2
	err     error
}

func (m *mockMemoryFinder) Find(ctx context.Context, projectID, query string, scopes []string) ([]model.MemoryRecordV2, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.records, nil
}

func TestT118AutomaticRecallHooks(t *testing.T) {
	now := time.Now().UTC()
	finder := &mockMemoryFinder{
		records: []model.MemoryRecordV2{
			{
				ID:        "MEM-FAIL-1",
				ProjectID: "PROJ-1",
				Kind:      model.MemoryKindFinding,
				Title:     "SQLite lock timeout on retry",
				Body:      "Use exponential backoff when database is locked.",
				ScopeID:   "scope-1",
				CreatedAt: now,
			},
		},
	}

	hook := hooks.NewRecallHook(hooks.Config{
		Finder:  finder,
		Timeout: 50 * time.Millisecond,
	})
	ctx := context.Background()

	// 1. Task start hook retrieves relevant memory
	resStart, err := hook.OnTaskStart(ctx, "PROJ-1", "TASK-1", "Database lock optimization", []string{"scope-1"})
	if err != nil {
		t.Fatalf("OnTaskStart: %v", err)
	}
	if len(resStart) != 1 || resStart[0].ID != "MEM-FAIL-1" {
		t.Fatalf("expected MEM-FAIL-1 on task start, got: %+v", resStart)
	}

	// 2. Retry hook retrieves failure memory
	resRetry, err := hook.OnTaskRetry(ctx, "PROJ-1", "TASK-1", "Error: database is locked (sqlite3.OperationalError)", []string{"scope-1"})
	if err != nil {
		t.Fatalf("OnTaskRetry: %v", err)
	}
	if len(resRetry) == 0 {
		t.Fatal("expected failure memory on task retry")
	}

	// 3. Fallback when finder is unavailable / fails
	failingFinder := &mockMemoryFinder{err: context.DeadlineExceeded}
	failingHook := hooks.NewRecallHook(hooks.Config{
		Finder:  failingFinder,
		Timeout: 10 * time.Millisecond,
	})

	degradedRes, err := failingHook.OnTaskStart(ctx, "PROJ-1", "TASK-2", "Query", []string{"scope-1"})
	if err != nil {
		t.Fatalf("hook should not fail execution on index timeout: %v", err)
	}
	if len(degradedRes) != 0 {
		t.Fatalf("expected empty result on index failure, got: %+v", degradedRes)
	}
}
