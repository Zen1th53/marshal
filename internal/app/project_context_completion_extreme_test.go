//go:build !governed

package app

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestRequiredCompletionRuntimeActualHostReviewAndAtomicAttestation(t *testing.T) {
	r, ctx, cli, envelope, id, _ := completionRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.Verification().completeRequiredContextAttestationWithCLI(ctx, session, envelope, "host-fixture-only", cli)
	if err != nil || a.VerificationID != session.ID || a.VerificationVersion != session.Version {
		t.Fatal("actual host architecture review did not atomically qualify all tasks and attestation", a, err)
	}
	persisted, err := r.store.LatestCompletionAttestation(ctx, session.ID)
	if err != nil || persisted.Digest != a.Digest {
		t.Fatal("coverage/attestation pair missing", err)
	}
	// This observes actual host callbacks and canonical fixtures, not genuine
	// native/model or paid-source Process06 completion.
}

func TestRequiredCompletionRuntimeUnknownArchitectureCannotAttest(t *testing.T) {
	r, ctx, cli, envelope, id, _ := completionRuntimeFixture(t, "unknown-model")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Verification().completeRequiredContextAttestationWithCLI(ctx, session, envelope, "host-fixture-only", cli); err == nil {
		t.Fatal("unknown model architecture became completion")
	}
	if _, err = r.store.LatestCompletionAttestation(ctx, session.ID); err == nil {
		t.Fatal("unknown architecture persisted canonical completion")
	}
}

func TestRequiredCompletionRuntimeLiveProofOneShotAndForeignRuntime(t *testing.T) {
	r, ctx, cli, envelope, id, _ := completionRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	proof := completionLiveProofFixture(t, r, ctx, cli, envelope, id)
	foreign := &VerificationService{runtime: &Runtime{}}
	if _, err = foreign.commitRequiredContextCompletionProof(ctx, session, envelope, "foreign-host", proof); err == nil || proof.consumed {
		t.Fatal("foreign runtime spent rightful proof", err)
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := r.Verification().commitRequiredContextCompletionProof(ctx, session, envelope, "host-fixture-only", proof)
			results <- err
		}()
	}
	passed := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			passed++
		}
	}
	if passed != 1 {
		t.Fatal("live proof did not authorize exactly one transaction", passed)
	}
}

func TestRequiredCompletionRuntimeChangedMemorySpendsProofWithoutAttestation(t *testing.T) {
	r, ctx, cli, envelope, id, _ := completionRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	proof := completionLiveProofFixture(t, r, ctx, cli, envelope, id)
	receipt := proof.receipt
	dataOnly := &requiredArchitectureCompletionProof{runtime: r, receipt: receipt}
	if _, err = r.Verification().commitRequiredContextCompletionProof(ctx, session, envelope, "serialized-fixture", dataOnly); err == nil {
		t.Fatal("durable receipt minted actual turn proof")
	}
	if _, err = r.store.UpdateMemory(t.Context(), r.ProjectID(), "MEM-required-resolver", 1, func(record *model.MemoryRecordV2) error { record.ACLScope = "foreign"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Verification().commitRequiredContextCompletionProof(ctx, session, envelope, "host-fixture-only", proof); err == nil || !proof.consumed {
		t.Fatal("changed memory finalized or preserved reusable proof", err)
	}
	if _, err = r.store.LatestCompletionAttestation(ctx, id); err == nil {
		t.Fatal("revoked memory persisted attestation")
	}
	if _, err = r.Verification().commitRequiredContextCompletionProof(ctx, session, envelope, "host-fixture-only", proof); err == nil {
		t.Fatal("spent proof retried")
	}
}
