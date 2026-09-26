package marshal

import "testing"

type fakeCapabilityGate bool

func (g fakeCapabilityGate) Capability(name string) bool {
	return bool(g) && name == CapabilityMarshal
}

func TestTierPolicyNoLeaseUsesStandard(t *testing.T) {
	p := TierPolicy(nil, DefaultSettings())
	if p.Tier != Standard || p.Concurrency != 1 || p.CrossReviewRequired || p.VerifierRequired {
		t.Fatalf("unexpected policy: %+v", p)
	}
}

func TestTierPolicyMarshalLeaseUsesSettings(t *testing.T) {
	settings := DefaultSettings()
	settings.UltraConcurrency = 5
	p := TierPolicy(fakeCapabilityGate(true), settings)
	if p.Tier != Ultra || p.Concurrency != 5 || !p.CrossReviewRequired || !p.VerifierRequired {
		t.Fatalf("unexpected policy: %+v", p)
	}
	if p := TierPolicy(fakeCapabilityGate(false), settings); p.Tier != Standard {
		t.Fatalf("unrelated capability granted Marshal: %+v", p)
	}
}

func TestTierPolicyExpiryAffectsNextDispatchOnly(t *testing.T) {
	gate := fakeCapabilityGate(true)
	running := TierPolicy(gate, DefaultSettings())
	gate = false
	next := TierPolicy(gate, DefaultSettings())
	if running.Tier != Ultra || !running.CrossReviewRequired || next.Tier != Standard || next.Concurrency != 1 || next.CrossReviewRequired {
		t.Fatalf("running=%+v next=%+v", running, next)
	}
}

func TestCrossReviewRequiresDifferentProvider(t *testing.T) {
	p := DispatchPolicy{CrossReviewRequired: true}
	for _, providers := range [][2]string{{"codex", "codex"}, {"codex", "CODEX"}, {" codex ", "codex"}, {"", "claude"}, {"codex", ""}} {
		if err := CheckCrossReviewProvider(p, providers[0], providers[1]); err == nil {
			t.Fatalf("accepted providers %q", providers)
		}
	}
	if err := CheckCrossReviewProvider(p, "codex", "claude"); err != nil {
		t.Fatal(err)
	}
}
