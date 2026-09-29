package marshal_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestTierPolicyCloudGateLease(t *testing.T) {
	var _ marshal.CapabilityGate = (*cloud.Gate)(nil)
	now := time.Now().UTC()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ring := cloud.NewKeyRing()
	if err := ring.Add("tier-key", pub); err != nil {
		t.Fatal(err)
	}
	gate := cloud.NewGate("installation", "session", ring, func() time.Time { return now })
	claims := cloud.Claims{JTI: "tier-test", KeyID: "tier-key", EntitlementID: "entitlement", InstallationID: "installation", SessionID: "session", Capabilities: []string{marshal.CapabilityMarshal}, IssuedAt: now, ExpiresAt: now.Add(time.Minute)}
	lease, err := cloud.SignLease(claims, cloud.Bundle{PolicyDigest: "digest", RoutingTable: map[string]string{"primary": "route"}, IssuedFor: "installation"}, priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Adopt(lease); err != nil {
		t.Fatal(err)
	}
	if p := marshal.TierPolicy(gate, marshal.DefaultSettings()); p.Tier != marshal.Ultra {
		t.Fatalf("valid lease policy: %+v", p)
	}
	now = now.Add(2 * time.Minute)
	if p := marshal.TierPolicy(gate, marshal.DefaultSettings()); p.Tier != marshal.Standard {
		t.Fatalf("expired lease policy: %+v", p)
	}
}
