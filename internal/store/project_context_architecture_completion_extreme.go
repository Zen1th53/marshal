//go:build !governed

package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"time"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/verification"
)

// Called inside the completion writer transaction, before coverage/attestation
// writes. This observes SQL data only; its caller still holds the owned fence.
func validateProjectArchitectureCompletionTx(ctx context.Context, tx *sql.Tx, compatible projectcontext.Receipt, session verification.Session, envelope verification.BundleEnvelope, now time.Time) error {
	stored, err := readProjectContextReceiptTx(ctx, tx, compatible.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(stored, compatible) || compatible.Stage != "architecture_compatibility" || compatible.Outcome != "COMPATIBLE" {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:27")
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data_json FROM audit_events WHERE event_id=? AND event_type='PROJECT_ARCHITECTURE_COMPATIBILITY_RECORDED'`, "architecture-receipt-"+compatible.ID).Scan(&raw); err != nil {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:31")
	}
	if len(raw) > projectcontext.MaximumPacketBytes {
		return projectcontext.ErrBudget
	}
	var record projectArchitectureEvidenceRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:40")
	}
	input, observation := record.Input, record.Observation
	if record.ReceiptID != compatible.ID || observation.ID != compatible.ID || record.CompatibilityEvidenceDigest != compatible.CompatibilityEvidenceDigest || observation.InputDigest != input.Digest || input.Packet.Text != "" || input.Contract != compatible.CurrentContract || compatible.Binding.Contract != input.Contract || input.Packet.Digest != compatible.PacketDigest || input.Packet.ManifestDigest != compatible.Binding.ManifestDigest || input.Packet.Bytes != compatible.RenderedBytes || !reflect.DeepEqual(input.Packet.Loaded, compatible.Loaded) || compatible.RequiredCount != len(input.Packet.Loaded) || compatible.IncludedCount != compatible.RequiredCount || input.Binding != session.Binding || input.VerificationID != session.ID || input.VerificationVersion != session.Version || observation.VerificationID != session.ID || observation.VerificationVersion != session.Version || input.SessionDigest != projectcontext.Digest("marshal.architecture.verification.v1", session) || input.BundleDigest != envelope.Bundle.ManifestDigest || envelope.Bundle.VerificationID != session.ID {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:44")
	}
	boundInput := input
	boundInput.Digest = ""
	if input.Digest != projectcontext.Digest("marshal.project.architecture.input.v1", boundInput) {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:49")
	}
	digest := projectcontext.Digest("marshal.project.architecture.observation.v1", struct {
		Input       string
		Observation ProjectArchitectureObservation
	}{input.Digest, observation})
	if digest != compatible.CompatibilityEvidenceDigest {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:56")
	}
	outcome, err := projectArchitectureOutcome(input, observation)
	if err != nil || outcome != "COMPATIBLE" {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:60")
	}
	if err = validateProjectContextPlan(ctx, tx, compatible.Binding); err != nil {
		return err
	}
	var planRaw string
	if err = tx.QueryRowContext(ctx, `SELECT plan_json FROM execution_plans WHERE plan_id=? AND version=?`, compatible.Binding.PlanID, compatible.Binding.PlanVersion).Scan(&planRaw); err != nil {
		return err
	}
	p, err := decodePlan(planRaw)
	if err != nil || input.PlanDigest != projectcontext.Digest("marshal.architecture.plan.v1", p) {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:71")
	}
	contract, _, contractDigest, err := readProjectContract(ctx, tx, compatible.Binding.Project)
	if err != nil {
		return err
	}
	if input.Contract != (projectcontext.ContractRef{ID: contract.ID, Revision: contract.Revision, Digest: contractDigest}) {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:78")
	}
	// Requalify EVERY mandatory record, including future task/path scopes that
	// no current approved task selects. Audience metadata carries no body or
	// grant; its exact projection is bound by the review's state digest.
	selection, err := projectcontext.PlanningSelection(contract)
	if err != nil || now.IsZero() || now.Before(compatible.CheckedAt) || record.Audience.Actor != compatible.Binding.Principal || record.Authority == nil {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:85")
	}
	snapshot, err := readProjectContextSnapshotBound(ctx, tx, compatible.Binding.Project, record.Audience, selection, now, compatible.Binding.Contract)
	if err != nil {
		return err
	}
	if snapshot.StateDigest != compatible.Binding.RequiredStateDigest || snapshot.InventoryDigest != compatible.Binding.InventoryDigest || snapshot.CurrentContract != compatible.CurrentContract {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:92")
	}
	if record.Authority.Request.Purpose != ProjectContextAuthorityCompletion || normalizeProjectAuthorityBinding(record.Authority.Request.Binding) != normalizeProjectAuthorityBinding(compatible.Binding) {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:95")
	}
	authority, err := readProjectContextAuthorityTx(ctx, tx, record.Authority.Request, now)
	if err != nil {
		return err
	}
	if authority.Digest != record.Authority.Digest {
		return projectcontext.Changed("project_context_architecture_completion_extreme.go:102")
	}
	var sessionRaw []byte
	if err = tx.QueryRowContext(ctx, `SELECT session_json FROM verification_sessions WHERE verification_id=? AND version=?`, session.ID, session.Version).Scan(&sessionRaw); err != nil {
		return verification.ErrConflict
	}
	var canonicalSession verification.Session
	if json.Unmarshal(sessionRaw, &canonicalSession) != nil || !reflect.DeepEqual(session, canonicalSession) {
		return verification.ErrBindingMismatch
	}
	if err = envelope.Bundle.Verify(envelope.Payloads, session.Binding); err != nil {
		return err
	}
	return nil
}
