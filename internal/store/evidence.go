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

	"github.com/Zen1th53/marshal/internal/model"
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin artifact registration: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
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
	eventID, err := model.NewID("EVENT-")
	if err != nil {
		return err
	}
	taskID := ""
	if len(artifact.TaskIDs) > 0 {
		taskID = artifact.TaskIDs[0]
	}
	if err := s.AppendEvent(ctx, tx, model.Event{ID: eventID, Type: "ARTIFACT_REGISTERED",
		ProjectID: artifact.ProjectID, TaskID: taskID, SessionID: artifact.ProducerSession,
		Timestamp: artifact.CreatedAt, Data: map[string]any{"artifact_id": artifact.ID, "digest": artifact.Digest, "kind": artifact.Kind}}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit artifact registration: %w", err)
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

func (s *Store) StartRun(ctx context.Context, run model.WorkerRun) error {
	if run.ID == "" || run.TaskID == "" || run.SessionID == "" || run.Adapter == "" ||
		run.AdapterVersion == "" || run.BaseCommit == "" || run.StartedAt.IsZero() ||
		run.Status != "running" {
		return fmt.Errorf("%w: incomplete worker run", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin worker run: %w", err)
	}
	defer tx.Rollback()
	var projectID, agentID, sessionTask string
	if err := tx.QueryRowContext(ctx, `
		SELECT s.project_id, s.agent_id, COALESCE(s.task_id, '')
		FROM sessions s WHERE s.session_id = ?
	`, run.SessionID).Scan(&projectID, &agentID, &sessionTask); err != nil {
		return fmt.Errorf("read worker session: %w", err)
	}
	if sessionTask != "" && sessionTask != run.TaskID {
		return fmt.Errorf("%w: worker session is bound to another task", model.ErrConflict)
	}
	verification, err := json.Marshal(nonNilStrings(run.Verification))
	if err != nil {
		return fmt.Errorf("%w: encode run verification: %v", model.ErrInvalid, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO worker_runs(
			run_id, task_id, session_id, adapter, adapter_version, base_commit,
			result_commit, started_at, ended_at, exit_status, status,
			stdout_artifact_id, stderr_artifact_id, verification_json, revision
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, 'running', NULL, NULL, ?, ?)
	`, run.ID, run.TaskID, run.SessionID, run.Adapter, run.AdapterVersion,
		run.BaseCommit, nullIfEmpty(run.ResultCommit),
		run.StartedAt.UTC().Format(time.RFC3339Nano), string(verification), run.Revision); err != nil {
		return fmt.Errorf("insert worker run: %w", err)
	}
	eventID, err := model.NewID("EVENT-")
	if err != nil {
		return err
	}
	if err := s.AppendEvent(ctx, tx, model.Event{
		ID: eventID, Type: "WORKER_STARTED", ProjectID: projectID, TaskID: run.TaskID,
		ActorAgentID: agentID, SessionID: run.SessionID,
		AggregateRevision: run.Revision, Timestamp: run.StartedAt,
		Data: map[string]any{"run_id": run.ID, "adapter": run.Adapter},
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit worker run: %w", err)
	}
	return nil
}

func (s *Store) FinishRun(ctx context.Context, finish model.RunFinish) error {
	if finish.ID == "" || finish.EndedAt.IsZero() || !validRunTerminalStatus(finish.Status) {
		return fmt.Errorf("%w: incomplete worker run finish", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin worker run finish: %w", err)
	}
	defer tx.Rollback()
	var taskID, sessionID, currentStatus, projectID, agentID string
	var revision int64
	if err := tx.QueryRowContext(ctx, `
		SELECT r.task_id, r.session_id, r.status, r.revision, s.project_id, s.agent_id
		FROM worker_runs r JOIN sessions s ON s.session_id = r.session_id
		WHERE r.run_id = ?
	`, finish.ID).Scan(&taskID, &sessionID, &currentStatus, &revision, &projectID, &agentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: worker run %s", model.ErrNotFound, finish.ID)
		}
		return fmt.Errorf("read worker run: %w", err)
	}
	if currentStatus != "running" || revision != finish.ExpectedRevision {
		return fmt.Errorf("%w: worker run is not active at expected revision", model.ErrConflict)
	}
	verification, err := json.Marshal(nonNilStrings(finish.Verification))
	if err != nil {
		return fmt.Errorf("%w: encode run verification: %v", model.ErrInvalid, err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE worker_runs
		SET result_commit = ?, ended_at = ?, exit_status = ?, status = ?,
		    stdout_artifact_id = ?, stderr_artifact_id = ?, verification_json = ?,
		    revision = revision + 1
		WHERE run_id = ? AND status = 'running' AND revision = ?
	`, nullIfEmpty(finish.ResultCommit), finish.EndedAt.UTC().Format(time.RFC3339Nano),
		finish.ExitStatus, finish.Status, nullIfEmpty(finish.StdoutArtifactID),
		nullIfEmpty(finish.StderrArtifactID), string(verification), finish.ID,
		finish.ExpectedRevision)
	if err != nil {
		return fmt.Errorf("finish worker run: %w", err)
	}
	if err := requireOne(result, "finish worker run"); err != nil {
		return err
	}
	if finish.Status != "success" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE sessions SET status = 'failed', revision = revision + 1
			WHERE session_id = ? AND status = 'active'
		`, sessionID); err != nil {
			return fmt.Errorf("fail worker session: %w", err)
		}
	}
	eventID, err := model.NewID("EVENT-")
	if err != nil {
		return err
	}
	data := map[string]any{"run_id": finish.ID, "status": finish.Status}
	if finish.ExitStatus != nil {
		data["exit_status"] = *finish.ExitStatus
	}
	if err := s.AppendEvent(ctx, tx, model.Event{
		ID: eventID, Type: "WORKER_EXITED", ProjectID: projectID, TaskID: taskID,
		ActorAgentID: agentID, SessionID: sessionID,
		AggregateRevision: revision + 1, Timestamp: finish.EndedAt, Data: data,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit worker run finish: %w", err)
	}
	return nil
}

func validRunTerminalStatus(status string) bool {
	switch status {
	case "success", "failed", "timeout", "cancelled", "blocked":
		return true
	default:
		return false
	}
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
