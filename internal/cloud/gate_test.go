package cloud

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/goalintake"
)

const (
	testInstallation = "inst-00112233445566778899aabbccddeeff"
	testSession      = "sess-ffeeddccbbaa99887766554433221100"
)

// signer mints leases for tests. The production client never signs anything —
// only the server does — so this exists solely to produce inputs.
type signer struct {
	keyID string
	priv  ed25519.PrivateKey
}

func newSigner(t *testing.T, keyID string) (*signer, *KeyRing) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	ring := NewKeyRing()
	if err := ring.Add(keyID, pub); err != nil {
		t.Fatalf("add key: %v", err)
	}
	return &signer{keyID: keyID, priv: priv}, ring
}

func (s *signer) issue(t *testing.T, c Claims, b Bundle) Lease {
	t.Helper()
	c.KeyID = s.keyID
	if c.JTI == "" {
		c.JTI = "jti-test"
	}
	input, err := signingInput(c, b)
	if err != nil {
		t.Fatalf("signing input: %v", err)
	}
	return Lease{
		Claims:    c,
		Bundle:    b,
		Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.priv, input)),
	}
}

// validClaims is a lease that should pass every check.
func validClaims(now time.Time) Claims {
	return Claims{
		EntitlementID:  "ent-1",
		InstallationID: testInstallation,
		SessionID:      testSession,
		ClientVersion:  "1.0.0",
		Capabilities:   []string{CapabilityDelegation},
		IssuedAt:       now,
		ExpiresAt:      now.Add(5 * time.Minute),
	}
}

func validBundle() Bundle {
	return Bundle{
		PolicyDigest: "sha256:abc",
		RoutingTable: map[string]string{"primary": "https://marshal.blackhat.uz"},
		IssuedFor:    testInstallation,
	}
}

// gateAt builds a gate whose clock the test controls.
func gateAt(ring *KeyRing, now *time.Time) *Gate {
	return NewGate(testInstallation, testSession, ring, func() time.Time { return *now })
}

// A gate with nothing adopted is the state every offline Standard session is
// in, so refusing must be the default rather than an error condition.
func TestGateStartsUnentitled(t *testing.T) {
	now := time.Now().UTC()
	_, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	if g.Entitled() {
		t.Fatal("a fresh gate reported entitlement")
	}
	if g.Capability(CapabilityDelegation) {
		t.Fatal("a fresh gate granted a capability")
	}
	if _, err := g.Bundle(); !errors.Is(err, ErrNoLease) {
		t.Fatalf("want ErrNoLease, got %v", err)
	}
	if g.Mode() != goalintake.ModeStandard {
		t.Fatalf("want Standard from an unentitled gate, got %v", g.Mode())
	}
}

// A nil gate must behave exactly like an unentitled one. This is what lets every
// call site skip a nil check, and a missing nil check is how an offline path
// would otherwise panic instead of degrading.
func TestNilGateIsUnentitled(t *testing.T) {
	var g *Gate
	if g.Entitled() || g.Capability(CapabilityDelegation) {
		t.Fatal("a nil gate granted something")
	}
	if g.Mode() != goalintake.ModeStandard {
		t.Fatal("a nil gate did not report Standard")
	}
	if _, err := g.Bundle(); !errors.Is(err, ErrNoLease) {
		t.Fatalf("want ErrNoLease from a nil gate, got %v", err)
	}
	if _, ok := g.ExpiresAt(); ok {
		t.Fatal("a nil gate reported an expiry")
	}
	if _, ok := g.RenewAt(); ok {
		t.Fatal("a nil gate proposed a renewal")
	}
	policy := g.Policy(true)
	if policy.Entitled {
		t.Fatal("a nil gate produced an entitled policy")
	}
	g.Degrade() // must not panic
	if err := g.Adopt(Lease{}); !errors.Is(err, ErrNoLease) {
		t.Fatalf("want ErrNoLease adopting into a nil gate, got %v", err)
	}
}

func TestGateAdoptsValidLease(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	if err := g.Adopt(s.issue(t, validClaims(now), validBundle())); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if !g.Entitled() {
		t.Fatal("a verified lease did not produce entitlement")
	}
	if g.Mode() != goalintake.ModeUltra {
		t.Fatal("an entitled gate did not report Ultra")
	}
	b, err := g.Bundle()
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if b.PolicyDigest != "sha256:abc" {
		t.Fatalf("bundle did not survive adoption: %+v", b)
	}
}

