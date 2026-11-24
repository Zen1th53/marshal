//go:build governed

package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The governed edition never selects checks-only proof: any weakened snapshot
// (whether final-model or cross-review opt-out) is refused, and its effective
// policy reads as the shipped defaults.
func TestGovernedRejectsChecksOnlyDelivery(t *testing.T) {
	cases := []struct {
		name     string
		snapshot marshal.ReviewPolicySnapshot
	}{
		{
			name: "final-model-off",
			snapshot: marshal.ReviewPolicySnapshot{
				PolicyVersion: marshal.ReviewPolicyVersion,
				FinalModel:    marshal.FinalModelOff,
				CrossReview:   marshal.CrossReviewRequired,
				Design:        marshal.DesignReviewOnDemand,
				Conformance:   marshal.ConformanceEnforce,
			},
		},
		{
			name: "cross-review-off",
			snapshot: marshal.ReviewPolicySnapshot{
				PolicyVersion: marshal.ReviewPolicyVersion,
				FinalModel:    marshal.FinalModelRequired,
				CrossReview:   marshal.CrossReviewOff,
				Design:        marshal.DesignReviewOnDemand,
				Conformance:   marshal.ConformanceEnforce,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := tc.snapshot
			run := marshal.Run{ReviewPolicy: &snap}
			if err := run.ValidateReviewPolicy(); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
				t.Fatalf("weakened snapshot = %v, want ErrReviewPolicyLocked", err)
			}
			if checksOnlyDelivery(run) {
				t.Fatal("governed edition selected checks-only delivery")
			}
		})
	}
}
