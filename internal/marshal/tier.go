package marshal

import (
	"errors"
	"strings"
)

const CapabilityMarshal = "ultra.marshal"

// CapabilityGate reports whether a capability is currently granted.
type CapabilityGate interface {
	Capability(name string) bool
}

// DispatchPolicy is the execution policy selected for one dispatch.
type DispatchPolicy struct {
	Tier                Tier
	Concurrency         int
	CrossReviewRequired bool
	VerifierRequired    bool
}

// TierPolicy reads the gate when dispatch is about to start.
func TierPolicy(gate CapabilityGate, settings Settings) DispatchPolicy {
	if gate == nil || !gate.Capability(CapabilityMarshal) {
		return DispatchPolicy{Tier: Standard, Concurrency: 1}
	}
	concurrency := settings.UltraConcurrency
	if concurrency < 1 {
		concurrency = DefaultSettings().UltraConcurrency
	}
	return DispatchPolicy{Tier: Ultra, Concurrency: concurrency, CrossReviewRequired: true, VerifierRequired: true}
}

// CheckCrossReviewProvider enforces provider independence when cross-review is required.
func CheckCrossReviewProvider(policy DispatchPolicy, workerProvider, reviewerProvider string) error {
	if !policy.CrossReviewRequired {
		return nil
	}
	workerProvider = strings.TrimSpace(workerProvider)
	reviewerProvider = strings.TrimSpace(reviewerProvider)
	if workerProvider == "" || reviewerProvider == "" || strings.EqualFold(workerProvider, reviewerProvider) {
		return errors.New("cross-review requires a different provider")
	}
	return nil
}
