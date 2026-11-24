//go:build !governed

package marshal

func validateEditionReviewPolicy(ReviewPolicy) error {
	return nil
}

func effectiveEditionReviewPolicy(p ReviewPolicy) ReviewPolicy {
	return p.Effective()
}
