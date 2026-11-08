package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvaluateKnownRanking(t *testing.T) {
	corpus := Corpus{
		Version: 1, ProjectID: "PROJECT-test",
		Records: []RecordFixture{
			{ID: "required", Title: "required", Body: "required"},
			{ID: "acceptable", Title: "acceptable", Body: "acceptable"},
			{ID: "irrelevant", Title: "irrelevant", Body: "irrelevant"},
			{ID: "stale", Title: "stale", Body: "stale"},
		},
		Queries: []QueryFixture{{
			ID: "q1", Text: "query", PrincipalID: "reader", Required: []string{"required"},
			Acceptable: []string{"acceptable"}, Irrelevant: []string{"irrelevant"},
			Forbidden: map[string][]string{"stale": {"stale"}},
		}},
	}
	metrics, err := Evaluate(corpus, []QueryOutcome{{
		QueryID: "q1", RankedIDs: []string{"irrelevant", "required", "acceptable"},
		ContextBytes: 120, RecallDuration: 12 * time.Millisecond,
	}}, []int{1, 3})
	if err != nil {
		t.Fatal(err)
	}
	if metrics.RecallAtK[1] != 0 || metrics.RecallAtK[3] != 1 {
		t.Fatalf("unexpected recall: %+v", metrics.RecallAtK)
	}
	if metrics.PrecisionAtK[1] != 0 || metrics.PrecisionAtK[3] != 2.0/3.0 {
		t.Fatalf("unexpected precision: %+v", metrics.PrecisionAtK)
	}
	if metrics.MRR != 0.5 || metrics.FalsePositiveRecallRate != 1.0/3.0 {
		t.Fatalf("unexpected reciprocal rank/false-positive rate: %+v", metrics)
	}
	if metrics.ForbiddenExposureRate["stale"] != 0 {
		t.Fatalf("unexpected forbidden exposure: %+v", metrics.ForbiddenExposureRate)
	}
	if metrics.ContextBytesPerUsefulResult != 60 || metrics.ContextTokensPerUseful != 15 {
		t.Fatalf("unexpected context cost: %+v", metrics)
	}
	if metrics.MeanTimeToFirstUseful != 12*time.Millisecond {
		t.Fatalf("unexpected time to useful result: %s", metrics.MeanTimeToFirstUseful)
	}
}

func TestLoadCorpusRejectsOverlappingClassifications(t *testing.T) {
	_, err := LoadCorpus(strings.NewReader(`{
		"version":1,"project_id":"p",
		"records":[{"id":"m","title":"t","body":"b"}],
		"queries":[{"id":"q","text":"x","principal_id":"a","required":["m"],"irrelevant":["m"]}]
	}`))
	if err == nil {
		t.Fatal("expected overlapping relevance classifications to fail validation")
	}
}

func TestEvaluateDynamicResponsivenessWhenRankingsChange(t *testing.T) {
	corpus := Corpus{
		Version:   1,
		ProjectID: "PROJECT-eval",
		Records: []RecordFixture{
			{ID: "req-1", Title: "req-1", Body: "req-1"},
			{ID: "req-2", Title: "req-2", Body: "req-2"},
			{ID: "acc-1", Title: "acc-1", Body: "acc-1"},
			{ID: "irr-1", Title: "irr-1", Body: "irr-1"},
			{ID: "irr-2", Title: "irr-2", Body: "irr-2"},
			{ID: "stale-1", Title: "stale-1", Body: "stale-1"},
		},
		Queries: []QueryFixture{{
			ID:          "q1",
			Text:        "find relevant context",
			PrincipalID: "worker",
			Required:    []string{"req-1", "req-2"},
			Acceptable:  []string{"acc-1"},
			Irrelevant:  []string{"irr-1", "irr-2"},
			Forbidden:   map[string][]string{"stale": {"stale-1"}},
		}},
	}

	// 1. Optimal ranking: all required in top 2.
	metricsOptimal, err := Evaluate(corpus, []QueryOutcome{{
		QueryID:        "q1",
		RankedIDs:      []string{"req-1", "req-2", "acc-1"},
		ContextBytes:   300,
		RecallDuration: 5 * time.Millisecond,
	}}, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("Evaluate optimal: %v", err)
	}

	// 2. Degraded ranking: required items delayed behind irrelevant items.
	metricsDegraded, err := Evaluate(corpus, []QueryOutcome{{
		QueryID:        "q1",
		RankedIDs:      []string{"irr-1", "irr-2", "req-1"},
		ContextBytes:   300,
		RecallDuration: 15 * time.Millisecond,
	}}, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("Evaluate degraded: %v", err)
	}

	// 3. Stale/forbidden exposure ranking:
	metricsForbidden, err := Evaluate(corpus, []QueryOutcome{{
		QueryID:        "q1",
		RankedIDs:      []string{"stale-1", "req-1"},
		ContextBytes:   200,
		RecallDuration: 8 * time.Millisecond,
	}}, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("Evaluate forbidden: %v", err)
	}

	// 4. Empty ranking:
	metricsEmpty, err := Evaluate(corpus, []QueryOutcome{{
		QueryID:        "q1",
		RankedIDs:      nil,
		ContextBytes:   0,
		RecallDuration: 1 * time.Millisecond,
	}}, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("Evaluate empty: %v", err)
	}

	// Verify metrics dynamically reflect outcomes and are not hardcoded constants.
	if metricsOptimal.RecallAtK[2] != 1.0 {
		t.Fatalf("optimal Recall@2 = %f, want 1.0", metricsOptimal.RecallAtK[2])
	}
	if metricsDegraded.RecallAtK[2] != 0.0 {
		t.Fatalf("degraded Recall@2 = %f, want 0.0", metricsDegraded.RecallAtK[2])
	}
	if metricsDegraded.RecallAtK[3] != 0.5 {
		t.Fatalf("degraded Recall@3 = %f, want 0.5", metricsDegraded.RecallAtK[3])
	}
	if metricsOptimal.MRR != 1.0 {
		t.Fatalf("optimal MRR = %f, want 1.0", metricsOptimal.MRR)
	}
	if metricsDegraded.MRR != 1.0/3.0 {
		t.Fatalf("degraded MRR = %f, want %f", metricsDegraded.MRR, 1.0/3.0)
	}
	if metricsEmpty.RecallAtK[1] != 0.0 || metricsEmpty.MRR != 0.0 {
		t.Fatalf("empty metrics should be 0, got Recall@1=%f MRR=%f", metricsEmpty.RecallAtK[1], metricsEmpty.MRR)
	}
	if metricsOptimal.ForbiddenExposureRate["stale"] != 0.0 {
		t.Fatalf("optimal forbidden exposure = %f, want 0.0", metricsOptimal.ForbiddenExposureRate["stale"])
	}
	if metricsForbidden.ForbiddenExposureRate["stale"] != 1.0 {
		t.Fatalf("forbidden exposure = %f, want 1.0", metricsForbidden.ForbiddenExposureRate["stale"])
	}
}

func TestRootConformanceReportsDoNotExist(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("could not locate repo root via %s: %v", root, err)
	}

	reports := []string{
		"derived-index-rebuild-report.json",
		"memory-ultimate-conformance.json",
	}
	for _, reportFile := range reports {
		path := filepath.Join(root, reportFile)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("fabricated root report %s must not exist in repository root (err=%v)", reportFile, err)
		}
	}
}
