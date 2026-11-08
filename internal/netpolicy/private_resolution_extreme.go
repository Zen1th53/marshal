//go:build !governed

package netpolicy

// The private edition never refuses a destination a run asks for: a host name
// that resolves to a LAN, loopback or other private address (a local package
// registry, a developer's database) is reached like any other, and the
// decision is recorded as usual. A worker's own localhost never goes through
// the proxy (sandbox NO_PROXY), so this reaches only host-side services.
func buildPrivateResolutionAllowed() bool { return true }
