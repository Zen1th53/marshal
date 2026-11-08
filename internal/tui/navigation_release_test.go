package tui

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
)

// This file is item 1's in-process release/entitlement entry-point contract.
//
// It drives the real Workspace the way Ctrl+N and the exported OpenNavigation
// (the path both Esc-on-an-empty-composer and /menu route through) do, across
// every release and entitlement state, and asserts that the gate is read live
// rather than captured at startup.
//
// The PTY half of this contract lives in navigation_release_pty_test.go, which
// runs the compiled binary and cannot be shortcut with TestMain's in-process
// navigationReleased=true assignment.

// navigationExpectedRefusal returns the category of refusal a session must
// produce for a release/entitlement combination. "" means open; "not available
// yet" is the global release hold; "ULTRA" is the entitlement refusal.
func navigationExpectedRefusal(extreme, released bool, ent string) string {
	if extreme {
		// The Marshal opens navigation for every session: it is the
		// development/qualification build that bypasses both the release hold
		// and the entitlement gate.
		return ""
	}
	if !released {
		// The global release hold is stronger than entitlement: everyone is
		// refused, including a verified ULTRA session, until the surface is
		// released.
		return "not available yet"
	}
	switch ent {
	case "verified":
		return ""
	default: // standard, expired, revoked
		return "ULTRA"
	}
}

// navigationExpiredGate holds a lease that has already lapsed. It is built with
// a mutable clock so the lease verifies on adoption and then expires, which is
// what a live expiry (not a refused adoption) actually looks like.
func navigationExpiredGate(t *testing.T) *cloud.Gate {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate expired-gate signing key: %v", err)
	}
	const keyID, inst, sess = "k-nav-expired", "inst-nav-expired", "sess-nav-expired"
	ring := cloud.NewKeyRing()
	if err := ring.Add(keyID, pub); err != nil {
		t.Fatalf("trust expired-gate key: %v", err)
	}
	issued := time.Now().UTC()
	lease := testcloud.IssueLease(t, cloud.Claims{
		JTI:            "jti-nav-expired",
		KeyID:          keyID,
		EntitlementID:  "ent-nav-expired",
		InstallationID: inst,
		SessionID:      sess,
		Capabilities:   []string{cloud.CapabilityDelegation},
		IssuedAt:       issued,
		ExpiresAt:      issued.Add(2 * time.Minute),
	}, cloud.Bundle{
		PolicyDigest: "digest-nav-expired",
		RoutingTable: map[string]string{"default": "route-nav-expired"},
		IssuedFor:    inst,
	}, priv)
	clock := issued
	gate := cloud.NewGate(inst, sess, ring, func() time.Time { return clock })
	if err := gate.Adopt(lease); err != nil {
		t.Fatalf("adopt expiring lease: %v", err)
	}
	clock = issued.Add(5 * time.Minute) // now past the 2-minute window
	return gate
}

// TestNavigationReleaseEntryPoints is the definitive in-process gate. It covers
// both release states, both entry points, and Standard / verified / expired /
// revoked leases, and it runs identically under the standard and extreme
// editions (the expected behaviour differs per edition).
func TestNavigationReleaseEntryPoints(t *testing.T) {
	states := []struct {
		name  string
		setup func(*testing.T, *Workspace)
	}{
		{"standard", func(t *testing.T, ws *Workspace) { ws.AttachULTRA(nil, false) }},
		{"verified", func(t *testing.T, ws *Workspace) {
			ws.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), true)
		}},
		{"expired", func(t *testing.T, ws *Workspace) { ws.AttachULTRA(navigationExpiredGate(t), true) }},
		{"revoked", func(t *testing.T, ws *Workspace) {
			g := testcloud.EntitledGate(t, testcloud.Options{})
			g.Degrade()
			ws.AttachULTRA(g, true)
		}},
	}

	for _, released := range []bool{false, true} {
		for _, st := range states {
			name := fmt.Sprintf("released=%v/ent=%s", released, st.name)
			t.Run(name, func(t *testing.T) {
				want := navigationExpectedRefusal(editionExtreme, released, st.name)

				t.Run("ctrlN", func(t *testing.T) {
					ws := NewWorkspace(nil, "proj", "sess-cn")
					ws.navReleased = released
					st.setup(t, ws)
					assertRefusalCategory(t, ws.navigationRefusal(), want)

					if !ws.dispatchNavigationKey(context.Background(), KeyEvent{Type: KeyCtrlN}) {
						t.Fatal("Ctrl+N did not consume the key")
					}
					assertEntryPointOpened(t, ws, want)
					if want != "" && ws.state.LastOutputIsError {
						t.Errorf("the refusal is a mode, not an error: %q", ws.state.LastOutput)
					}
				})

				t.Run("openNavigation", func(t *testing.T) {
					ws := NewWorkspace(nil, "proj", "sess-on")
					ws.navReleased = released
					st.setup(t, ws)
					assertRefusalCategory(t, ws.navigationRefusal(), want)

					if got := ws.OpenNavigation(context.Background()); got != (want == "") {
						t.Fatalf("OpenNavigation returned %v, want %v", got, want == "")
					}
					assertEntryPointOpened(t, ws, want)
				})
			})
		}
	}
}

