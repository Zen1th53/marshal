package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
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

type ReconcileResult struct {
	ReconciledTasks    int `json:"reconciled_tasks"`
	TerminatedSessions int `json:"terminated_sessions"`
	FailedWorkerRuns   int `json:"failed_worker_runs"`
}

func (s *Store) ReconcileStartupOrphans(ctx context.Context) (ReconcileResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("begin startup reconciliation: %w", err)
	}
	defer tx.Rollback()

	projectID, err := currentProjectID(ctx, tx)
	if err != nil {
		return ReconcileResult{}, err
	}

	now := utcNow()
	res := ReconcileResult{}

	// 1. Terminate running worker runs with no active lease
	wrRes, err := tx.ExecContext(ctx, `
		UPDATE worker_runs
		SET exit_status = 137, status = 'failed', ended_at = ?, revision = revision + 1
		WHERE ended_at IS NULL AND session_id NOT IN (
			SELECT session_id FROM leases WHERE status = 'active' AND expires_at >= ?
		)
	`, now, now)
	if err == nil {
		n, _ := wrRes.RowsAffected()
		res.FailedWorkerRuns = int(n)
	}

	// 2. Mark active sessions with no active unexpired lease as stale
	sessRes, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET status = 'stale', revision = revision + 1
		WHERE status = 'active' AND session_id NOT IN (
			SELECT session_id FROM leases WHERE status = 'active' AND expires_at >= ?
		)
	`, now)
	if err == nil {
		n, _ := sessRes.RowsAffected()
		res.TerminatedSessions = int(n)
	}

	// 3. Mark expired active leases as released
	leaseRes, err := tx.ExecContext(ctx, `
		UPDATE leases
		SET status = 'released', revision = revision + 1
		WHERE status = 'active' AND expires_at < ?
	`, now)
	if err == nil {
		n, _ := leaseRes.RowsAffected()
		res.ReconciledTasks = int(n)
	}

	// 4. Release tasks that were claimed/working with expired or missing leases
	_, _ = tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = 'ready', owner_agent_id = NULL, revision = revision + 1, updated_at = ?
		WHERE status IN ('claimed', 'working') AND task_id NOT IN (
			SELECT task_id FROM leases WHERE status = 'active' AND expires_at >= ?
		)
	`, now, now)

	if res.FailedWorkerRuns > 0 || res.TerminatedSessions > 0 || res.ReconciledTasks > 0 {
		eventID, err := model.NewID("EVENT-")
		if err == nil {
			_ = s.AppendEvent(ctx, tx, model.Event{
				ID:                eventID,
				Type:              "STARTUP_RECONCILIATION",
				ProjectID:         projectID,
				AggregateRevision: 0,
				Timestamp:         time.Now().UTC(),
				Data: map[string]any{
					"reconciled_tasks":    res.ReconciledTasks,
					"terminated_sessions": res.TerminatedSessions,
					"failed_worker_runs":  res.FailedWorkerRuns,
				},
			})
		}
	}

	if err := tx.Commit(); err != nil {
		return ReconcileResult{}, fmt.Errorf("commit startup reconciliation: %w", err)
	}

	return res, nil
}
