//go:build governed

package marshal

import "fmt"

func validateEditionReviewPolicy(p ReviewPolicy) error {
	eff := p.Effective()
	if eff.Weakened() {
		return fmt.Errorf("%w: finalModel=%s crossReview=%s design=%s conformance=%s",
			ErrReviewPolicyLocked, eff.FinalModel, eff.CrossReview, eff.Design, eff.Conformance)
	}
	return nil
}

func effectiveEditionReviewPolicy(p ReviewPolicy) ReviewPolicy {
	eff := p.Effective()
	if eff.Weakened() {
		return DefaultReviewPolicy()
	}
	return eff
}
