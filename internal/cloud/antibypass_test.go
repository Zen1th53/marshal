package cloud

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/goalintake"
)

// These are the bypass attempts the design is meant to defeat, written as the
// attacker would attempt them rather than as unit tests of individual helpers.
// Each asserts the same end state: no delegation, and no usable ULTRA material.

// assertNoULTRA is the outcome every attack in this file must produce.
func assertNoULTRA(t *testing.T, g *Gate, attack string) {
	t.Helper()
	if g.Entitled() {
		t.Fatalf("%s: gate reported entitlement", attack)
	}
	if g.Mode() != goalintake.ModeStandard {
		t.Fatalf("%s: gate did not report Standard", attack)
	}
	if _, err := g.Bundle(); err == nil {
		t.Fatalf("%s: ULTRA material was available", attack)
	}
	// The decision that actually governs behaviour must refuse to delegate,
	// even with the local preference switched on.
	if goalintake.Confirm(goalintake.Intake{}, g.Mode(), g.Policy(true)).Delegated {
		t.Fatalf("%s: confirmation was delegated", attack)
	}
}

// Attack: patch the local configuration to claim ULTRA. This is the flag the
// pack forbids relying on, and it must achieve nothing on its own.
func TestBypassLocalFlagAchievesNothing(t *testing.T) {
	now := time.Now().UTC()
	_, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	// Every local switch turned on, no lease held.
	cfg := Config{Endpoint: "https://marshal.blackhat.uz", ExecutionEnabled: true}
	if !cfg.Enabled() || !cfg.ExecutionEnabled {
		t.Fatal("test setup did not enable the local preference")
	}
	assertNoULTRA(t, g, "local flag patched on")
}

// Attack: hand-build a lease locally, as a patched client would.
func TestBypassFabricatedLease(t *testing.T) {
	now := time.Now().UTC()
	_, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	fabricated := Lease{
		Claims: Claims{
			JTI: "jti-fake", KeyID: "k1", EntitlementID: "ent-fake",
			InstallationID: testInstallation, SessionID: testSession,
			ClientVersion: "1.0.0", Capabilities: []string{CapabilityDelegation},
			IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute),
		},
		Bundle:    validBundle(),
		Signature: base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)),
	}
	if err := g.Adopt(fabricated); err == nil {
		t.Fatal("a fabricated lease was adopted")
	}
	assertNoULTRA(t, g, "fabricated lease")
}

// Attack: generate a keypair, sign a lease with it, and present it. This is the
// strongest purely local forgery available, and it fails on the key ring.
func TestBypassSelfSignedLease(t *testing.T) {
	now := time.Now().UTC()
	_, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	attacker, _ := newSigner(t, "k1") // same key id, attacker's key
	if err := g.Adopt(attacker.issue(t, validClaims(now), validBundle())); err == nil {
		t.Fatal("a self-signed lease was adopted")
	}
	assertNoULTRA(t, g, "self-signed lease")
}

// Attack: copy a genuine lease from another machine.
func TestBypassCopiedLease(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")

	// A genuine lease, issued to somebody else.
	victim := "inst-cccccccccccccccccccccccccccccccc"
	claims := validClaims(now)
	claims.InstallationID = victim
	bundle := validBundle()
	bundle.IssuedFor = victim
	stolen := s.issue(t, claims, bundle)

	g := gateAt(ring, &now)
	if err := g.Adopt(stolen); err == nil {
		t.Fatal("a lease copied from another installation was adopted")
	}
	assertNoULTRA(t, g, "copied lease")
}

// Attack: hold a real lease, then wind the clock back to keep it alive past
// its expiry.
func TestBypassClockRollback(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	if err := g.Adopt(s.issue(t, validClaims(now), validBundle())); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	expiry, _ := g.ExpiresAt()

	// Time passes; the lease dies.
	now = expiry.Add(time.Second)
	assertNoULTRA(t, g, "expired lease")

	// The attacker winds the clock back to well before issuance.
	now = expiry.Add(-24 * time.Hour)
	if g.Entitled() {
		t.Fatal("clock rollback revived a dropped lease")
	}
	assertNoULTRA(t, g, "clock rolled back after expiry")

	// Re-presenting the same lease under the rolled-back clock must also fail.
	// The gate judges time by a high-water mark, so time only moves forward as
	// far as it is concerned, and a lease that lapsed once stays lapsed.
	replay := s.issue(t, validClaims(expiry.Add(-25*time.Hour)), validBundle())
	if err := g.Adopt(replay); err == nil {
		t.Fatal("a stale lease was adopted under a rolled-back clock")
	}
	assertNoULTRA(t, g, "stale lease replayed under a rolled-back clock")
}

// Attack: keep replaying the same captured lease to extend a session forever.
// The lease is bounded, so replay buys only the window that was already granted.
func TestBypassReplayIsBounded(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	captured := s.issue(t, validClaims(now), validBundle())
	if err := g.Adopt(captured); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	now = now.Add(MaxLeaseLifetime + time.Minute)
	// Replaying the same lease after its window must not restore ULTRA.
	if err := g.Adopt(captured); err == nil {
		t.Fatal("an expired lease was re-adopted")
	}
	assertNoULTRA(t, g, "replayed expired lease")
}