// The central anti-bypass property: a lease this client did not have a key for
// is refused, so a locally fabricated one grants nothing however well-formed.
func TestGateRefusesForgedLease(t *testing.T) {
	now := time.Now().UTC()
	_, ring := newSigner(t, "k1")
	// The attacker signs with their own key but claims the trusted key's id.
	attacker, _ := newSigner(t, "k1")
	g := gateAt(ring, &now)

	forged := attacker.issue(t, validClaims(now), validBundle())
	if err := g.Adopt(forged); !errors.Is(err, ErrLeaseSignature) {
		t.Fatalf("want ErrLeaseSignature, got %v", err)
	}
	if g.Entitled() {
		t.Fatal("a refused lease still produced entitlement")
	}
}

// A lease signed by a key id the ring does not know is refused rather than
// falling back to any other key.
func TestGateRefusesUnknownKey(t *testing.T) {
	now := time.Now().UTC()
	_, ring := newSigner(t, "k1")
	other, _ := newSigner(t, "k-unknown")
	g := gateAt(ring, &now)

	if err := g.Adopt(other.issue(t, validClaims(now), validBundle())); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("want ErrUnknownKey, got %v", err)
	}
}

// Tampering with a signed claim must invalidate the signature. Without this a
// lease could be edited in flight to widen its capabilities.
func TestGateRefusesTamperedClaims(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	lease := s.issue(t, validClaims(now), validBundle())
	lease.Claims.Capabilities = append(lease.Claims.Capabilities, "ultra.everything")
	if err := g.Adopt(lease); !errors.Is(err, ErrLeaseSignature) {
		t.Fatalf("want ErrLeaseSignature after tampering, got %v", err)
	}
}

// The bundle is signed alongside the claims, so swapping one server-issued
// bundle for another is caught. An attacker holding two entitlements cannot
// mix the weaker lease with the stronger bundle.
func TestGateRefusesSwappedBundle(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	lease := s.issue(t, validClaims(now), validBundle())
	lease.Bundle.RoutingTable = map[string]string{"primary": "https://elsewhere"}
	if err := g.Adopt(lease); !errors.Is(err, ErrLeaseSignature) {
		t.Fatalf("want ErrLeaseSignature after bundle swap, got %v", err)
	}
}

// A genuinely signed lease minted for another machine must not work here.
func TestGateRefusesLeaseForAnotherInstallation(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	claims := validClaims(now)
	claims.InstallationID = "inst-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bundle := validBundle()
	bundle.IssuedFor = claims.InstallationID

	if err := g.Adopt(s.issue(t, claims, bundle)); !errors.Is(err, ErrLeaseBinding) {
		t.Fatalf("want ErrLeaseBinding, got %v", err)
	}
}

// A lease for a different session on the same machine is also refused, so one
// session's entitlement cannot be borrowed by another.
func TestGateRefusesLeaseForAnotherSession(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	claims := validClaims(now)
	claims.SessionID = "sess-1111111111111111"
	if err := g.Adopt(s.issue(t, claims, validBundle())); !errors.Is(err, ErrLeaseBinding) {
		t.Fatalf("want ErrLeaseBinding, got %v", err)
	}
}

// A bundle bound to a different installation than the claims is refused even
// when both are individually well-formed.
func TestGateRefusesMismatchedBundleBinding(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	bundle := validBundle()
	bundle.IssuedFor = "inst-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := g.Adopt(s.issue(t, validClaims(now), bundle)); !errors.Is(err, ErrLeaseBinding) {
		t.Fatalf("want ErrLeaseBinding, got %v", err)
	}
}

// This is the case that makes forging pointless. Even a perfectly signed lease
// is useless without server-held ULTRA material, and a client cannot invent it.
func TestGateRefusesLeaseWithoutULTRAMaterial(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	empty := Bundle{IssuedFor: testInstallation}
	if err := g.Adopt(s.issue(t, validClaims(now), empty)); !errors.Is(err, ErrLeaseInvalid) {
		t.Fatalf("want ErrLeaseInvalid for an empty bundle, got %v", err)
	}
	if g.Entitled() {
		t.Fatal("gate became entitled from a lease with no ULTRA material")
	}
}

