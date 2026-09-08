package verification

import (
	"testing"
	"time"
)

func TestFlakyEvidenceIsQuarantined(t *testing.T) {
	v := AssessStability([]Status{StatusPass, StatusFail, StatusPass}, []string{"race"})
	if v.Status != StatusUnknown || !v.Quarantined {
		t.Fatalf("%+v", v)
	}
}
func TestOracleIndependenceRejectsSharedLineage(t *testing.T) {
	a := OracleDescriptor{ID: "a", Provider: "p", Implementation: "i", Dataset: "d", Author: "x"}
	b := OracleDescriptor{ID: "b", Provider: "q", Implementation: "i", Dataset: "e", Author: "y"}
	if Independent(a, b) {
		t.Fatal("shared implementation counted independent")
	}
}
func TestDifferentialAndMetamorphicDisagreementFailsClosed(t *testing.T) {
	if Differential(StatusPass, StatusFail) != StatusUnknown {
		t.Fatal("differential disagreement hidden")
	}
	if Metamorphic(StatusPass, StatusPass, false) != StatusFail {
		t.Fatal("broken relation passed")
	}
}
func TestExternalEffectsNeedReadback(t *testing.T) {
	r := ExternalEffectReceipt{Operation: "push", Target: "origin", RequestedDigest: "a", ObservedDigest: "b", Observer: "git", ObservedAt: time.Now()}
	if r.Verify() != ErrReplayDivergence {
		t.Fatal("mismatched external readback accepted")
	}
}
func TestWaiverCannotOverrideMandatoryOrCritical(t *testing.T) {
	w := Waiver{ID: "w", Actor: "operator", Reason: "accepted", ExpiresAt: time.Now().Add(time.Hour)}
	if ValidateWaiver(w, true, false, time.Now()) != ErrUnsafeWaiver {
		t.Fatal("mandatory criterion waived")
	}
}
func TestProviderFailoverRequiresAuthorizedConfigurationAndEvidence(t *testing.T) {
	attempts := []ProviderAttempt{{Provider: "bad", ConfigurationDigest: "x", Status: StatusPass, EvidenceIDs: []string{"fake"}}, {Provider: "good", ConfigurationDigest: "approved", Status: StatusPass, EvidenceIDs: []string{"e"}}}
	got, err := SelectProvider(attempts, map[string]string{"good": "approved"})
	if err != nil || got.Provider != "good" {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestUnknownDiscoveryCannotSelfVerify(t *testing.T) {
	if ValidateDiscoveries([]Discovery{{Source: "fuzz", Finding: "crash", EvidenceID: "", Status: StatusPass}}) != StatusUnknown {
		t.Fatal("unbound discovery passed")
	}
}
func TestCalibrationTracksFalseResults(t *testing.T) {
	c := Calibration{TruePositive: 95, FalsePositive: 3, TrueNegative: 95, FalseNegative: 7}
	p, r, s := c.Rates()
	if s != StatusPass || p <= 0 || r <= 0 || CalibrationWithin(c, .01) {
		t.Fatalf("p=%f r=%f s=%s", p, r, s)
	}
}
func TestVerificationSandboxIsolation(t *testing.T) {
	good := SandboxPolicy{ReadOnlyTree: true, NetworkDisabled: true, SecretsDisabled: true, WritableRoots: []string{"/tmp/marshal-verify"}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	good.WritableRoots = []string{"/repo"}
	if err := good.Validate(); err == nil {
		t.Fatal("repository writable")
	}
}
func TestSemanticScopeRejectsGaming(t *testing.T) {
	if _, err := CanonicalScopes([]string{"all"}); err == nil {
		t.Fatal("unbounded scope accepted")
	}
	got, err := CanonicalScopes([]string{"runtime/provider", "runtime/provider"})
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
}
