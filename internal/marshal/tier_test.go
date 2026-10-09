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

type fakeExecutionGate struct {
	capable bool
	enabled bool
}

func (g fakeExecutionGate) Capability(name string) bool {
	return g.capable && name == CapabilityMarshal
}

func (g fakeExecutionGate) ExecutionEnabled() bool {
	return g.enabled
}

func TestTierPolicyExecutionDisabledUsesStandard(t *testing.T) {
	settings := DefaultSettings()
	settings.UltraConcurrency = 4
	// Entitled but execution off -> must use Standard with Concurrency 1
	p := TierPolicy(fakeExecutionGate{capable: true, enabled: false}, settings)
	if p.Tier != Standard || p.Concurrency != 1 || p.CrossReviewRequired || p.VerifierRequired {
		t.Fatalf("expected Standard with concurrency 1 when execution is disabled, got: %+v", p)
	}
	// Entitled and execution on -> must use Ultra
	pOn := TierPolicy(fakeExecutionGate{capable: true, enabled: true}, settings)
	if pOn.Tier != Ultra || pOn.Concurrency != 4 || !pOn.CrossReviewRequired || !pOn.VerifierRequired {
		t.Fatalf("expected Ultra when execution is enabled, got: %+v", pOn)
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

// A cross-review needs a reviewer, which may be the worker's own provider:
// independence comes from each role running in a session of its own.
func TestCrossReviewNeedsAReviewerNotAnotherProvider(t *testing.T) {
	p := DispatchPolicy{CrossReviewRequired: true}
	for _, providers := range [][2]string{{"codex", "codex"}, {"codex", "claude"}, {"", "claude"}} {
		if err := CheckCrossReviewProvider(p, providers[0], providers[1]); err != nil {
			t.Fatalf("refused providers %q: %v", providers, err)
		}
	}
	for _, reviewer := range []string{"", "  "} {
		if err := CheckCrossReviewProvider(p, "codex", reviewer); err == nil {
			t.Fatalf("accepted a cross-review without a reviewer %q", reviewer)
		}
	}
}