// A lifetime beyond the maximum is refused, so a compromised server key cannot
// mint a lease that outlives revocation by days.
func TestGateRefusesOverlongLease(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	claims := validClaims(now)
	claims.ExpiresAt = now.Add(MaxLeaseLifetime + time.Minute)
	if err := g.Adopt(s.issue(t, claims, validBundle())); !errors.Is(err, ErrLeaseInvalid) {
		t.Fatalf("want ErrLeaseInvalid for an overlong lease, got %v", err)
	}
}

// Entitlement lapses on its own, without anything being called to expire it.
func TestGateExpiresOnItsOwn(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	if err := g.Adopt(s.issue(t, validClaims(now), validBundle())); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	now = now.Add(6 * time.Minute)

	if g.Entitled() {
		t.Fatal("entitlement survived expiry")
	}
	if g.Capability(CapabilityDelegation) {
		t.Fatal("capability survived expiry")
	}
	if g.Mode() != goalintake.ModeStandard {
		t.Fatal("an expired gate did not fall back to Standard")
	}
	if _, err := g.Bundle(); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("want ErrLeaseExpired, got %v", err)
	}
}

// Winding the clock back must not extend a lease: expiry is an absolute
// timestamp, not a countdown that can be reset.
func TestClockRollbackDoesNotExtendEntitlement(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	if err := g.Adopt(s.issue(t, validClaims(now), validBundle())); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	expiry, ok := g.ExpiresAt()
	if !ok {
		t.Fatal("no expiry on an adopted lease")
	}

	now = now.Add(-24 * time.Hour)
	if got, _ := g.ExpiresAt(); !got.Equal(expiry) {
		t.Fatal("expiry moved with the clock; it must be absolute")
	}

	now = expiry.Add(time.Second)
	if g.Entitled() {
		t.Fatal("entitlement survived expiry after a clock rollback")
	}
}

// A lease claiming to be issued far in the future is refused, so a client whose
// clock is pushed forward cannot accept a lease minted for that future.
func TestGateRefusesFutureIssuedLease(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	claims := validClaims(now.Add(MaxClockSkew + time.Minute))
	if err := g.Adopt(s.issue(t, claims, validBundle())); !errors.Is(err, ErrLeaseInvalid) {
		t.Fatalf("want ErrLeaseInvalid for a future lease, got %v", err)
	}
}

func TestDegradeReturnsToStandard(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	if err := g.Adopt(s.issue(t, validClaims(now), validBundle())); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	g.Degrade()

	if g.Entitled() || g.Mode() != goalintake.ModeStandard {
		t.Fatal("Degrade did not return the gate to Standard")
	}
}

// Delegation is gated on the capability, not merely on holding a lease, so the
// server can issue ULTRA that still asks the user about everything.
func TestPolicyRequiresDelegationCapability(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	claims := validClaims(now)
	claims.Capabilities = []string{"ultra.something.else"}
	if err := g.Adopt(s.issue(t, claims, validBundle())); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	if !g.Entitled() {
		t.Fatal("the lease should still be a valid ULTRA lease")
	}
	if g.Policy(true).Entitled {
		t.Fatal("a lease without the delegation capability produced a delegating policy")
	}
}

// The user's local preference is not an authority. This is the "patched local
// flag" case: setting Execution true without a lease must change nothing.
func TestExecutionPreferenceIsNotAuthority(t *testing.T) {
	now := time.Now().UTC()
	_, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	policy := g.Policy(true)
	if policy.Entitled {
		t.Fatal("a local preference produced entitlement")
	}
	if !policy.ExecutionEnabled {
		t.Fatal("the preference was not carried through")
	}

	// And the decision that consumes it must refuse to delegate.
	decision := goalintake.Confirm(goalintake.Intake{}, g.Mode(), policy)
	if decision.Delegated {
		t.Fatal("an unentitled session delegated confirmation")
	}
}

// Renewal is scheduled with slack, so one failed attempt does not immediately
// cost the session its ULTRA mode.
func TestRenewAtLeavesRoomForRetry(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	if _, ok := g.RenewAt(); ok {
		t.Fatal("a gate with no lease proposed a renewal time")
	}
	if err := g.Adopt(s.issue(t, validClaims(now), validBundle())); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	at, ok := g.RenewAt()
	if !ok {
		t.Fatal("no renewal time for an adopted lease")
	}
	expiry, _ := g.ExpiresAt()
	if !at.Before(expiry) {
		t.Fatal("renewal is not scheduled before expiry")
	}
	if slack := expiry.Sub(at); slack < 2*time.Minute {
		t.Fatalf("renewal slack %v leaves no room to retry", slack)
	}
}

