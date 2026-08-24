package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func openBenchMemoryService(b *testing.B) (*Runtime, *MemoryService) {
	b.Helper()
	dir := b.TempDir()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "marshal.db"))
	if err != nil {
		b.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID:            "PROJECT-local",
		Repository:    "local",
		DefaultBranch: "main",
		PackVersion:   "1.0.0",
	}); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	svc := NewMemoryService(st)
	return &Runtime{store: st}, svc
}

func BenchmarkMemoryIngestionThroughput(b *testing.B) {
	ctx := context.Background()
	rt, _ := openBenchMemoryService(b)

	const projectID = "PROJECT-local"
	now := time.Now().UTC()

	b.ResetTimer()
	started := time.Now()
	for i := 0; i < b.N; i++ {
		rec := model.MemoryRecordV2{
			ID:         fmt.Sprintf("MEM-BENCH-%d", i),
			ProjectID:  projectID,
			Kind:       model.MemoryKindSemantic,
			Lifecycle:  model.MemoryDurable,
			Confidence: model.ConfidenceVerified,
			Authority:  model.AuthorityOperator,
			Title:      fmt.Sprintf("Benchmark Record %d", i),
			Body:       "High throughput memory insertion benchmark test body",
			Scope:      string(model.ScopeProject),
			ScopeID:    projectID,
			ObservedAt: now,
			IngestedAt: now,
			ValidFrom:  now,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := rt.Store().WriteMemoryV2(ctx, rec); err != nil {
			b.Fatalf("write memory: %v", err)
		}
	}
	b.ReportMetric(float64(b.N)/time.Since(started).Seconds(), "records/s")
}

func BenchmarkWorkingMemoryCASThroughput(b *testing.B) {
	ctx := context.Background()
	rt, svc := openBenchMemoryService(b)

	const projectID = "PROJECT-local"
	const taskID = "TASK-BENCH-CAS"
	p := testPrincipal("agent-bench")
	grantTaskMemoryAccess(b, rt, taskID, p)

	slot, err := svc.SetTaskSlot(ctx, p, projectID, taskID, working.SlotPlanState, "initial", true)
	if err != nil {
		b.Fatalf("set slot: %v", err)
	}

	curRev := slot.Revision

	b.ResetTimer()
	started := time.Now()
	for i := 0; i < b.N; i++ {
		updated, err := svc.UpdateTaskSlotCAS(ctx, p, projectID, taskID, working.SlotPlanState, curRev, fmt.Sprintf("val-%d", i))
		if err != nil {
			b.Fatalf("cas update: %v", err)
		}
		curRev = updated.Revision
	}
	b.ReportMetric(float64(b.N)/time.Since(started).Seconds(), "cas_writes/s")
}

func BenchmarkDerivedIndexRebuild10kRecords(b *testing.B) {
	ctx := context.Background()
	rt, svc := openBenchMemoryService(b)

	const projectID = "PROJECT-local"
	const totalRecords = 10000
	now := time.Now().UTC()

	// Seed 10,000 records
	for i := 0; i < totalRecords; i++ {
		rec := model.MemoryRecordV2{
			ID:         fmt.Sprintf("MEM-10K-%05d", i),
			ProjectID:  projectID,
			Kind:       model.MemoryKindSemantic,
			Lifecycle:  model.MemoryDurable,
			Confidence: model.ConfidenceVerified,
			Authority:  model.AuthorityOperator,
			Title:      fmt.Sprintf("Module Security Specification %d", i),
			Body:       fmt.Sprintf("Authentication and token verification procedures for service %d", i%100),
			Scope:      string(model.ScopeProject),
			ScopeID:    projectID,
			ObservedAt: now,
			IngestedAt: now,
			ValidFrom:  now,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := rt.Store().WriteMemoryV2(ctx, rec); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}

	b.ResetTimer()
	started := time.Now()
	for i := 0; i < b.N; i++ {
		if err := svc.RebuildProjections(ctx, projectID); err != nil {
			b.Fatalf("rebuild: %v", err)
		}
	}
	b.ReportMetric(float64(totalRecords*b.N)/time.Since(started).Seconds(), "records_rebuilt/s")
}

func BenchmarkRecallLatency10kRecords(b *testing.B) {
	ctx := context.Background()
	rt, svc := openBenchMemoryService(b)

	const projectID = "PROJECT-local"
	const totalRecords = 10000
	now := time.Now().UTC()

	// Seed 10,000 records
	for i := 0; i < totalRecords; i++ {
		rec := model.MemoryRecordV2{
			ID:         fmt.Sprintf("MEM-RECALL-%05d", i),
			ProjectID:  projectID,
			Kind:       model.MemoryKindSemantic,
			Lifecycle:  model.MemoryDurable,
			Confidence: model.ConfidenceVerified,
			Authority:  model.AuthorityOperator,
			Title:      fmt.Sprintf("Microservice Authorization Architecture %d", i),
			Body:       fmt.Sprintf("Token authorization policy and rate limiting config for tenant %d", i%50),
			Scope:      string(model.ScopeProject),
			ScopeID:    projectID,
			ObservedAt: now,
			IngestedAt: now,
			ValidFrom:  now,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := rt.Store().WriteMemoryV2(ctx, rec); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}

	if err := svc.RebuildProjections(ctx, projectID); err != nil {
		b.Fatalf("rebuild: %v", err)
	}

	p := testPrincipal("agent-bench")

	b.ResetTimer()
	started := time.Now()
	for i := 0; i < b.N; i++ {
		res, err := svc.Recall(ctx, p, RecallRequest{
			ProjectID: projectID,
			Query:     "Microservice Authorization Architecture 42",
		})
		if err != nil || len(res.Results) == 0 {
			b.Fatalf("recall failed: err=%v results=%d", err, len(res.Results))
		}
	}
	b.ReportMetric(float64(b.N)/time.Since(started).Seconds(), "recalls/s")
}

func TestM20_Scale10kRecordsVerification(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 10k scale test in short mode")
	}

	ctx := context.Background()
	rt, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	const totalRecords = 10000
	now := time.Now().UTC()

	t.Logf("Seeding %d canonical memory records into SQLite...", totalRecords)
	seedStart := time.Now()
	for i := 0; i < totalRecords; i++ {
		rec := model.MemoryRecordV2{
			ID:         fmt.Sprintf("MEM-SCALE-%05d", i),
			ProjectID:  projectID,
			Kind:       model.MemoryKindSemantic,
			Lifecycle:  model.MemoryDurable,
			Confidence: model.ConfidenceVerified,
			Authority:  model.AuthorityOperator,
			Title:      fmt.Sprintf("Subsystem Architecture Invariant %d", i),
			Body:       fmt.Sprintf("Database pooling and transaction boundary specification for service component %d", i%100),
			Scope:      string(model.ScopeProject),
			ScopeID:    projectID,
			ObservedAt: now,
			IngestedAt: now,
			ValidFrom:  now,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := rt.Store().WriteMemoryV2(ctx, rec); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	seedDuration := time.Since(seedStart)
	t.Logf("Seeding completed in %v (%0.1f records/sec)", seedDuration, float64(totalRecords)/seedDuration.Seconds())

	// Rebuild Projections
	rebuildStart := time.Now()
	if err := svc.RebuildProjections(ctx, projectID); err != nil {
		t.Fatalf("rebuild projections: %v", err)
	}
	rebuildDur := time.Since(rebuildStart)
	t.Logf("Derived index rebuild of 10,000 records completed in %v", rebuildDur)
	if rebuildDur > 10*time.Second {
		t.Fatalf("rebuild exceeded 10s threshold: %v", rebuildDur)
	}

	// Recall over 10k records
	p := testPrincipal("developer-1")
	recallStart := time.Now()
	res, err := svc.Recall(ctx, p, RecallRequest{
		ProjectID: projectID,
		Query:     "Subsystem Architecture Invariant 42",
	})
	recallDur := time.Since(recallStart)
	if err != nil {
		t.Fatalf("recall over 10k: %v", err)
	}
	t.Logf("Recall over 10,000 records latency: %v (found %d records)", recallDur, len(res.Results))
	if len(res.Results) == 0 {
		t.Fatalf("expected recall results over 10k records")
	}

	// Concurrent Recall
	var wg sync.WaitGroup
	var mu sync.Mutex
	var maxDur time.Duration
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			qStart := time.Now()
			qRes, qErr := svc.Recall(ctx, p, RecallRequest{
				ProjectID: projectID,
				Query:     fmt.Sprintf("Subsystem Architecture Invariant %d", idx*5),
			})
			qDur := time.Since(qStart)
			mu.Lock()
			if qDur > maxDur {
				maxDur = qDur
			}
			mu.Unlock()
			if qErr != nil || len(qRes.Results) == 0 {
				t.Errorf("concurrent recall %d failed: err=%v results=%d", idx, qErr, len(qRes.Results))
			}
		}(i)
	}
	wg.Wait()
	t.Logf("20 concurrent recalls completed, max latency: %v", maxDur)
}
