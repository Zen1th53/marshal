package cloud

import (
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/goalintake"
)

// CapabilityDelegation is the capability a lease must grant for ULTRA to
// delegate confirmation. Naming it explicitly means a lease can grant ULTRA
// without granting delegation, which is what makes a capability set more than
// decoration.
const CapabilityDelegation = "ultra.delegate"

// Gate is the single place MARSHAL decides whether ULTRA is authorized.
//
// There is exactly one of these per session and every entry path consults it —
// CLI, TUI, MCP and A2A alike. That is not tidiness: if each surface decided for
// itself, "every path is gated" would be a convention maintained by whoever
// remembered, and the first surface to forget would be a bypass nobody noticed.
//
// The gate answers from a held lease, never from configuration. A nil gate
// answers no, which is what makes Standard MARSHAL work with this package
// present but unconfigured.
type Gate struct {
	mu    sync.RWMutex
	lease *Lease
	ring  *KeyRing

	installationID string
	sessionID      string
	now            func() time.Time
}

// NewGate builds a gate bound to one installation and session.
func NewGate(installationID, sessionID string, ring *KeyRing, now func() time.Time) *Gate {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Gate{
		ring:           ring,
		installationID: installationID,
		sessionID:      sessionID,
		now:            now,
	}
}

// Adopt verifies a lease and, if it holds up, installs it.
//
// Verification happens here rather than at the call site so there is no path
// that stores an unverified lease and checks it later. A lease that fails is not
// retained at all, so a later caller cannot find it and assume somebody checked.
func (g *Gate) Adopt(l Lease) error {
	if g == nil {
		return ErrNoLease
	}
	if err := VerifyLease(l, g.ring, VerifyInput{
		InstallationID: g.installationID,
		SessionID:      g.sessionID,
		Now:            g.now(),
	}); err != nil {
		return err
	}
	// A lease that verifies but carries no ULTRA material is not a working
	// session. Refusing it here keeps the failure at the boundary instead of
	// surfacing later inside something that assumed routing data existed.
	if _, err := l.UsableBundle(); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lease = &l
	return nil
}

// Entitled reports whether a verified, unexpired lease is held right now.
//
// Expiry is re-checked on every call rather than captured at adoption. A lease
// that was valid six minutes ago is not a lease that is valid now, and a gate
// that answered from adoption-time state would keep ULTRA alive past its window.
func (g *Gate) Entitled() bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.entitledLocked()
}

// entitledLocked assumes the caller holds at least a read lock.
func (g *Gate) entitledLocked() bool {
	if g.lease == nil {
		return false
	}
	return g.now().Before(g.lease.Claims.ExpiresAt)
}

// Capability reports whether the held lease grants a named capability.
func (g *Gate) Capability(name string) bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.entitledLocked() {
		return false
	}
	return g.lease.HasCapability(name)
}

// Bundle returns the server-held ULTRA material.
//
// This is what an ULTRA session actually consumes, and it is why forcing
// Entitled to true in a patched build buys nothing: there is no local way to
// produce this, so the session has a boolean and nothing to run with it.
func (g *Gate) Bundle() (Bundle, error) {
	if g == nil {
		return Bundle{}, ErrNoLease
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.lease == nil {
		return Bundle{}, ErrNoLease
	}
	if !g.now().Before(g.lease.Claims.ExpiresAt) {
		return Bundle{}, ErrLeaseExpired
	}
	return g.lease.UsableBundle()
}

// Degrade drops the held lease, returning the session to Standard.
//
// Called on expiry, revocation, or a server that cannot be reached. It discards
// authorization and nothing else: losing contact with the Cloud must never cost
// somebody their work, so this is a change of mode, not an interruption.
func (g *Gate) Degrade() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lease = nil
}

// ExpiresAt reports when the held lease lapses.
func (g *Gate) ExpiresAt() (time.Time, bool) {
	if g == nil {
		return time.Time{}, false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.lease == nil {
		return time.Time{}, false
	}
	return g.lease.Claims.ExpiresAt, true
}

// RenewAt reports when renewal should be attempted.
//
// Renewal is scheduled at roughly half the remaining life rather than at expiry,
// so one failed attempt still leaves time for another before ULTRA drops. A
// client that renewed at the last moment would degrade on every brief hiccup.
func (g *Gate) RenewAt() (time.Time, bool) {
	if g == nil {
		return time.Time{}, false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.lease == nil {
		return time.Time{}, false
	}
	// The point is fixed by the lease's own window rather than by how much time
	// happens to remain when this is asked. Halving the remaining time on each
	// call would move the target every time it was consulted, so the moment
	// would recede and renewal would never actually come due.
	issued := g.lease.Claims.IssuedAt
	expiry := g.lease.Claims.ExpiresAt
	return issued.Add(expiry.Sub(issued) / 2), true
}

// Mode reports the operating mode this gate authorizes.
//
// A gate without a lease reports Standard. This is the function that decides
// what an unreachable server means, and it means Standard — never Ultra, because
// a failure that granted more capability than success would be the wrong
// failure direction for a control whose whole purpose is restraint.
func (g *Gate) Mode() goalintake.Mode {
	if g.Entitled() {
		return goalintake.ModeUltra
	}
	return goalintake.ModeStandard
}

// Policy builds the delegation policy for a Goal confirmation.
//
// The two fields answer different questions and come from different places, and
// keeping them apart is the point. Entitled is a fact only the server can
// establish. ExecutionEnabled is a preference the user sets locally: they may
// hold a valid entitlement and still want to be asked about everything.
//
// So patching the local preference to true does not grant ULTRA — it only
// expresses a wish about an entitlement the client does not have.
func (g *Gate) Policy(executionEnabled bool) goalintake.DelegationPolicy {
	return goalintake.DelegationPolicy{
		// Delegation is gated on the capability, not merely on holding a lease,
		// so the server can issue ULTRA that still asks the user each time.
		Entitled:         g.Capability(CapabilityDelegation),
		ExecutionEnabled: executionEnabled,
	}
}
