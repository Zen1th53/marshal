package recommendation

import "testing"

func TestRecommendationStruct(t *testing.T) {
	r := Recommendation{ID: "rec-1", Kind: "CONTEXT_TUNING", Confidence: 0.92}
	if r.ID != "rec-1" {
		t.Fatalf("expected rec-1, got %s", r.ID)
	}
}
