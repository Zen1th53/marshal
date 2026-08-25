package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// TestMemoryScale100K is an opt-in workstation/scheduled gate. It is excluded
// from normal CI because its purpose is to record reproducible resource and
// latency evidence, not to turn a large fixture into a unit-test dependency.
func TestMemoryScale100K(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_MEMORY_100K") != "1" {
		t.Skip("set MARSHAL_TEST_MEMORY_100K=1 for the scheduled 100k memory benchmark")
	}
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "marshal-100k.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "PROJECT-local", Repository: "scale-fixture", DefaultBranch: "main", PackVersion: "1.0.0"}); err != nil {
		t.Fatal(err)
	}

	const total = 100_000
	now := time.Now().UTC()
	var before, afterSeed, afterRebuild runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	seedStart := time.Now()
	for i := 0; i < total; i++ {
		rec := model.MemoryRecordV2{
			ID: fmt.Sprintf("MEM-100K-%06d", i), ProjectID: "PROJECT-local",
			Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryDurable,
			Confidence: model.ConfidenceVerified, Authority: model.AuthorityVerified,
			Title: fmt.Sprintf("Component %06d architecture", i),
			Body:  fmt.Sprintf("Canonical SQLite procedure for component %d tenant %d", i, i%1000),
			Scope: string(model.ScopeProject), ScopeID: "PROJECT-local",
			Source:     model.MemorySource{Kind: "benchmark", Reference: "phase2-100k"},
			ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
		}
		if err := st.WriteMemoryV2(ctx, rec); err != nil {
			t.Fatalf("seed record %d: %v", i, err)
		}
	}
	seedDuration := time.Since(seedStart)
	runtime.ReadMemStats(&afterSeed)
	seedDBInfo, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	svc := NewMemoryService(st)
	rebuildStart := time.Now()
	if err := svc.RebuildProjections(ctx, "PROJECT-local"); err != nil {
		t.Fatal(err)
	}
	rebuildDuration := time.Since(rebuildStart)
	runtime.ReadMemStats(&afterRebuild)

	principal := testPrincipal("AGENT-scale")
	latencies := make([]time.Duration, 31)
	for i := range latencies {
		started := time.Now()
		res, err := svc.Recall(ctx, principal, RecallRequest{
			ProjectID: "PROJECT-local", Query: fmt.Sprintf("Component %06d architecture", 42000+i),
			CurrentHead: "scale-head", CanonicalHead: "scale-head", MaxRecords: 5, MaxBytes: 4096,
		})
		latencies[i] = time.Since(started)
		if err != nil || len(res.Results) == 0 {
			t.Fatalf("recall %d: results=%d err=%v", i, len(res.Results), err)
		}
	}
	coldRecall := latencies[0]
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	dbInfo, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	pick := func(q float64) time.Duration {
		idx := int(q * float64(len(latencies)-1))
		return latencies[idx]
	}
	t.Logf("MEMORY_100K records=%d seed=%s records_per_sec=%.1f db_after_seed_bytes=%d db_after_31_recalls_bytes=%d rebuild=%s recall_cold=%s recall_p50=%s recall_p95=%s recall_p99=%s heap_seed_delta=%d heap_index_delta=%d go=%s os=%s arch=%s cpu=%d",
		total, seedDuration, float64(total)/seedDuration.Seconds(), seedDBInfo.Size(), dbInfo.Size(), rebuildDuration,
		coldRecall, pick(.50), pick(.95), pick(.99),
		int64(afterSeed.HeapAlloc)-int64(before.HeapAlloc), int64(afterRebuild.HeapAlloc)-int64(afterSeed.HeapAlloc),
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
}
