package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/model"
)

// Chaos and fault injection for Process 07 (spec 40). Each case interrupts or
// corrupts durable learning and asserts that nothing is silently promoted,
// corrupted or lost.

// A commit that fails partway must leave no trace. A half-applied commit would
// leave promoted items whose commit does not exist.
func TestChaosFailedCommitAppliesNothing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}

	// One good addition and one revision targeting a version that no longer
	// exists. The revision fails, so the addition must not land either.
	fresh := p07Item("m-new", nil)
	doomed := p07Item("m-1", nil)
	doomed.Version = 9

	c, err := learning.NewCommit(learning.Commit{
		ID: "mc-2", Binding: p07Entry(),
		Additions: []learning.Item{fresh}, Revisions: []learning.Item{doomed},
		Provenance: "chaos",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewCommit: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, c); err == nil {
		t.Fatal("a commit with an impossible revision succeeded")
	}

	if _, err := st.GetMemoryItem(ctx, "m-new"); !errors.Is(err, learning.ErrNotFound) {
		t.Fatalf("the addition from a failed commit was persisted: %v", err)
	}
	if _, err := st.GetMemoryCommit(ctx, "mc-2"); !errors.Is(err, learning.ErrNotFound) {
		t.Fatalf("a failed commit was persisted: %v", err)
	}
}

// Memory must survive a restart: durability is the point of the store.
func TestChaosMemorySurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := dir + "/state.db"

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := first.AppendMemoryCommit(ctx, p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	if err := second.Migrate(ctx); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	got, err := second.GetMemoryItem(ctx, "m-1")
	if err != nil {
		t.Fatalf("GetMemoryItem after restart: %v", err)
	}
	if got.State != model.ClaimStateVerified {
		t.Fatalf("state after restart = %s, want VERIFIED", got.State)
	}
}

// A corrupted index row must be reported, never served as canonical memory.
func TestChaosCorruptedItemIsReportedNotServed(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE memory_items SET digest='corrupt' WHERE item_id='m-1'`); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, err := st.GetMemoryItem(ctx, "m-1"); !errors.Is(err, learning.ErrTampered) {
		t.Fatalf("GetMemoryItem err = %v, want ErrTampered", err)
	}
}

// Invalidating an item that does not exist is refused rather than inventing
// one, so a lost invalidation is visible instead of silent.
func TestChaosInvalidationOfMissingItemIsRefused(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	c, err := learning.NewCommit(learning.Commit{
		ID: "mc-1", Binding: p07Entry(), Invalidations: []string{"m-missing"}, Provenance: "chaos",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewCommit: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, c); !errors.Is(err, learning.ErrInvalid) {
		t.Fatalf("AppendMemoryCommit err = %v, want ErrInvalid", err)
	}
}

// An invalidation and a promotion racing on the same claim must both be
// recorded in order: the invalidation cannot be lost.
func TestChaosInvalidationIsNotLostToAConcurrentRevision(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}

	invalidation, err := learning.NewCommit(learning.Commit{
		ID: "mc-invalidate", Binding: p07Entry(), Invalidations: []string{"m-1"}, Provenance: "chaos",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewCommit: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, invalidation); err != nil {
		t.Fatalf("invalidation: %v", err)
	}

	// A revision built against the pre-invalidation version must now fail.
	late := p07Item("m-1", nil)
	late.Version = 2
	late.State = model.ClaimStateVerified
	stale, err := learning.NewCommit(learning.Commit{
		ID: "mc-late", Binding: p07Entry(), Revisions: []learning.Item{late}, Provenance: "chaos",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewCommit: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, stale); !errors.Is(err, learning.ErrConflict) {
		t.Fatalf("late revision err = %v, want ErrConflict", err)
	}

	got, err := st.GetMemoryItem(ctx, "m-1")
	if err != nil {
		t.Fatalf("GetMemoryItem: %v", err)
	}
	if got.State != model.ClaimStateInvalidated {
		t.Fatalf("state = %s, want the invalidation to stand", got.State)
	}
}

// A duplicate commit id is refused: memory commits are append-only.
func TestChaosDuplicateCommitIsRefused(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	c := p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})
	if err := st.AppendMemoryCommit(ctx, c); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, c); err == nil {
		t.Fatal("a memory commit was applied twice")
	}
}

// The migration chain must reach the Process 07 schema from an empty database
// and be safe to re-run.
func TestChaosMigrationIsIdempotent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate pass %d: %v", i, err)
		}
	}
	var version int
	if err := st.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != LatestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, LatestSchemaVersion)
	}
	// Memory written after repeated migrations is still readable.
	if err := st.AppendMemoryCommit(ctx, p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})); err != nil {
		t.Fatalf("AppendMemoryCommit after re-migration: %v", err)
	}
	if _, err := st.GetMemoryItem(ctx, "m-1"); err != nil {
		t.Fatalf("GetMemoryItem after re-migration: %v", err)
	}
}

// Scale: retrieval and dependency fan-out must stay bounded over a large store.
func TestPerformanceMemoryScale(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test skipped in short mode")
	}
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	const items = 10_000
	batch := make([]learning.Item, 0, items)
	for i := 0; i < items; i++ {
		it := p07Item(fmt.Sprintf("m-%05d", i), []learning.Dependency{
			{Kind: "tool", ID: fmt.Sprintf("tool-%d", i%50), Version: "1.0"},
		})
		batch = append(batch, it)
	}
	c, err := learning.NewCommit(learning.Commit{
		ID: "mc-scale", Binding: p07Entry(), Additions: batch, Provenance: "scale",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewCommit: %v", err)
	}
	start := time.Now()
	if err := st.AppendMemoryCommit(ctx, c); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}
	t.Logf("wrote %d items in %s", items, time.Since(start))

	// Dependency fan-out is indexed, so one tool change touches only the
	// items that rest on it rather than scanning the whole graph.
	start = time.Now()
	affected, err := st.ItemsDependingOn(ctx, learning.Dependency{Kind: "tool", ID: "tool-7", Version: "2.0"})
	if err != nil {
		t.Fatalf("ItemsDependingOn: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("dependency fan-out returned %d items in %s", len(affected), elapsed)
	if len(affected) != items/50 {
		t.Fatalf("fan-out = %d items, want %d", len(affected), items/50)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("dependency fan-out took %s, want it bounded", elapsed)
	}

	// Retrieval over the full store stays bounded regardless of store size.
	start = time.Now()
	all, err := st.ListMemoryItems(ctx, "proj-1", true)
	if err != nil {
		t.Fatalf("ListMemoryItems: %v", err)
	}
	results := learning.Retrieve(all, learning.Query{ProjectID: "proj-1", Limit: 50}, time.Now().UTC())
	elapsed = time.Since(start)
	t.Logf("retrieval over %d items returned %d results in %s", len(all), len(results), elapsed)
	if len(results) != 50 {
		t.Fatalf("results = %d, want the requested bound of 50", len(results))
	}
	if elapsed > 10*time.Second {
		t.Fatalf("retrieval took %s, want it bounded", elapsed)
	}
}
