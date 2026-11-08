//go:build governed

package netpolicy

// Community refuses a host name that resolves to a private, loopback,
// link-local or metadata address (DNS rebinding / SSRF guard).
func buildPrivateResolutionAllowed() bool { return false }
