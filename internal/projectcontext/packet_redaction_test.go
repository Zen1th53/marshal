package projectcontext

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

// A secret in the goal reaches the rendered packet in Community, whose
// callers then refuse the packet. Extreme renders the packet with the secret
// removed (reproducibly, so its digest is stable), names the redaction, and
// keeps it valid JSON.
func TestPacketRedactionFollowsTheEdition(t *testing.T) {
	secret := "hunter2-" + "packet"
	g := model.GoalContract{ID: "GOAL-current", Revision: 7, OriginalRequest: "deploy with password=" + secret, DesiredOutcome: "Deployed"}
	p, err := Render(canonicalGoalDeliveryManifest(t, g), 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	fw := security.NewFirewall(security.FirewallConfig{})
	if !security.RedactsSecrets {
		if !strings.Contains(p.Text, secret) || fw.ScanText(p.Text) == nil || strings.Contains(p.Text, "firewall_redacted") {
			t.Fatal("Community packet rendering changed")
		}
		return
	}
	if strings.Contains(p.Text, secret) || fw.ScanText(p.Text) != nil {
		t.Fatalf("secret reached the packet: %s", p.Text)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(p.Text, packetHeader)), &envelope); err != nil {
		t.Fatal(err)
	}
	if _, ok := envelope["firewall_redacted"]; !ok {
		t.Fatal("redaction evidence missing")
	}
	var bytes int
	if json.Unmarshal(envelope["bytes"], &bytes) != nil || bytes != len(p.Text) || p.Bytes != len(p.Text) {
		t.Fatal("redacted packet byte accounting mismatch")
	}
	again, err := Render(canonicalGoalDeliveryManifest(t, g), 16<<10)
	if err != nil || again.Digest != p.Digest {
		t.Fatal("redacted packet is not reproducible", err)
	}
}
