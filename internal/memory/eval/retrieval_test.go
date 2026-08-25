package eval

import (
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
