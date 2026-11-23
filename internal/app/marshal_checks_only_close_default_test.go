//go:build governed

package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The governed edition never selects checks-only proof: a weakened snapshot
// is refused, and its effective policy reads as the shipped defaults.
func TestGovernedRejectsChecksOnlyDelivery(t *testing.T) {
	run := marshal.Run{ReviewPolicy: &marshal.ReviewPolicySnapshot{PolicyVersion: marshal.ReviewPolicyVersion, FinalModel: marshal.FinalModelOff, CrossReview: marshal.CrossReviewRequired, Design: marshal.DesignReviewOnDemand, Conformance: marshal.ConformanceEnforce}}
	if err := run.ValidateReviewPolicy(); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("weakened snapshot = %v, want ErrReviewPolicyLocked", err)
	}
	if checksOnlyDelivery(run) {
		t.Fatal("governed edition selected checks-only delivery")
	}
}
