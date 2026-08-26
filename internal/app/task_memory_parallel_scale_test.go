package app

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/working"
)

func TestTaskMemoryParallelScale(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_MEMORY_PARALLEL_SCALE") != "1" {
		t.Skip("set MARSHAL_TEST_MEMORY_PARALLEL_SCALE=1 for 2/5/10/20/50-agent measurements")
	}
	for _, agents := range []int{2, 5, 10, 20, 50} {
		t.Run(fmt.Sprintf("agents_%d", agents), func(t *testing.T) {
			ctx := context.Background()
			rt, svc := openTestMemoryService(t)
			taskID := fmt.Sprintf("TASK-PARALLEL-%d", agents)
			principals := make([]authz.Principal, agents)
			for i := range principals {
				principals[i] = testPrincipal(fmt.Sprintf("agent-parallel-%d-%d", agents, i))
			}
			grantTaskMemoryAccess(t, rt, taskID, principals...)

			start := make(chan struct{})
			writeLatency := make([]time.Duration, agents)
			writeErrors := make(chan error, agents)
			var wg sync.WaitGroup
			for i := range principals {
				wg.Add(1)
				go func(index int) {
					defer wg.Done()
					<-start
					began := time.Now()
					_, err := svc.SetTaskSlotWithProvenance(ctx, principals[index], "PROJECT-local", taskID,
						working.SlotType(fmt.Sprintf("finding_%03d", index)), fmt.Sprintf("verified finding %03d", index), false,
						WorkingProvenance{Provider: "synthetic-scale"})
					writeLatency[index] = time.Since(began)
					writeErrors <- err
				}(i)
			}
			wallStart := time.Now()
			close(start)
			wg.Wait()
			wall := time.Since(wallStart)
			close(writeErrors)
			for err := range writeErrors {
				if err != nil {
					t.Fatal(err)
				}
			}

			refreshLatency := make([]time.Duration, agents)
			missed, duplicates := 0, 0
			for i := range principals {
				began := time.Now()
				page, err := svc.RefreshTaskMemory(ctx, principals[i], "PROJECT-local", taskID, 0, 200)
				refreshLatency[i] = time.Since(began)
				if err != nil {
					t.Fatal(err)
				}
				seen := make(map[int64]struct{}, len(page.Changes))
				for _, change := range page.Changes {
					if _, duplicate := seen[change.Sequence]; duplicate {
						duplicates++
					}
					seen[change.Sequence] = struct{}{}
				}
				if len(seen) < agents {
					missed += agents - len(seen)
				}
			}
			sort.Slice(writeLatency, func(i, j int) bool { return writeLatency[i] < writeLatency[j] })
			sort.Slice(refreshLatency, func(i, j int) bool { return refreshLatency[i] < refreshLatency[j] })
			percentile := func(values []time.Duration, q float64) time.Duration {
				return values[int(q*float64(len(values)-1))]
			}
			t.Logf("PARALLEL_MEMORY agents=%d wall=%s throughput=%.1f/s write_p50=%s write_p95=%s write_p99=%s refresh_p50=%s refresh_p95=%s refresh_p99=%s missed=%d duplicate=%d",
				agents, wall, float64(agents)/wall.Seconds(), percentile(writeLatency, .50), percentile(writeLatency, .95), percentile(writeLatency, .99),
				percentile(refreshLatency, .50), percentile(refreshLatency, .95), percentile(refreshLatency, .99), missed, duplicates)
			if missed != 0 || duplicates != 0 {
				t.Fatalf("delivery integrity missed=%d duplicate=%d", missed, duplicates)
			}
		})
	}
}
