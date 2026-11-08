//go:build !governed

package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/verification"
)

func architectureCompletionCheck(t *testing.T, s *Store, compatible projectcontext.Receipt, session verification.Session, envelope verification.BundleEnvelope) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	return validateProjectArchitectureCompletionTx(ctx, tx, compatible, session, envelope, compatible.CheckedAt)
}

func TestProjectArchitectureCompletionChecksExactCanonicalProducer(t *testing.T) {
	s, a, o, envelope, _ := architectureFixture(t)
	compatible, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.GetVerificationSession(t.Context(), o.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = architectureCompletionCheck(t, s, compatible, session, envelope); err != nil {
		t.Fatal("exact canonical architecture producer refused completion", err)
	}
	// SQL inspection and the serialized observation still prove no live model.
	_, _, _, _, fixtureReceipt := projectCompletionFixture(t)
	if err = architectureCompletionCheck(t, s, fixtureReceipt, session, envelope); err == nil {
		t.Fatal("fixture receipt without canonical producer event granted completion")
	}
}

func TestProjectArchitectureCompletionRejectsChangedVerifierAndBundle(t *testing.T) {
	for _, scenario := range []string{"verification version", "verification id", "session content", "bundle", "plan", "receipt digest", "receipt coverage", "observation tamper"} {
		t.Run(scenario, func(t *testing.T) {
			s, a, o, envelope, _ := architectureFixture(t)
			compatible, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, o, envelope)
			if err != nil {
				t.Fatal(err)
			}
			session, err := s.GetVerificationSession(t.Context(), o.VerificationID)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "verification version":
				session.Version++
				if err = s.UpdateVerificationSession(t.Context(), session, session.Version-1); err != nil {
					t.Fatal(err)
				}
			case "verification id":
				session.ID = "new-canonical-verification"
				if err = s.CreateVerificationSession(t.Context(), session); err != nil {
					t.Fatal(err)
				}
				envelope.Bundle, err = verification.BuildEvidenceBundle("new-verification-bundle", session.ID, session.Binding, nil, a.Now)
				if err != nil {
					t.Fatal(err)
				}
			case "session content":
				session.Version++
				session.Evidence[0].Provider = "changed-reviewer"
				if err = s.UpdateVerificationSession(t.Context(), session, session.Version-1); err != nil {
					t.Fatal(err)
				}
			case "bundle":
				envelope.Bundle, err = verification.BuildEvidenceBundle("new-evidence-bundle", session.ID, session.Binding, nil, a.Now)
				if err != nil {
					t.Fatal(err)
				}
			case "plan":
				p, _ := s.GetActivePlan(t.Context(), planTestProject)
				next, err := p.Revise(p.Version, "Architecture basis changed", a.Now)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.SavePlan(t.Context(), next, p.Version); err != nil {
					t.Fatal(err)
				}
			case "receipt digest":
				compatible.CompatibilityEvidenceDigest = projectcontext.Digest("caller", true)
			case "receipt coverage":
				compatible.IncludedCount--
			case "observation tamper":
				if _, err = s.db.ExecContext(t.Context(), `UPDATE audit_events SET data_json=json_set(data_json,'$.observation.Invocation.ObservedModel','caller-model') WHERE event_type='PROJECT_ARCHITECTURE_COMPATIBILITY_RECORDED'`); err != nil {
					t.Fatal(err)
				}
			}
			if err = architectureCompletionCheck(t, s, compatible, session, envelope); err == nil {
				t.Fatal("changed verifier/bundle/canonical producer reused compatibility")
			}
		})
	}
}
