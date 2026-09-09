// Package cloud holds MARSHAL's Community Cloud client.
//
// Standard MARSHAL does not need this package and does not use it. Everything
// here exists to answer one question for ULTRA: does this installation hold a
// verified, unexpired entitlement issued by the Community Cloud authority?
//
// The question is deliberately not "is ULTRA switched on locally". A local
// switch has a local answer, and a local answer can be edited by anyone who can
// edit the binary or its config. The authority for ULTRA is the server, and the
// evidence is a short-lived signed lease that carries material no client can
// invent.
package cloud

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrLeaseInvalid marks a lease that is malformed or self-inconsistent.
	ErrLeaseInvalid = errors.New("cloud: lease invalid")
	// ErrLeaseSignature marks a lease whose signature does not verify.
	ErrLeaseSignature = errors.New("cloud: lease signature")
	// ErrLeaseExpired marks a lease past its expiry.
	ErrLeaseExpired = errors.New("cloud: lease expired")
	// ErrLeaseBinding marks a lease issued to a different installation or
	// session than the one presenting it.
	ErrLeaseBinding = errors.New("cloud: lease binding")
	// ErrUnknownKey marks a lease signed by a key this client does not trust.
	ErrUnknownKey = errors.New("cloud: unknown signing key")
	// ErrNoLease marks the absence of any usable ULTRA authorization.
	ErrNoLease = errors.New("cloud: no verified lease")
)

// MaxLeaseLifetime bounds how long a single lease may be valid.
//
// Short lifetimes are what make revocation meaningful. A lease that lived for a
// day would mean a revoked entitlement kept working for a day, so the useful
// upper bound is the longest outage a user should keep ULTRA across, not the
// longest the cryptography would allow.
const MaxLeaseLifetime = 10 * time.Minute

// MaxClockSkew is the tolerance for a client clock ahead of the server's.
const MaxClockSkew = 2 * time.Minute

// Claims are the signed assertions the server makes about a lease.
type Claims struct {
	// JTI is a unique identifier minted by the server. It exists so a captured
	// lease can be recognised if it is presented twice.
	JTI string `json:"jti"`
	// KeyID names the signing key, so keys rotate without invalidating leases
	// that are still inside their window.
	KeyID string `json:"kid"`

	EntitlementID  string `json:"entitlement_id"`
	InstallationID string `json:"installation_id"`
	SessionID      string `json:"session_id"`

	// ClientVersion pins the MARSHAL version the lease was issued to, so a
	// version policy can refuse builds that must not run ULTRA.
	ClientVersion string `json:"client_version"`

	// Capabilities is the ULTRA capability set. It travels inside the signed
	// claims rather than being decided locally, because a locally decided
	// capability set is a boolean with extra steps.
	Capabilities []string `json:"capabilities"`

	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Bundle is the server-held ULTRA material.
//
// This is the part that cannot be forged into existence. A patched client can
// claim entitlement, but ULTRA needs the policy digest and routing table to
// actually run, and those are only ever produced by the server. That is the
// difference between a licence check and an authorization.
type Bundle struct {
	PolicyDigest string            `json:"policy_digest"`
	RoutingTable map[string]string `json:"routing_table"`
	// IssuedFor binds the bundle to an installation, so a bundle cannot be
	// lifted out of one lease and replayed inside another.
	IssuedFor string `json:"issued_for"`
}

// Lease is a signed grant of ULTRA capability for a bounded time.
type Lease struct {
	Claims    Claims `json:"claims"`
	Bundle    Bundle `json:"bundle"`
	Signature string `json:"signature"`
}

// Valid checks a lease's internal consistency against a reference time.
//
// This runs before any signature work as a cheap structural filter, and again
// conceptually after: a signature over incoherent claims is still incoherent.
func (c Claims) Valid(now time.Time) error {
	switch {
	case c.JTI == "":
		return fmt.Errorf("%w: missing identifier", ErrLeaseInvalid)
	case c.KeyID == "":
		return fmt.Errorf("%w: missing key id", ErrLeaseInvalid)
	case c.InstallationID == "":
		return fmt.Errorf("%w: missing installation", ErrLeaseInvalid)
	case c.SessionID == "":
		return fmt.Errorf("%w: missing session", ErrLeaseInvalid)
	case c.EntitlementID == "":
		return fmt.Errorf("%w: missing entitlement", ErrLeaseInvalid)
	case c.IssuedAt.IsZero() || c.ExpiresAt.IsZero():
		return fmt.Errorf("%w: missing validity window", ErrLeaseInvalid)
	case !c.ExpiresAt.After(c.IssuedAt):
		return fmt.Errorf("%w: expiry does not follow issuance", ErrLeaseInvalid)
	case c.ExpiresAt.Sub(c.IssuedAt) > MaxLeaseLifetime:
		return fmt.Errorf("%w: lifetime exceeds %s", ErrLeaseInvalid, MaxLeaseLifetime)
	}
	// A lease that claims to have been issued in the future is either a clock
	// problem or an attempt to widen the window, and neither should be honoured
	// beyond the skew allowance.
	if c.IssuedAt.After(now.Add(MaxClockSkew)) {
		return fmt.Errorf("%w: issued in the future", ErrLeaseInvalid)
	}
	return nil
}

// signingInput renders the bytes that are signed.
//
// Claims and bundle are signed together, so a bundle cannot be swapped for
// another server-issued one - the mix-and-match an attacker holding two
// entitlements would try.
//
// The encoding must match the authority byte for byte, and the authority is the
// server: this marshals the same anonymous struct it does. An independently
// "improved" encoding here - sorted keys, integer timestamps - produces bytes
// no server ever signed, so every genuine lease fails verification. Go emits
// struct fields in declaration order, so matching the field order in Claims and
// Bundle is what makes this deterministic, not any sorting done here.
func signingInput(c Claims, b Bundle) ([]byte, error) {
	input, err := json.Marshal(struct {
		Claims Claims `json:"claims"`
		Bundle Bundle `json:"bundle"`
	}{c, b})
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrLeaseInvalid, err)
	}
	return input, nil
}

