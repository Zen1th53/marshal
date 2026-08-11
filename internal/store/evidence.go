package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"time"

	"github.com/Zen1th53/slaves/internal/model"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (s *Store) AppendEvent(ctx context.Context, tx *sql.Tx, event model.Event) error {
	if event.ID == "" || event.Type == "" || event.Timestamp.IsZero() || event.AggregateRevision < 0 {
		return fmt.Errorf("%w: incomplete event", model.ErrInvalid)
	}
	if event.Data == nil {
		event.Data = map[string]any{}
	}
	data, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("%w: encode event data: %v", model.ErrInvalid, err)
	}
	timestamp := event.Timestamp.UTC().Format(time.RFC3339Nano)

	var storedType, storedTimestamp, storedData string
	var projectID, taskID, actorID, sessionID sql.NullString
	var revision int64
	err = tx.QueryRowContext(ctx, `
		SELECT event_type, project_id, task_id, actor_agent_id, session_id,
		       aggregate_revision, timestamp, data_json
		FROM audit_events WHERE event_id = ?
	`, event.ID).Scan(&storedType, &projectID, &taskID, &actorID, &sessionID,
		&revision, &storedTimestamp, &storedData)
	switch {
	case err == nil:
		if storedType == event.Type && projectID.String == event.ProjectID &&
			taskID.String == event.TaskID && actorID.String == event.ActorAgentID &&
			sessionID.String == event.SessionID && revision == event.AggregateRevision &&
			storedTimestamp == timestamp && storedData == string(data) {
			return nil
		}
		return fmt.Errorf("%w: event ID %s has different payload", model.ErrConflict, event.ID)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events(
			event_id, event_type, project_id, task_id, actor_agent_id, session_id,
			aggregate_revision, timestamp, data_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.Type, nullIfEmpty(event.ProjectID), nullIfEmpty(event.TaskID),
		nullIfEmpty(event.ActorAgentID), nullIfEmpty(event.SessionID),
		event.AggregateRevision, timestamp, string(data)); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func (s *Store) RegisterArtifact(ctx context.Context, artifact model.Artifact) error {
	if artifact.ID == "" || artifact.ProjectID == "" || artifact.Kind == "" ||
		!digestPattern.MatchString(artifact.Digest) || artifact.SourceCommit == "" ||
		artifact.Path == "" || artifact.Size < 0 || artifact.CreatedAt.IsZero() {
		return fmt.Errorf("%w: incomplete artifact metadata", model.ErrInvalid)
	}
	taskIDs, err := json.Marshal(nonNilStrings(artifact.TaskIDs))
	if err != nil {
		return fmt.Errorf("%w: encode artifact tasks: %v", model.ErrInvalid, err)
	}
	verificationRefs, err := json.Marshal(nonNilStrings(artifact.VerificationRefs))
	if err != nil {
		return fmt.Errorf("%w: encode artifact verification refs: %v", model.ErrInvalid, err)
	}
	existing, err := s.GetArtifact(ctx, artifact.ID)
	if err == nil {
		if reflect.DeepEqual(existing, artifact) {
			return nil
		}
		return fmt.Errorf("%w: artifact ID %s has different metadata", model.ErrConflict, artifact.ID)
	}
	if !errors.Is(err, model.ErrNotFound) {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO artifacts(
			artifact_id, project_id, kind, digest, source_commit,
			producer_session_id, task_ids_json, verification_refs_json,
			path, size_bytes, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, artifact.ID, artifact.ProjectID, artifact.Kind, artifact.Digest,
		artifact.SourceCommit, nullIfEmpty(artifact.ProducerSession),
		string(taskIDs), string(verificationRefs), artifact.Path, artifact.Size,
		artifact.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("register artifact: %w", err)
	}
	return nil
}

func (s *Store) GetArtifact(ctx context.Context, artifactID string) (model.Artifact, error) {
	var artifact model.Artifact
	var producer sql.NullString
	var taskIDs, verificationRefs, createdAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT artifact_id, project_id, kind, digest, source_commit,
		       producer_session_id, task_ids_json, verification_refs_json,
		       path, size_bytes, created_at
		FROM artifacts WHERE artifact_id = ?
	`, artifactID).Scan(&artifact.ID, &artifact.ProjectID, &artifact.Kind,
		&artifact.Digest, &artifact.SourceCommit, &producer, &taskIDs,
		&verificationRefs, &artifact.Path, &artifact.Size, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Artifact{}, fmt.Errorf("%w: artifact %s", model.ErrNotFound, artifactID)
	}
	if err != nil {
		return model.Artifact{}, fmt.Errorf("read artifact: %w", err)
	}
	artifact.ProducerSession = producer.String
	if err := json.Unmarshal([]byte(taskIDs), &artifact.TaskIDs); err != nil {
		return model.Artifact{}, fmt.Errorf("decode artifact tasks: %w", err)
	}
	if err := json.Unmarshal([]byte(verificationRefs), &artifact.VerificationRefs); err != nil {
		return model.Artifact{}, fmt.Errorf("decode artifact verification refs: %w", err)
	}
	artifact.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return model.Artifact{}, fmt.Errorf("parse artifact creation: %w", err)
	}
	return artifact, nil
}

func (s *Store) RecordVerification(ctx context.Context, verification model.Verification) error {
	if verification.ID == "" || verification.TaskID == "" || verification.Commit == "" ||
		!digestPattern.MatchString(verification.OutputDigest) || verification.CreatedAt.IsZero() {
		return fmt.Errorf("%w: incomplete verification", model.ErrInvalid)
	}
	if verification.Valid && verification.ExitStatus != 0 {
		return fmt.Errorf("%w: failed command cannot create valid verification", model.ErrInvalid)
	}
	command, err := json.Marshal(nonNilStrings(verification.Command))
	if err != nil {
		return fmt.Errorf("%w: encode verification command: %v", model.ErrInvalid, err)
	}
	valid := 0
	if verification.Valid {
		valid = 1
	}
	var invalidatedAt any
	if verification.InvalidatedAt != nil {
		invalidatedAt = verification.InvalidatedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO verifications(
			verification_id, task_id, session_id, commit_hash, command_json,
			exit_status, output_digest, valid, created_at, invalidated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, verification.ID, verification.TaskID, nullIfEmpty(verification.SessionID),
		verification.Commit, string(command), verification.ExitStatus,
		verification.OutputDigest, valid,
		verification.CreatedAt.UTC().Format(time.RFC3339Nano), invalidatedAt)
	if err != nil {
		return fmt.Errorf("record verification: %w", err)
	}
	return nil
}

func (s *Store) ObserveHEAD(ctx context.Context, taskID, newCommit string, expectedRevision int64) error {
	if taskID == "" || newCommit == "" {
		return fmt.Errorf("%w: task and new HEAD are required", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin HEAD observation: %w", err)
	}
	defer tx.Rollback()
	var oldHead sql.NullString
	var revision int64
	var projectID string
	if err := tx.QueryRowContext(ctx, `
		SELECT head_commit, revision, project_id FROM tasks WHERE task_id = ?
	`, taskID).Scan(&oldHead, &revision, &projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: task %s", model.ErrNotFound, taskID)
		}
		return fmt.Errorf("read task HEAD: %w", err)
	}
	if revision != expectedRevision {
		return fmt.Errorf("%w: stale task revision", model.ErrConflict)
	}
	if oldHead.Valid && oldHead.String == newCommit {
		return tx.Commit()
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		UPDATE tasks SET head_commit = ?, revision = revision + 1, updated_at = ?
		WHERE task_id = ? AND revision = ?
	`, newCommit, now.Format(time.RFC3339Nano), taskID, expectedRevision)
	if err != nil {
		return fmt.Errorf("update task HEAD: %w", err)
	}
	if err := requireOne(result, "update task HEAD"); err != nil {
		return err
	}
	headEventID, err := model.NewID("EVENT-")
	if err != nil {
		return err
	}
	if err := s.AppendEvent(ctx, tx, model.Event{
		ID: headEventID, Type: "HEAD_CHANGED", ProjectID: projectID, TaskID: taskID,
		AggregateRevision: revision + 1, Timestamp: now,
		Data: map[string]any{"old_commit": oldHead.String, "new_commit": newCommit},
	}); err != nil {
		return err
	}
	invalidated := int64(0)
	if oldHead.Valid {
		result, err = tx.ExecContext(ctx, `
			UPDATE verifications SET valid = 0, invalidated_at = ?
			WHERE task_id = ? AND commit_hash = ? AND valid = 1
		`, now.Format(time.RFC3339Nano), taskID, oldHead.String)
		if err != nil {
			return fmt.Errorf("invalidate verification: %w", err)
		}
		invalidated, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count invalidated verifications: %w", err)
		}
	}
	if invalidated > 0 {
		eventID, err := model.NewID("EVENT-")
		if err != nil {
			return err
		}
		if err := s.AppendEvent(ctx, tx, model.Event{
			ID: eventID, Type: "VERIFICATION_INVALIDATED", ProjectID: projectID, TaskID: taskID,
			AggregateRevision: revision + 1, Timestamp: now,
			Data: map[string]any{"reason": "HEAD changed", "old_commit": oldHead.String, "new_commit": newCommit},
		}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit HEAD observation: %w", err)
	}
	return nil
}