// assertRefusalCategory checks a refusal string against the expected category.
func assertRefusalCategory(t *testing.T, got, want string) {
	t.Helper()
	switch want {
	case "":
		if got != "" {
			t.Fatalf("refusal = %q, want empty (open)", got)
		}
	case "not available yet":
		if !strings.Contains(got, "not available yet") {
			t.Fatalf("refusal = %q, want the global release hold", got)
		}
	case "ULTRA":
		if !strings.Contains(got, "ULTRA") || !strings.Contains(got, "Standard") {
			t.Fatalf("refusal = %q, want the entitlement refusal", got)
		}
	}
}

// assertEntryPointOpened checks that the entry point did (or did not) open the
// navigation view.
func assertEntryPointOpened(t *testing.T, ws *Workspace, want string) {
	t.Helper()
	if want == "" {
		if !ws.navView.IsOpen() {
			t.Fatal("entry point did not open navigation")
		}
		return
	}
	if ws.navView.IsOpen() {
		t.Fatalf("entry point opened navigation despite the %q refusal", want)
	}
}

// TestNavigationEntitlementReadLive proves the gate is consulted on every
// attempt rather than captured at startup: a session that acquires ULTRA
// mid-run opens navigation without a restart, and one that loses it is refused
// again immediately. The Marshal bypasses the gate, so there is nothing
// live to observe there.
func TestNavigationEntitlementReadLive(t *testing.T) {
	if editionExtreme {
		ws := NewWorkspace(nil, "proj", "sess-live-extreme")
		ws.navReleased = false
		if got := ws.navigationRefusal(); got != "" {
			t.Fatalf("extreme live refusal = %q, want empty", got)
		}
		return
	}

	ws := NewWorkspace(nil, "proj", "sess-live")
	ws.navReleased = true
	ctx := context.Background()

	// Standard: refused.
	if ws.navigationEntitled() {
		t.Fatal("a nil gate reported entitled")
	}
	if !ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyCtrlN}) {
		t.Fatal("Ctrl+N was not consumed")
	}
	if ws.navView.IsOpen() {
		t.Fatal("a Standard session opened navigation")
	}

	// Acquire entitlement mid-session: opens without restarting.
	gate := testcloud.EntitledGate(t, testcloud.Options{})
	ws.AttachULTRA(gate, true)
	if !ws.navigationEntitled() {
		t.Fatal("did not become entitled after attaching a verified lease")
	}
	if !ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyCtrlN}) {
		t.Fatal("Ctrl+N was not consumed after entitlement")
	}
	if !ws.navView.IsOpen() {
		t.Fatal("an entitled session could not open navigation")
	}
	ws.navView.Close()

	// Revoke mid-session: refused again, live.
	gate.Degrade()
	if ws.navigationEntitled() {
		t.Fatal("a degraded gate still reported entitled")
	}
	if !ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyCtrlN}) {
		t.Fatal("Ctrl+N was not consumed after revocation")
	}
	if ws.navView.IsOpen() {
		t.Fatal("a revoked session opened navigation")
	}
	if !strings.Contains(ws.state.LastOutput, "ULTRA") {
		t.Fatalf("the revocation refusal was not explained: %q", ws.state.LastOutput)
	}

	// Re-acquire: opens again.
	ws.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), true)
	if !ws.OpenNavigation(ctx) {
		t.Fatal("a re-entitled session could not open navigation")
	}
	if !ws.navView.IsOpen() {
		t.Fatal("re-entitled navigation did not open")
	}
}