// The renewal point must be a fixed moment in the lease's window, not a
// function of how much time is left when it is asked. An earlier version halved
// the remaining time on every call, so the target receded whenever it was
// consulted and renewal never actually came due: a long session would expire
// rather than renew.
func TestRenewAtDoesNotRecede(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	if err := g.Adopt(s.issue(t, validClaims(now), validBundle())); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	first, ok := g.RenewAt()
	if !ok {
		t.Fatal("no renewal time")
	}

	// Advancing the clock must not move the target.
	for _, step := range []time.Duration{time.Minute, time.Minute, 2 * time.Minute} {
		now = now.Add(step)
		again, ok := g.RenewAt()
		if !ok {
			t.Fatal("renewal time disappeared")
		}
		if !again.Equal(first) {
			t.Fatalf("renewal moved from %v to %v after advancing %v", first, again, step)
		}
	}

	// And it must actually come due before the lease expires.
	expiry, _ := g.ExpiresAt()
	if !first.Before(expiry) {
		t.Fatal("renewal is not scheduled before expiry")
	}
	if !now.After(first) {
		t.Fatal("the clock advanced past renewal but the target was still ahead")
	}
}

// Rotation must not invalidate a lease that is still inside its window: the
// outgoing key keeps verifying while the incoming key starts signing.
func TestKeyRotationOverlap(t *testing.T) {
	now := time.Now().UTC()
	oldSigner, ring := newSigner(t, "k-old")

	newPub, newPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := ring.Add("k-new", newPub); err != nil {
		t.Fatalf("add: %v", err)
	}
	newSigner := &signer{keyID: "k-new", priv: newPriv}

	for _, s := range []*signer{oldSigner, newSigner} {
		g := gateAt(ring, &now)
		if err := g.Adopt(s.issue(t, validClaims(now), validBundle())); err != nil {
			t.Fatalf("lease from %s refused during overlap: %v", s.keyID, err)
		}
	}

	// Retiring the old key stops it being honoured.
	ring.Remove("k-old")
	g := gateAt(ring, &now)
	if err := g.Adopt(oldSigner.issue(t, validClaims(now), validBundle())); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a retired key was still honoured: %v", err)
	}
}

// An empty ring must refuse everything rather than defaulting to trust.
func TestEmptyKeyRingRefusesEverything(t *testing.T) {
	now := time.Now().UTC()
	s, _ := newSigner(t, "k1")
	g := NewGate(testInstallation, testSession, NewKeyRing(), func() time.Time { return now })

	if err := g.Adopt(s.issue(t, validClaims(now), validBundle())); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("want ErrUnknownKey from an empty ring, got %v", err)
	}
}

// Signing input must not depend on Go's map iteration order, or a lease would
// verify or fail depending on the run.
func TestSigningInputIsDeterministic(t *testing.T) {
	now := time.Now().UTC()
	bundle := Bundle{
		PolicyDigest: "sha256:abc",
		IssuedFor:    testInstallation,
		RoutingTable: map[string]string{
			"primary": "https://a", "secondary": "https://b",
			"tertiary": "https://c", "quaternary": "https://d",
		},
	}
	claims := validClaims(now)
	claims.Capabilities = []string{"z.cap", "a.cap", "m.cap"}

	first, err := signingInput(claims, bundle)
	if err != nil {
		t.Fatalf("signing input: %v", err)
	}
	for i := 0; i < 200; i++ {
		again, err := signingInput(claims, bundle)
		if err != nil {
			t.Fatalf("signing input: %v", err)
		}
		if string(again) != string(first) {
			t.Fatal("signing input is not deterministic across runs")
		}
	}
}

// Concurrent use must be safe: the TUI, CLI and lease maintenance all touch one
// gate, and rotation happens while verification is in flight.
func TestGateConcurrentUse(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)
	lease := s.issue(t, validClaims(now), validBundle())

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 200; j++ {
				_ = g.Adopt(lease)
				_ = g.Entitled()
				_ = g.Capability(CapabilityDelegation)
				_, _ = g.Bundle()
				_ = g.Mode()
				_ = g.Policy(true)
				g.Degrade()
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
