//go:build governed

package app

import "github.com/Zen1th53/marshal/internal/verification"

// Community has no runtime proof output artifacts; keep its export unchanged.
func addRuntimeCheckOutputs(map[string][]byte, *verification.RuntimeProof) error { return nil }
