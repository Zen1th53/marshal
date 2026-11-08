//go:build !governed

package app

import (
	"os"
	"testing"
)

func TestRequiredCanonicalVerifierProofDataOnlySessionCannotSpawnArchitecture(t *testing.T) {
	r, ctx, cli, envelope, id, captured := architectureRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Verification().completeRequiredContextAttestationWithCLI(ctx, session, envelope, "data-only-canonical-session", cli); err == nil {
		t.Fatal("canonical data-only verified session granted architecture and completion")
	}
	if _, err = os.Stat(captured); !os.IsNotExist(err) {
		t.Fatal("missing actual P06 proof spawned architecture reviewer")
	}
	if _, err = r.store.LatestCompletionAttestation(ctx, id); err == nil {
		t.Fatal("data-only P06 attested completion")
	}
}

func TestRequiredCanonicalVerifierProofForeignRuntimeCannotSpendAndReplayRefuses(t *testing.T) {
	r, ctx, _, envelope, id, _ := completionRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r.requiredIntegrationSources.mu.Lock()
	proof := r.requiredIntegrationSources.byRun[session.Binding.RunID].verification
	r.requiredIntegrationSources.mu.Unlock()
	if _, err = (&Runtime{}).takeRequiredCanonicalVerifierProof(ctx, session, envelope); err == nil || proof.consumed {
		t.Fatal("foreign runtime spent rightful actual verifier capture", err)
	}
	if _, err = r.takeRequiredCanonicalVerifierProof(ctx, session, envelope); err != nil {
		t.Fatal(err)
	}
	if _, err = r.takeRequiredCanonicalVerifierProof(ctx, session, envelope); err == nil {
		t.Fatal("actual verifier capture replayed")
	}
}

func TestRequiredCanonicalVerifierProofReturnedSessionCannotMutateFrozenProof(t *testing.T) {
	r, ctx, cli, envelope, id, _ := completionRuntimeFixture(t, "aliased-session")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Verification().completeRequiredContextAttestationWithCLI(ctx, session, envelope, "host-proof-session-clone", cli); err != nil {
		t.Fatal("returned session aliases changed live proof", err)
	}
}

func TestRequiredCanonicalVerifierProofSameActualSessionCannotCountIndependentArchitecture(t *testing.T) {
	r, ctx, cli, envelope, id, _ := completionRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := r.takeRequiredCanonicalVerifierProof(ctx, session, envelope)
	if err != nil {
		t.Fatal(err)
	}
	var architecture *requiredArchitectureCompletionProof
	if _, err = r.produceRequiredArchitectureReview(ctx, cli, id, envelope, &architecture); err != nil {
		t.Fatal(err)
	}
	architecture.capture.session = verifier.capture.session
	if err = verifier.bindArchitecture(r, architecture); err == nil {
		t.Fatal("reused actual native session counted as independent architecture")
	}
	if _, err = r.store.LatestCompletionAttestation(ctx, id); err == nil {
		t.Fatal("same-session model proof attested completion")
	}
}
