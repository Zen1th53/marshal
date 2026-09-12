package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/verification"
)

func (s *Store) CreateVerificationSession(ctx context.Context, session verification.Session) error {
	if err := verification.ValidateSession(session); err != nil {
		return err
	}
	body, err := json.Marshal(session)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO verification_sessions(verification_id,version,project_id,goal_id,plan_id,run_id,state,session_json,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, session.ID, session.Version, session.Binding.ProjectID, session.Binding.GoalID, session.Binding.PlanID, session.Binding.RunID, session.State, body, session.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return fmt.Errorf("create verification session: %w", err)
	}
	return nil
}

const timeLayout = "2006-01-02T15:04:05.999999999Z07:00"

func (s *Store) GetVerificationSession(ctx context.Context, id string) (verification.Session, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT session_json FROM verification_sessions WHERE verification_id=?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return verification.Session{}, verification.ErrNotFound
	}
	if err != nil {
		return verification.Session{}, err
	}
	var v verification.Session
	if err := json.Unmarshal(body, &v); err != nil {
		return verification.Session{}, fmt.Errorf("decode verification session: %w", err)
	}
	return v, nil
}

// LatestVerificationForRun returns the newest canonical verification session
// for an exact Process 05 run.  Status surfaces use this read path rather than
// guessing that a run ID is itself a verification ID.
func (s *Store) LatestVerificationForRun(ctx context.Context, runID string) (verification.Session, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT session_json FROM verification_sessions
		WHERE run_id=?
		ORDER BY version DESC, updated_at DESC, verification_id DESC LIMIT 1
	`, runID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return verification.Session{}, verification.ErrNotFound
	}
	if err != nil {
		return verification.Session{}, err
	}
	var session verification.Session
	if err := json.Unmarshal(body, &session); err != nil {
		return verification.Session{}, fmt.Errorf("decode verification session: %w", err)
	}
	return session, nil
}

func (s *Store) UpdateVerificationSession(ctx context.Context, session verification.Session, expectedVersion int64) error {
	if session.Version != expectedVersion+1 {
		return verification.ErrConflict
	}
	if err := verification.ValidateSession(session); err != nil {
		return err
	}
	body, err := json.Marshal(session)
	if err != nil {
		return err
	}
	r, err := s.db.ExecContext(ctx, `UPDATE verification_sessions SET version=?,state=?,session_json=?,updated_at=? WHERE verification_id=? AND version=?`, session.Version, session.State, body, session.UpdatedAt.UTC().Format(timeLayout), session.ID, expectedVersion)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return verification.ErrConflict
	}
	return nil
}

func (s *Store) AppendCompletionAttestation(ctx context.Context, a verification.CompletionAttestation) error {
	if err := a.Verify(a.Binding); err != nil {
		return err
	}
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO completion_attestations(attestation_id,verification_id,verification_version,decision,digest,attestation_json,issued_at) SELECT ?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM verification_sessions WHERE verification_id=? AND version=?)`, a.ID, a.VerificationID, a.VerificationVersion, a.Decision, a.Digest, body, a.IssuedAt.UTC().Format(timeLayout), a.VerificationID, a.VerificationVersion)
	if err != nil {
		return fmt.Errorf("append completion attestation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return verification.ErrConflict
	}
	return nil
}

// LatestCompletionAttestation returns the most recent attestation for one
// verification session. Process 07 binds to this record rather than to a
// caller-supplied digest, so learning cannot be attached to an outcome that
// was never attested.
func (s *Store) LatestCompletionAttestation(ctx context.Context, verificationID string) (verification.CompletionAttestation, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT attestation_json FROM completion_attestations
		WHERE verification_id=?
		ORDER BY verification_version DESC, issued_at DESC LIMIT 1
	`, verificationID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return verification.CompletionAttestation{}, verification.ErrNotFound
	}
	if err != nil {
		return verification.CompletionAttestation{}, err
	}
	var a verification.CompletionAttestation
	if err := json.Unmarshal(body, &a); err != nil {
		return verification.CompletionAttestation{}, fmt.Errorf("decode completion attestation: %w", err)
	}
	return a, nil
}
