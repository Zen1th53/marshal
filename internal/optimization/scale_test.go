package optimization

import (
	"fmt"
	"testing"
	"time"
)

// TestProcess08ScaleCandidateAndResultAggregation exercises the largest
// in-memory control-plane collections named by the Process 08 scale pack. It
// is deliberately deterministic: it reports elapsed time for regression
// visibility, but has no machine-specific throughput threshold that could
// turn a slower qualified host into a fabricated failure.
func TestProcess08ScaleCandidateAndResultAggregation(t *testing.T) {
	started := time.Now()
	candidates := make([]Candidate, 0, 1_000)
	for i := 0; i < 1_000; i++ {
		candidates = append(candidates, Candidate{
			ID:               fmt.Sprintf("candidate-%04d", i),
			Dimension:        DimRouting,
			Hypothesis:       fmt.Sprintf("route variant %04d", i),
			TaskScope:        []string{"code"},
			RollbackPlan:     "restore pinned baseline",
			VerificationPlan: "independent verifier",
			Provenance:       "scale-fixture",
		})
	}
	clusters, err := Cluster(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(clusters); got != len(candidates) {
		t.Fatalf("clusters=%d, want %d distinct candidates", got, len(candidates))
	}

	results := make([]ExperimentResult, 0, 10_000)
	for i := 0; i < 10_000; i++ {
		results = append(results, ExperimentResult{
			ID:        fmt.Sprintf("result-%05d", i),
			TaskID:    fmt.Sprintf("task-%05d", i),
			TaskClass: "code",
			Outcome:   StatusPass,
			ClusterID: fmt.Sprintf("cluster-%02d", i%10),
		})
	}
	if flaky := DetectFlaky(results); len(flaky) != 0 {
		t.Fatalf("deterministic result history was marked flaky: %v", flaky)
	}
	comparison := ComparePaired(results[:5_000], results[5_000:])
	if comparison.Tasks != 0 {
		t.Fatalf("disjoint task sets unexpectedly paired: %+v", comparison)
	}
	t.Logf("process08 scale: candidates=%d results=%d clusters=%d elapsed=%s", len(candidates), len(results), len(clusters), time.Since(started))
}
