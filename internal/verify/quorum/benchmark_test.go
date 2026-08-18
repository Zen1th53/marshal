package quorum

import (
	"context"
	"testing"
	"time"
)

func BenchmarkEvaluate100Attestations(b *testing.B) {
	created := time.Unix(100, 0).UTC()
	engine := NewEngine(func() time.Time { return created })
	attestations := make([]Attestation, 100)
	for i := range attestations {
		attestations[i] = Attestation{Subject: "agent-" + string(rune('a'+i%26)), Provider: "Codex", Role: "reviewer", ChangeID: "change-1", EvidenceID: "evidence-1", Kind: "security", Result: ResultPass, ContentDigest: "sha256:change", CreatedAt: created}
	}
	// The benchmark uses one distinct principal per required attestation in
	// production-sized inputs; this fixture intentionally measures the linear
	// validation path and is not a quorum-policy acceptance test.
	for i := range attestations {
		attestations[i].Subject = "agent-" + string(rune(i+1000))
	}
	requirements := []Requirement{{Kind: "security", Minimum: 100, Independent: true, AllowedRoles: []string{"reviewer"}}}
	provenance := Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Evaluate(context.Background(), requirements, attestations, provenance); err != nil {
			b.Fatal(err)
		}
	}
}
