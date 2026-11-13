//go:build !governed

package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/verification"
)

// Observations are canonical DATA. Only the runtime's unexported successful
// fresh-turn capture and source/owner fence authorize their application. A
// binary selection digest observes the CLI file before/after execution; it is
// not a kernel attestation of an executable image.
type ProjectVerificationObservation struct {
	ID, Provider, ObservedModel, SessionID, ContextReceiptID        string
	BinarySelectionDigest, InputDigest, OutputDigest, PayloadDigest string
	SourceHead, WorkspaceTree                                       string
	StartedAt, FinishedAt                                           time.Time
}

// Revalidate current essential context and exact canonical BLOCKED baseline
// in the same reserved writer transaction as evidence, derived decision, and
// fresh-session provenance. Any failure leaves the baseline unchanged.
func (s *Store) CommitProjectContextVerificationObservation(ctx context.Context, a ProjectContextAdmission, expected, next verification.Session, observation ProjectVerificationObservation) error {
	now := next.UpdatedAt
	if projectArchitectureUnknownIdentity(observation.ObservedModel) || projectArchitectureUnknownIdentity(observation.SessionID) {
		return projectcontext.Changed("project_context_verification_extreme.go:34")
	}
	if expected.State != verification.Blocked || expected.ID == "" || expected.Version < 1 || next.ID != expected.ID || next.Version != expected.Version+1 || next.Binding != expected.Binding || now.IsZero() || now.Before(observation.FinishedAt) || observation.FinishedAt.Before(observation.StartedAt) || observation.StartedAt.IsZero() || observation.ID == "" || observation.Provider == "" || observation.ObservedModel == "" || observation.SessionID == "" || a.ID != observation.ContextReceiptID || a.Stage != "review" || a.Binding.RunID != expected.Binding.RunID || a.Binding.RunVersion != expected.Binding.RunVersion || a.Binding.Tree != observation.WorkspaceTree || a.Binding.Tree != expected.Binding.TreeDigest || a.ExpectedAuthority == nil || a.ExpectedStateDigest == "" || a.ExpectedPacketDigest == "" || a.Binding.RequiredStateDigest != a.ExpectedStateDigest || a.Audience.Actor != a.Binding.Principal || normalizeProjectAuthorityBinding(a.ExpectedAuthority.Request.Binding) != normalizeProjectAuthorityBinding(a.Binding) {
		return projectcontext.Changed("project_context_verification_extreme.go:37")
	}
	for _, digest := range []string{observation.BinarySelectionDigest, observation.InputDigest, observation.OutputDigest, observation.PayloadDigest} {
		if !digestPattern.MatchString(digest) {
			return projectcontext.ErrInvalid
		}
	}
	if !projectArchitectureObject(observation.SourceHead) || a.Binding.Base != observation.SourceHead || !reflect.DeepEqual(next.Criteria, expected.Criteria) || !reflect.DeepEqual(next.RequiredChecks, expected.RequiredChecks) || !reflect.DeepEqual(next.RuntimeProof, expected.RuntimeProof) || len(next.Claims) != len(expected.Claims) || len(next.Evidence) != len(expected.Evidence)+len(expected.Claims) || !reflect.DeepEqual(next.Evidence[:len(expected.Evidence)], expected.Evidence) || !reflect.DeepEqual(next.Contradictions, expected.Contradictions) || !reflect.DeepEqual(next.Waivers, expected.Waivers) || next.RiskTier != expected.RiskTier || !reflect.DeepEqual(next.KnownBlockers, expected.KnownBlockers) || !reflect.DeepEqual(next.Limitations, expected.Limitations) || next.Cancelled != expected.Cancelled || !next.CreatedAt.Equal(expected.CreatedAt) || verification.ValidateSession(next) != nil || next.State != verification.Evaluate(next, next.Binding, now) {
		return projectcontext.Changed("project_context_verification_extreme.go:45")
	}
	for i, old := range expected.Claims {
		claim := next.Claims[i]
		if len(claim.EvidenceIDs) != len(old.EvidenceIDs)+1 || !reflect.DeepEqual(claim.EvidenceIDs[:len(old.EvidenceIDs)], old.EvidenceIDs) {
			return projectcontext.Changed("project_context_verification_extreme.go:50")
		}
		evidence := next.Evidence[len(expected.Evidence)+i]
		if evidence.ID != claim.EvidenceIDs[len(old.EvidenceIDs)] || evidence.ClaimID != claim.ID || evidence.Kind != "fresh-independent-native-verifier" || evidence.Producer != observation.ID || evidence.Provider != observation.Provider || evidence.Oracle != "independent-semantic-model-review" || evidence.ClusterID != "native-verifier-"+observation.ID || evidence.InputsDigest != observation.InputDigest || evidence.OutputDigest != observation.OutputDigest || evidence.TreeDigest != expected.Binding.TreeDigest || evidence.EnvironmentDigest != expected.Binding.EnvironmentDigest || !evidence.CreatedAt.Equal(observation.FinishedAt) || evidence.Attempts != 1 || evidence.Reason == "" || evidence.Reason != strings.TrimSpace(evidence.Reason) || (evidence.Status == verification.StatusPass && evidence.Passes != 1) || (evidence.Status != verification.StatusPass && evidence.Passes != 0) {
			return projectcontext.Changed("project_context_verification_extreme.go:54")
		}
		claim.EvidenceIDs = old.EvidenceIDs
		if !reflect.DeepEqual(claim, old) {
			return projectcontext.Changed("project_context_verification_extreme.go:58")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Acquire the sole writer reservation before reading mutable authority.
	if _, err = tx.ExecContext(ctx, `UPDATE verification_sessions SET version=version WHERE verification_id=? AND version=?`, expected.ID, expected.Version); err != nil {
		return err
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT session_json FROM verification_sessions WHERE verification_id=? AND version=?`, expected.ID, expected.Version).Scan(&raw); err != nil {
		return verification.ErrConflict
	}
	var canonical verification.Session
	if json.Unmarshal([]byte(raw), &canonical) != nil || !reflect.DeepEqual(canonical, expected) {
		return verification.ErrConflict
	}
	parent, err := readProjectContextReceiptTx(ctx, tx, a.ID)
	if err != nil {
		return err
	}
	if parent.Outcome != "QUALIFIED" || parent.Stage != a.Stage || parent.ParentReceiptID != a.ParentReceiptID || parent.Binding != a.Binding || parent.PacketDigest != a.ExpectedPacketDigest || now.Before(parent.CheckedAt) || parent.ValidUntil != nil && !now.Before(*parent.ValidUntil) {
		return projectcontext.Changed("project_context_verification_extreme.go:83")
	}
	authority, err := readProjectContextAuthorityTx(ctx, tx, a.ExpectedAuthority.Request, now)
	if err != nil {
		return err
	}
	if authority.Digest != a.ExpectedAuthority.Digest {
		return projectcontext.Changed("project_context_verification_extreme.go:90")
	}
	snapshot, err := readProjectContextSnapshotBound(ctx, tx, a.Binding.Project, a.Audience, a.Selection, now, a.Binding.Contract)
	if err != nil {
		return err
	}
	if snapshot.StateDigest != a.ExpectedStateDigest {
		return projectcontext.Changed("project_context_verification_extreme.go:97")
	}
	// A session cannot certify another invocation, even if its report is replayed.
	sessionKey := "verification-native-session-" + strings.TrimPrefix(projectcontext.Digest("marshal.required.verifier.session.v1", struct{ Provider, Session string }{observation.Provider, observation.SessionID}), "sha256:")
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE event_id=?`, sessionKey).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return projectcontext.Changed("project_context_verification_extreme.go:106")
	}
	if err = s.AppendEvent(ctx, tx, model.Event{ID: sessionKey, Type: "PROJECT_VERIFICATION_NATIVE_SESSION_BOUND", ProjectID: a.Binding.Project.StorageID, Timestamp: now, Data: map[string]any{"verification_id": next.ID, "verification_version": next.Version, "observation": observation, "context_receipt_id": a.ID}}); err != nil {
		return err
	}
	body, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE verification_sessions SET version=?,state=?,session_json=?,updated_at=? WHERE verification_id=? AND version=?`, next.Version, next.State, body, now.UTC().Format(timeLayout), next.ID, expected.Version)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return verification.ErrConflict
	}
	return tx.Commit()
}
