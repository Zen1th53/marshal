// Package testcloud mints real ULTRA leases for tests.
//
// It is not a bypass. The lease it produces is signed with a throwaway key that
// the returned gate trusts, and it is installed through cloud.Gate.Adopt, so it
// travels the same verification path a server-issued lease does. A test that
// needs an entitled session gets one the production code cannot distinguish
// from the real thing, and code that refuses an unentitled session still does.
package testcloud

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/cloud"
)

// Options tune the minted entitlement. The zero value is a valid, currently
// live lease granting delegation.
type Options struct {
	InstallationID string
	SessionID      string
	Capabilities   []string
	// Lifetime defaults to five minutes, which is inside cloud.MaxLeaseLifetime.
	Lifetime time.Duration
}

// EntitledGate returns a gate holding a verified, unexpired ULTRA lease.
func EntitledGate(t *testing.T, options Options) *cloud.Gate {
	t.Helper()

	if options.InstallationID == "" {
		options.InstallationID = "test-installation"
	}
	if options.SessionID == "" {
		options.SessionID = "test-session"
	}
	if options.Capabilities == nil {
		options.Capabilities = []string{cloud.CapabilityDelegation}
	}
	if options.Lifetime <= 0 {
		options.Lifetime = 5 * time.Minute
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test signing key: %v", err)
	}
	const keyID = "test-key"
	ring := cloud.NewKeyRing()
	if err := ring.Add(keyID, pub); err != nil {
		t.Fatalf("trust test signing key: %v", err)
	}

	now := time.Now().UTC()
	lease, err := cloud.SignLease(cloud.Claims{
		JTI:            "test-lease",
		KeyID:          keyID,
		EntitlementID:  "test-entitlement",
		InstallationID: options.InstallationID,
		SessionID:      options.SessionID,
		Capabilities:   options.Capabilities,
		IssuedAt:       now,
		ExpiresAt:      now.Add(options.Lifetime),
	}, cloud.Bundle{
		PolicyDigest: "test-policy-digest",
		RoutingTable: map[string]string{"default": "test-route"},
		IssuedFor:    options.InstallationID,
	}, priv)
	if err != nil {
		t.Fatalf("sign test lease: %v", err)
	}

	gate := cloud.NewGate(options.InstallationID, options.SessionID, ring, nil)
	if err := gate.Adopt(lease); err != nil {
		t.Fatalf("adopt test lease: %v", err)
	}
	if !gate.Entitled() {
		t.Fatal("test lease adopted but the gate is not entitled")
	}
	return gate
}
