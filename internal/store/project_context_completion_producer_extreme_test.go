//go:build !governed

package store

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/verification"
)

func projectCanonicalCompletionFixture(t *testing.T) (*Store, []ProjectContextAdmission, verification.BundleEnvelope, verification.CompletionAttestation, projectcontext.Receipt) {
	t.Helper()
	s, a, o, envelope, tasks := architectureFixture(t)
	compatible, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.GetVerificationSession(t.Context(), o.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	attestation, err := verification.NewCompletionAttestation("completion-canonical-producer", session, envelope.Bundle.ManifestDigest, "fixture-runtime-completion", a.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s, tasks, envelope, attestation, compatible
}

func TestProjectContextCompletionRefusesReceiptWithoutCanonicalArchitectureProducer(t *testing.T) {
	s, tasks, envelope, attestation, compatible := projectCompletionFixture(t)
	if _, err := s.CommitProjectContextCompletion(t.Context(), tasks, envelope, attestation, compatible.ID); err == nil {
		t.Fatal("bare fixture compatibility receipt bypassed canonical producer validation")
	}
	if n := queryInt(t, s.db, `SELECT count(*) FROM completion_attestations`); n != 0 {
		t.Fatal("refusal retained attestation", n)
	}
}