// KeyRing holds the verification keys this client trusts.
//
// It holds more than one so rotation is not an outage: the new key starts
// signing while the previous key still verifies, and leases already in flight
// stay valid until they expire on their own.
type KeyRing struct {
	// mu guards keys. Rotation happens while other goroutines are verifying,
	// and an unguarded map read during a write is a crash rather than a
	// misverification.
	mu   sync.RWMutex
	keys map[string]ed25519.PublicKey
}

// NewKeyRing returns an empty ring.
func NewKeyRing() *KeyRing { return &KeyRing{keys: map[string]ed25519.PublicKey{}} }

// Add trusts a public key under a key id.
func (r *KeyRing) Add(keyID string, pub ed25519.PublicKey) error {
	if keyID == "" {
		return fmt.Errorf("%w: empty key id", ErrLeaseInvalid)
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: wrong public key size", ErrLeaseInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys[keyID] = pub
	return nil
}

// Remove retires a key, which is how a compromised key stops being honoured.
func (r *KeyRing) Remove(keyID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.keys, keyID)
}

// Len reports how many keys are trusted.
func (r *KeyRing) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.keys)
}

func (r *KeyRing) lookup(keyID string) (ed25519.PublicKey, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pub, ok := r.keys[keyID]
	return pub, ok
}

// VerifyInput is what a verifier knows beyond the lease itself.
type VerifyInput struct {
	// InstallationID and SessionID are this client's own identity. A lease
	// naming anything else is refused, which is the check that stops a lease
	// copied from another machine from working here.
	InstallationID string
	SessionID      string
	// Now is this client's clock. Expiry is an absolute timestamp, so moving
	// the clock backwards cannot extend a lease.
	Now time.Time
}

// VerifyLease checks a lease against a key ring and the presenting identity.
//
// Order matters. The signature is checked before any field is trusted, because
// until it verifies every field is attacker-controlled — including the key id
// used to select the key, which is why an unknown key id is a refusal rather
// than a fallback to some default.
func VerifyLease(l Lease, ring *KeyRing, in VerifyInput) error {
	if ring == nil || ring.Len() == 0 {
		return fmt.Errorf("%w: no verification keys", ErrUnknownKey)
	}
	pub, ok := ring.lookup(l.Claims.KeyID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKey, l.Claims.KeyID)
	}
	sig, err := base64.RawURLEncoding.DecodeString(l.Signature)
	if err != nil {
		return fmt.Errorf("%w: malformed encoding", ErrLeaseSignature)
	}
	input, err := signingInput(l.Claims, l.Bundle)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, input, sig) {
		return ErrLeaseSignature
	}

	// Everything below here is now known to be what the server said.
	if err := l.Claims.Valid(in.Now); err != nil {
		return err
	}
	if l.Claims.InstallationID != in.InstallationID {
		return fmt.Errorf("%w: lease belongs to another installation", ErrLeaseBinding)
	}
	if l.Claims.SessionID != in.SessionID {
		return fmt.Errorf("%w: lease belongs to another session", ErrLeaseBinding)
	}
	if l.Bundle.IssuedFor != l.Claims.InstallationID {
		return fmt.Errorf("%w: bundle belongs to another installation", ErrLeaseBinding)
	}
	if !in.Now.Before(l.Claims.ExpiresAt) {
		return ErrLeaseExpired
	}
	return nil
}

// HasCapability reports whether a lease grants a named capability.
//
// This deliberately does not verify. A helper that silently verified would make
// it easy to write a call site that reads a capability out of a lease nobody
// checked, and the compiler would not complain.
func (l Lease) HasCapability(name string) bool {
	for _, c := range l.Claims.Capabilities {
		if c == name {
			return true
		}
	}
	return false
}

// UsableBundle returns the ULTRA material, and fails when there is none.
//
// An empty bundle is what a fabricated lease produces. ULTRA cannot run without
// routing material, so this is the point at which a forged entitlement stops
// being useful even if every other check were somehow satisfied.
func (l Lease) UsableBundle() (Bundle, error) {
	if l.Bundle.PolicyDigest == "" || len(l.Bundle.RoutingTable) == 0 {
		return Bundle{}, fmt.Errorf("%w: lease carries no ULTRA material", ErrLeaseInvalid)
	}
	return l.Bundle, nil
}
