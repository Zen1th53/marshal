//go:build governed

package app

// attestationAcceptsStoredBinding: Default always re-verifies the live binding.
func attestationAcceptsStoredBinding(error) bool { return false }