// Attack: take a real lease and edit the capabilities to grant more.
func TestBypassCapabilityEscalation(t *testing.T) {
	now := time.Now().UTC()
	s, ring := newSigner(t, "k1")
	g := gateAt(ring, &now)

	// A lease that grants ULTRA but not delegation.
	claims := validClaims(now)
	claims.Capabilities = []string{"ultra.readonly"}
	lease := s.issue(t, claims, validBundle())

	// The attacker adds the capability they want.
	lease.Claims.Capabilities = append(lease.Claims.Capabilities, CapabilityDelegation)
	if err := g.Adopt(lease); err == nil {
		t.Fatal("an escalated lease was adopted")
	}
	assertNoULTRA(t, g, "capability escalation")

	// And the unmodified lease, while valid, still does not delegate.
	clean := s.issue(t, claims, validBundle())
	if err := g.Adopt(clean); err != nil {
		t.Fatalf("clean lease: %v", err)
	}
	if g.Policy(true).Entitled {
		t.Fatal("a lease without the delegation capability delegated anyway")
	}
}

// Attack: the server goes away mid-session and the client keeps ULTRA running
// on the strength of never being told otherwise.
func TestBypassServerOutageDoesNotExtend(t *testing.T) {
	f := newFakeServer(t)
	ctx := context.Background()
	client := f.client(t)

	ring, err := client.FetchKeys(ctx)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	state, _ := NewInstallation()
	sessionID, _ := NewSessionID()

	clock := time.Now().UTC()
	gate := NewGate(state.InstallationID, sessionID, ring, func() time.Time { return clock })
	if err := NewSession(client, gate, state, sessionID).Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !gate.Entitled() {
		t.Fatal("no entitlement after a successful start")
	}

	// The server disappears entirely, and time passes.
	f.server.Close()
	expiry, _ := gate.ExpiresAt()
	clock = expiry.Add(time.Second)

	assertNoULTRA(t, gate, "server outage past expiry")
}

// Attack: point the client at an endpoint the attacker controls, which answers
// every call plausibly. Without the real signing key it cannot mint a lease
// this client will accept.
func TestBypassServerImpersonation(t *testing.T) {
	realServer := newFakeServer(t)
	impostor := newFakeServer(t)
	ctx := context.Background()

	realRing, err := realServer.client(t).FetchKeys(ctx)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	state, _ := NewInstallation()
	sessionID, _ := NewSessionID()

	lease, err := impostor.client(t).StartSession(ctx, state, sessionID)
	if err != nil {
		t.Fatalf("impostor session: %v", err)
	}
	// The impostor answered everything correctly; only the signature betrays it.
	if lease.Claims.InstallationID != state.InstallationID {
		t.Fatal("test setup: the impostor did not mint a matching lease")
	}

	gate := NewGate(state.InstallationID, sessionID, realRing, nil)
	if err := gate.Adopt(lease); err == nil {
		t.Fatal("a lease from an impersonating endpoint was adopted")
	}
	assertNoULTRA(t, gate, "server impersonation")
}

// Attack: strip the client's trusted keys, hoping an empty ring means
// "trust anything" rather than "trust nothing".
func TestBypassEmptyKeyRingFailsClosed(t *testing.T) {
	now := time.Now().UTC()
	s, _ := newSigner(t, "k1")
	g := NewGate(testInstallation, testSession, NewKeyRing(), func() time.Time { return now })

	if err := g.Adopt(s.issue(t, validClaims(now), validBundle())); err == nil {
		t.Fatal("an empty key ring accepted a lease")
	}
	assertNoULTRA(t, g, "empty key ring")
}

// Attack: reach ULTRA without configuring the Cloud at all, hoping the absent
// client defaults to permitting rather than refusing.
func TestBypassUnconfiguredCloudFailsClosed(t *testing.T) {
	result := Authorize(context.Background(), Config{}, t.TempDir(), "1.0.0")
	if result.Gate != nil {
		t.Fatal("an unconfigured Cloud produced a gate")
	}
	assertNoULTRA(t, result.Gate, "unconfigured cloud")

	// And with the preference on but no endpoint, still nothing.
	result = Authorize(context.Background(), Config{ExecutionEnabled: true}, t.TempDir(), "1.0.0")
	assertNoULTRA(t, result.Gate, "unconfigured cloud with preference on")
}

// Attack: point at a real endpoint that cannot be reached, hoping failure is
// treated as success.
func TestBypassUnreachableCloudFailsClosed(t *testing.T) {
	cfg := Config{
		// A port nothing is listening on, over loopback so no DNS is involved.
		Endpoint:         "http://127.0.0.1:1",
		ExecutionEnabled: true,
	}
	result := Authorize(context.Background(), cfg, t.TempDir(), "1.0.0")
	if result.Err == nil {
		t.Fatal("an unreachable Cloud reported success")
	}
	assertNoULTRA(t, result.Gate, "unreachable cloud")
}

// A lease minted by a key that was rotated out must stop working, which is how
// a compromised signing key is contained.
func TestBypassRetiredKey(t *testing.T) {
	now := time.Now().UTC()
	compromised, ring := newSigner(t, "k-compromised")

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := ring.Add("k-current", pub); err != nil {
		t.Fatalf("add: %v", err)
	}
	_ = priv

	// The compromised key is retired.
	ring.Remove("k-compromised")

	g := gateAt(ring, &now)
	if err := g.Adopt(compromised.issue(t, validClaims(now), validBundle())); err == nil {
		t.Fatal("a lease from a retired key was adopted")
	}
	assertNoULTRA(t, g, "retired signing key")
}
