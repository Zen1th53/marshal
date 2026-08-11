package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/slaves/internal/model"
)

func (s *Store) BeginExecution(ctx context.Context, taskID, sessionID, agentID, branch, worktree, baseCommit string, expectedRevision int64) error {
	if taskID == "" || sessionID == "" || agentID == "" || branch == "" || worktree == "" || baseCommit == "" {
		return fmt.Errorf("%w: incomplete execution identity", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM tasks t
		JOIN leases l ON l.task_id=t.task_id AND l.status='active'
		JOIN sessions s ON s.session_id=l.session_id
		WHERE t.task_id=? AND t.status='claimed' AND t.revision=? AND t.owner_agent_id=?
		  AND s.session_id=? AND s.status='active'
	`, taskID, expectedRevision, agentID, sessionID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%w: execution does not own the claimed task", model.ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status='working', branch=?, worktree=?, base_commit=?, head_commit=?,
		 revision=revision+1, updated_at=? WHERE task_id=? AND revision=?
	`, branch, worktree, baseCommit, baseCommit, time.Now().UTC().Format(time.RFC3339Nano), taskID, expectedRevision)
	if err != nil {
		return err
	}
	if err := requireOne(result, "begin execution"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FinalizeExecution(ctx context.Context, taskID, sessionID string, success bool, expectedRevision int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var leaseID, projectID, agentID, sessionStatus string
	err = tx.QueryRowContext(ctx, `
		SELECT l.lease_id, t.project_id, s.agent_id, s.status
		FROM tasks t JOIN leases l ON l.task_id=t.task_id AND l.status='active'
		JOIN sessions s ON s.session_id=l.session_id
		WHERE t.task_id=? AND t.revision=? AND s.session_id=?
	`, taskID, expectedRevision, sessionID).Scan(&leaseID, &projectID, &agentID, &sessionStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: execution finalization identity mismatch", model.ErrConflict)
	}
	if err != nil {
		return err
	}
	status, eventType := model.TaskBlocked, "TASK_BLOCKED"
	if success {
		status, eventType = model.TaskReview, "TASK_RELEASED"
	}
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET status=?, owner_agent_id=NULL, revision=revision+1, updated_at=? WHERE task_id=? AND revision=?`, status, time.Now().UTC().Format(time.RFC3339Nano), taskID, expectedRevision)
	if err != nil {
		return err
	}
	if err := requireOne(result, "finalize execution"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE leases SET status='released', revision=revision+1 WHERE lease_id=? AND status='active'`, leaseID); err != nil {
		return err
	}
	if sessionStatus == string(model.SessionActive) {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET task_id=NULL, status='terminated', revision=revision+1 WHERE session_id=? AND status='active'`, sessionID); err != nil {
			return err
		}
	}
	eventID, err := model.NewID("EVENT-")
	if err != nil {
		return err
	}
	if err := s.AppendEvent(ctx, tx, model.Event{ID: eventID, Type: eventType, ProjectID: projectID, TaskID: taskID,
		ActorAgentID: agentID, SessionID: sessionID, AggregateRevision: expectedRevision + 1,
		Timestamp: time.Now().UTC(), Data: map[string]any{"execution_success": success}}); err != nil {
		return err
	}
	return tx.Commit()
}
