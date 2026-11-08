//go:build !governed

package store

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestProjectArchitectureCompletionRechecksMandatoryFutureTaskMemoryAtomically(t *testing.T) {
	s, a, observation, envelope, tasks := architectureFixture(t, true)
	compatible, err := s.CommitProjectArchitectureCompatibility(t.Context(), a, observation, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateMemory(t.Context(), a.Binding.Project.StorageID, "MEM-inspect-task-only", 1, func(record *model.MemoryRecordV2) error {
		record.Body = "Mandatory future-task decision changed after actual review"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := range tasks {
		repacketContextAdmission(t, s, &tasks[i])
	}
	session, err := s.GetVerificationSession(t.Context(), observation.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	attestation, err := verification.NewCompletionAttestation("future-task-memory-completion", session, envelope.Bundle.ManifestDigest, "fixture-only", a.Now)
	if err != nil {
		t.Fatal(err)
	}
	before := queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts WHERE stage='completion'`)
	if _, err = s.CommitProjectContextCompletion(t.Context(), tasks, envelope, attestation, compatible.ID); err == nil {
		t.Fatal("mandatory future-task memory changed outside all final task selections but completion succeeded")
	}
	if queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts WHERE stage='completion'`) != before || queryInt(t, s.db, `SELECT count(*) FROM completion_attestations`) != 0 {
		t.Fatal("failed whole-context check partially wrote completion")
	}
}
