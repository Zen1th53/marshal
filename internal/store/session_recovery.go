package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func (s *Store) SessionRecovery(ctx context.Context, id string, generation int64) (model.SessionRecovery, error) {
	var raw string
	var storedGeneration int64
	var storedDigest, storedProject, storedSource, storedState string
	var err error
	if generation == 0 {
		err = s.db.QueryRowContext(ctx, `SELECT r.payload,r.generation,r.digest,r.project_id,r.source_session_id,r.state FROM session_recovery_revisions r JOIN session_recovery_heads h ON h.recovery_id=r.recovery_id AND h.generation=r.generation WHERE r.recovery_id=?`, id).Scan(&raw, &storedGeneration, &storedDigest, &storedProject, &storedSource, &storedState)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT payload,generation,digest,project_id,source_session_id,state FROM session_recovery_revisions WHERE recovery_id=? AND generation=?`, id, generation).Scan(&raw, &storedGeneration, &storedDigest, &storedProject, &storedSource, &storedState)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return model.SessionRecovery{}, model.ErrNotFound
	}
	if err != nil {
		return model.SessionRecovery{}, err
	}
	r, decodeErr := decodeSessionRecovery(raw)
	if decodeErr != nil || r.ID != id || r.Generation != storedGeneration || r.Digest != storedDigest || r.Binding.ProjectID != storedProject || r.SourceSessionID != storedSource || r.State != storedState {
		return model.SessionRecovery{}, fmt.Errorf("%w: corrupt recovery revision", model.ErrInvalid)
	}
	return r, nil
}

func decodeSessionRecovery(raw string) (model.SessionRecovery, error) {
	var r model.SessionRecovery
	if len(raw) > 65536 {
		return r, model.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader([]byte(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, model.ErrInvalid
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF || r.Validate() != nil {
		return model.SessionRecovery{}, model.ErrInvalid
	}
	return r, nil
}

func (s *Store) ListSessionRecoveries(ctx context.Context, project string) ([]model.SessionRecovery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.payload,r.generation,r.digest,r.source_session_id FROM session_recovery_revisions r JOIN session_recovery_heads h ON h.recovery_id=r.recovery_id AND h.generation=r.generation WHERE r.project_id=? AND r.state='recoverable' ORDER BY r.updated_at DESC LIMIT 100`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.SessionRecovery{}
	for rows.Next() {
		var raw string
		var generation int64
		var digest, source string
		if err = rows.Scan(&raw, &generation, &digest, &source); err != nil {
			return nil, err
		}
		r, decodeErr := decodeSessionRecovery(raw)
		if decodeErr != nil || r.Binding.ProjectID != project || r.Generation != generation || r.Digest != digest || r.State != "recoverable" || r.SourceSessionID != source {
			return nil, model.ErrInvalid
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// checkRecoveryGoal rechecks the Goal head, cancellation and active policy in
// the same write transaction as adoption/checkpoint and its command receipt.
func checkRecoveryGoal(ctx context.Context, tx *sql.Tx, r model.SessionRecovery) error {
	b := r.Binding
	var id, project, intake string
	var rev int64
	err := tx.QueryRowContext(ctx, `SELECT a.active_goal_id,a.active_revision,g.project_id,g.intake_json FROM goal_active a JOIN goal_contracts g ON g.goal_id=a.active_goal_id AND g.revision=a.active_revision WHERE a.session_id=?`, r.SourceSessionID).Scan(&id, &rev, &project, &intake)
	if err != nil {
		return err
	}
	if id != b.GoalID || rev != b.GoalRevision || project != b.ProjectID {
		return model.ErrConflict
	}
	var p goalIntakePayload
	if json.Unmarshal([]byte(intake), &p) != nil {
		return model.ErrInvalid
	}
	if p.Confirmation == string(model.ConfirmationCancelled) || p.RequestDigest != b.OriginalIntentDigest || p.ConstitutionVersion != b.ConstitutionVersion || model.SessionRecoveryDigest(p.BudgetLimits) != b.GoalBudgetDigest {
		return model.ErrConflict
	}
	var terminal int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM goal_terminations WHERE session_id=? AND goal_id=? AND state='CANCELLED'`, r.SourceSessionID, b.GoalID).Scan(&terminal); err != nil {
		return err
	}
	if terminal != 0 {
		return model.ErrConflict
	}
	var policyID, digest string
	var version, count int64
	err = tx.QueryRowContext(ctx, `SELECT count(*),coalesce(max(policy_id),''),coalesce(max(version),0),coalesce(max(digest),'') FROM policy_versions WHERE state='active'`).Scan(&count, &policyID, &version, &digest)
	if err != nil {
		return err
	}
	if count == 0 {
		if b.PolicyDigest != "none" || b.PolicyID != "" {
			return model.ErrConflict
		}
		return nil
	}
	if err != nil {
		return err
	}
	if count != 1 || policyID != b.PolicyID || version != b.PolicyVersion || digest != b.PolicyDigest {
		return model.ErrConflict
	}
	return nil
}

// ValidateSessionRecovery is a read-only transaction for replay validation.
func (s *Store) ValidateSessionRecovery(ctx context.Context, r model.SessionRecovery) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRecoveryGoal(ctx, tx, r); err != nil {
		return err
	}
	if err = checkRecoveryMemory(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ValidateSessionRecoveryCommand(ctx context.Context, r model.SessionRecovery, command CommandRecord) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkRecoveryGrant(ctx, tx, command); err != nil {
		return err
	}
	if err = checkRecoveryGoal(ctx, tx, r); err != nil {
		return err
	}
	if err = checkRecoveryMemory(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit()
}

func checkRecoveryGrant(ctx context.Context, tx *sql.Tx, r CommandRecord) error {
	if r.CapabilityGrantID == "" {
		return model.ErrInvalid
	}
	var expiry string
	err := tx.QueryRowContext(ctx, `SELECT expires_at FROM capability_grants WHERE id=? AND subject=? AND task_id=? AND revoked_at IS NULL`, r.CapabilityGrantID, r.Actor, r.ProjectID).Scan(&expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrConflict
	}
	if err != nil {
		return err
	}
	expires, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !time.Now().UTC().Before(expires) {
		return model.ErrConflict
	}
	return nil
}

func checkRecoveryMemory(ctx context.Context, tx *sql.Tx, r model.SessionRecovery) error {
	for _, ref := range r.Memory {
		var rev int64
		var digest, life, superseded, conflicts, evidence string
		var validTo sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT revision,content_digest,lifecycle,superseded_by,conflict_ids,evidence_ids,valid_to FROM memory_records_v2 WHERE project_id=? AND memory_id=?`, r.Binding.ProjectID, ref.ID).Scan(&rev, &digest, &life, &superseded, &conflicts, &evidence, &validTo)
		if err != nil {
			return err
		}
		if rev != ref.Revision || digest != ref.Digest || superseded != "" || conflicts != "" || (life != string(model.MemoryCandidate) && life != string(model.MemoryVerified) && life != string(model.MemoryDurable)) {
			return model.ErrConflict
		}
		if validTo.Valid {
			until, e := time.Parse(time.RFC3339Nano, validTo.String)
			if e != nil || !time.Now().UTC().Before(until) {
				return model.ErrConflict
			}
		}
		if evidence != "" {
			for _, id := range strings.Split(evidence, ",") {
				var state string
				if err = tx.QueryRowContext(ctx, `SELECT state FROM evidence_nodes WHERE node_id=?`, id).Scan(&state); err != nil {
					return err
				}
				if state != "stored" && state != "linked" {
					return model.ErrConflict
				}
			}
		}
	}
	return nil
}

// WriteSessionRecovery requires the application's authorized command context.
// The optional lastCheck runs after SQLite acquires the write lock. It must
// only check external observations, never query this Store (one connection).
// Adoption requires lastCheck; absence fails closed.
func (s *Store) WriteSessionRecovery(ctx context.Context, next model.SessionRecovery, expected int64, expectedDigest string, lastCheck func(context.Context) error) error {
	command, ok := ctx.Value(commandKey{}).(CommandRecord)
	if !ok || command.Operation != "session.recover" || command.CapabilityGrantID == "" || command.TargetID != next.ID || command.ExpectedVersion != expected || command.ProjectID != next.Binding.ProjectID || command.SessionID != next.SourceSessionID {
		return model.ErrInvalid
	}
	if next.Generation != expected+1 || next.Validate() != nil {
		return model.ErrInvalid
	}
	for attempt := 0; attempt < sqliteBusyRetries; attempt++ {
		err := s.writeSessionRecoveryOnce(ctx, next, expected, expectedDigest, lastCheck)
		if !isSQLiteBusy(err) {
			return err
		}
		if err = waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
	return model.ErrConflict
}

func (s *Store) writeSessionRecoveryOnce(ctx context.Context, next model.SessionRecovery, expected int64, expectedDigest string, lastCheck func(context.Context) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Acquire the writer lock before reading the head (avoids a deferred
	// transaction's read-to-write upgrade racing another adopter).
	if _, err = tx.ExecContext(ctx, `UPDATE session_recovery_heads SET generation=generation WHERE recovery_id=?`, next.ID); err != nil {
		return err
	}
	var generation int64
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT h.generation,r.payload FROM session_recovery_heads h JOIN session_recovery_revisions r ON r.recovery_id=h.recovery_id AND r.generation=h.generation WHERE h.recovery_id=?`, next.ID).Scan(&generation, &raw)
	if expected == 0 {
		if !errors.Is(err, sql.ErrNoRows) {
			if err != nil {
				return err
			}
			return model.ErrConflict
		}
		if next.State != "recoverable" || expectedDigest != "" {
			return model.ErrInvalid
		}
	} else {
		if err != nil {
			return err
		}
		old, decodeErr := decodeSessionRecovery(raw)
		if decodeErr != nil || old.Generation != generation || old.ID != next.ID {
			return model.ErrInvalid
		}
		if generation != expected || old.Digest != expectedDigest || old.State != "recoverable" || old.SourceSessionID != next.SourceSessionID || old.Binding.ProjectID != next.Binding.ProjectID {
			return model.ErrConflict
		}
		if next.State == "adopted" && (next.Binding != old.Binding || lastCheck == nil || next.AdoptedSessionID == next.SourceSessionID) {
			return model.ErrConflict
		}
	}
	if err = checkRecoveryGoal(ctx, tx, next); err != nil {
		return err
	}
	if err = checkRecoveryMemory(ctx, tx, next); err != nil {
		return err
	}
	if lastCheck != nil {
		if err = lastCheck(ctx); err != nil {
			return err
		}
	}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO session_recovery_revisions(recovery_id,generation,project_id,source_session_id,state,digest,payload,updated_at) VALUES(?,?,?,?,?,?,?,?)`, next.ID, next.Generation, next.Binding.ProjectID, next.SourceSessionID, next.State, next.Digest, string(data), next.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if expected == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO session_recovery_heads(recovery_id,generation) VALUES(?,?)`, next.ID, next.Generation)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE session_recovery_heads SET generation=? WHERE recovery_id=? AND generation=?`, next.Generation, next.ID, expected)
	}
	if err != nil {
		return err
	}
	r := ctx.Value(commandKey{}).(CommandRecord)
	if err = insertCommandReceipt(ctx, tx, r, next.Generation); err != nil {
		return err
	}
	return tx.Commit()
}
