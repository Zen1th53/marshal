package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/Zen1th53/slaves/internal/model"
)

func (s *Store) ImportTasks(ctx context.Context, tasks []model.Task) (model.ImportResult, error) {
	if len(tasks) == 0 {
		return model.ImportResult{}, fmt.Errorf("%w: no tasks to import", model.ErrInvalid)
	}
	batch := make(map[string]model.Task, len(tasks))
	for _, task := range tasks {
		if err := task.Validate(); err != nil {
			return model.ImportResult{}, err
		}
		if _, exists := batch[task.ID]; exists {
			return model.ImportResult{}, fmt.Errorf("%w: duplicate task %s", model.ErrInvalid, task.ID)
		}
		batch[task.ID] = task
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.ImportResult{}, fmt.Errorf("begin task import: %w", err)
	}
	defer tx.Rollback()
	projectID, err := currentProjectID(ctx, tx)
	if err != nil {
		return model.ImportResult{}, err
	}

	for _, task := range tasks {
		for _, dependency := range task.Dependencies {
			if _, inBatch := batch[dependency]; inBatch {
				continue
			}
			var exists int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE task_id = ? AND project_id = ?", dependency, projectID).Scan(&exists); err != nil {
				return model.ImportResult{}, fmt.Errorf("check dependency %s: %w", dependency, err)
			}
			if exists != 1 {
				return model.ImportResult{}, fmt.Errorf("%w: task %s dependency %s is missing", model.ErrConflict, task.ID, dependency)
			}
		}
	}

	result := model.ImportResult{}
	now := utcNow()
	for _, task := range tasks {
		existing, found, err := loadTaskTx(ctx, tx, projectID, task.ID)
		if err != nil {
			return model.ImportResult{}, err
		}
		if found {
			if !tasksEqual(existing, task) {
				return model.ImportResult{}, fmt.Errorf("%w: task %s has divergent content", model.ErrConflict, task.ID)
			}
			result.Matched++
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tasks(
				task_id, project_id, title, status, risk, owner_agent_id, branch,
				worktree, base_commit, head_commit, revision, created_at, updated_at
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, task.ID, projectID, task.Title, task.Status, task.Risk, task.OwnerAgentID,
			task.Branch, task.Worktree, task.BaseCommit, task.HeadCommit, task.Revision,
			now, now); err != nil {
			return model.ImportResult{}, fmt.Errorf("insert task %s: %w", task.ID, err)
		}
		result.Added++
	}
	for _, task := range tasks {
		for _, dependency := range task.Dependencies {
			if _, err := tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO task_dependencies(task_id, depends_on_task_id, kind)
				VALUES(?, ?, 'hard')
			`, task.ID, dependency); err != nil {
				return model.ImportResult{}, fmt.Errorf("insert dependency %s -> %s: %w", task.ID, dependency, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return model.ImportResult{}, fmt.Errorf("commit task import: %w", err)
	}
	return result, nil
}

func (s *Store) ReadyTasks(ctx context.Context) ([]model.Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.task_id, t.title, t.status, t.risk, t.owner_agent_id, t.branch,
		       t.worktree, t.base_commit, t.head_commit, t.revision
		FROM tasks t
		WHERE t.status = 'ready'
		  AND NOT EXISTS (
		    SELECT 1 FROM task_dependencies d
		    JOIN tasks required ON required.task_id = d.depends_on_task_id
		    WHERE d.task_id = t.task_id AND required.status <> 'merged'
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM leases l WHERE l.task_id = t.task_id AND l.status = 'active'
		  )
		ORDER BY (
		  SELECT count(*) FROM task_dependencies blocked WHERE blocked.depends_on_task_id = t.task_id
		) DESC, t.task_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query ready tasks: %w", err)
	}
	defer rows.Close()
	var tasks []model.Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ready tasks: %w", err)
	}
	for i := range tasks {
		tasks[i].Dependencies, err = s.taskDependencies(ctx, tasks[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return tasks, nil
}

func (s *Store) ClaimTask(ctx context.Context, request model.ClaimRequest) (model.Lease, error) {
	if request.TaskID == "" || request.AgentID == "" || request.SessionID == "" || !request.ExpiresAt.After(time.Now().UTC()) {
		return model.Lease{}, fmt.Errorf("%w: incomplete claim request", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Lease{}, fmt.Errorf("begin task claim: %w", err)
	}
	defer tx.Rollback()

	var sessionAgent, sessionProject, sessionStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT agent_id, project_id, status FROM sessions WHERE session_id = ?
	`, request.SessionID).Scan(&sessionAgent, &sessionProject, &sessionStatus); err != nil {
		return model.Lease{}, claimConflict("session is not available", err)
	}
	if sessionAgent != request.AgentID || sessionStatus != string(model.SessionActive) {
		return model.Lease{}, claimConflict("session identity does not match", nil)
	}

	var status string
	var revision int64
	var projectID string
	if err := tx.QueryRowContext(ctx, `
		SELECT status, revision, project_id FROM tasks WHERE task_id = ?
	`, request.TaskID).Scan(&status, &revision, &projectID); err != nil {
		return model.Lease{}, claimConflict("task is not available", err)
	}
	if projectID != sessionProject || status != string(model.TaskReady) || revision != request.ExpectedRevision {
		return model.Lease{}, claimConflict("task is not ready at expected revision", nil)
	}
	var unsatisfied int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*)
		FROM task_dependencies d
		JOIN tasks required ON required.task_id = d.depends_on_task_id
		WHERE d.task_id = ? AND required.status <> 'merged'
	`, request.TaskID).Scan(&unsatisfied); err != nil {
		return model.Lease{}, fmt.Errorf("check task dependencies: %w", err)
	}
	if unsatisfied != 0 {
		return model.Lease{}, claimConflict("task dependencies are not complete", nil)
	}
	var active int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM leases WHERE task_id = ? AND status = 'active'", request.TaskID).Scan(&active); err != nil {
		return model.Lease{}, fmt.Errorf("check active lease: %w", err)
	}
	if active != 0 {
		return model.Lease{}, claimConflict("task already has an active lease", nil)
	}

	leaseID, err := model.NewID("LEASE-")
	if err != nil {
		return model.Lease{}, err
	}
	eventID, err := model.NewID("EVENT-")
	if err != nil {
		return model.Lease{}, err
	}
	acquiredAt := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO leases(lease_id, task_id, session_id, acquired_at, expires_at, revision, status)
		VALUES(?, ?, ?, ?, ?, 0, 'active')
	`, leaseID, request.TaskID, request.SessionID, acquiredAt.Format(time.RFC3339Nano),
		request.ExpiresAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return model.Lease{}, claimConflict("create active lease", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET owner_agent_id = ?, status = 'claimed', revision = revision + 1, updated_at = ?
		WHERE task_id = ? AND status = 'ready' AND revision = ?
	`, request.AgentID, acquiredAt.Format(time.RFC3339Nano), request.TaskID, request.ExpectedRevision)
	if err != nil {
		return model.Lease{}, fmt.Errorf("claim task: %w", err)
	}
	if err := requireOne(result, "claim task"); err != nil {
		return model.Lease{}, err
	}
	result, err = tx.ExecContext(ctx, `
		UPDATE sessions SET task_id = ?, revision = revision + 1
		WHERE session_id = ? AND status = 'active' AND task_id IS NULL
	`, request.TaskID, request.SessionID)
	if err != nil {
		return model.Lease{}, fmt.Errorf("bind claim session: %w", err)
	}
	if err := requireOne(result, "bind claim session"); err != nil {
		return model.Lease{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events(
			event_id, event_type, project_id, task_id, actor_agent_id, session_id,
			aggregate_revision, timestamp, data_json
		) VALUES(?, 'TASK_CLAIMED', ?, ?, ?, ?, ?, ?, '{}')
	`, eventID, projectID, request.TaskID, request.AgentID, request.SessionID,
		revision+1, acquiredAt.Format(time.RFC3339Nano)); err != nil {
		return model.Lease{}, fmt.Errorf("record task claim event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Lease{}, fmt.Errorf("commit task claim: %w", err)
	}
	return model.Lease{
		ID: leaseID, TaskID: request.TaskID, SessionID: request.SessionID,
		AcquiredAt: acquiredAt, ExpiresAt: request.ExpiresAt.UTC(), Status: "active",
	}, nil
}

func (s *Store) ReleaseTask(ctx context.Context, request model.ReleaseRequest) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin task release: %w", err)
	}
	defer tx.Rollback()
	var leaseSession, status, owner string
	var revision int64
	var projectID string
	err = tx.QueryRowContext(ctx, `
		SELECT l.session_id, l.status, COALESCE(t.owner_agent_id, ''), t.revision, t.project_id
		FROM leases l JOIN tasks t ON t.task_id = l.task_id
		WHERE l.lease_id = ? AND l.task_id = ?
	`, request.LeaseID, request.TaskID).Scan(&leaseSession, &status, &owner, &revision, &projectID)
	if err != nil || status != "active" || leaseSession != request.SessionID ||
		owner != request.AgentID || revision != request.ExpectedRevision {
		return fmt.Errorf("%w: task release identity or revision mismatch", model.ErrConflict)
	}
	now := utcNow()
	if _, err := tx.ExecContext(ctx, "UPDATE leases SET status = 'released', revision = revision + 1 WHERE lease_id = ? AND status = 'active'", request.LeaseID); err != nil {
		return fmt.Errorf("release lease: %w", err)
	}
	nextStatus := model.TaskReady
	if request.BlockedReason != "" {
		nextStatus = model.TaskBlocked
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET owner_agent_id = NULL, status = ?, revision = revision + 1, updated_at = ?
		WHERE task_id = ? AND revision = ?
	`, nextStatus, now, request.TaskID, request.ExpectedRevision)
	if err != nil {
		return fmt.Errorf("release task: %w", err)
	}
	if err := requireOne(result, "release task"); err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, "UPDATE sessions SET task_id = NULL, revision = revision + 1 WHERE session_id = ? AND task_id = ?", request.SessionID, request.TaskID)
	if err != nil {
		return fmt.Errorf("unbind release session: %w", err)
	}
	if err := requireOne(result, "unbind release session"); err != nil {
		return err
	}
	eventID, err := model.NewID("EVENT-")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events(
			event_id, event_type, project_id, task_id, actor_agent_id, session_id,
			aggregate_revision, timestamp, data_json
		) VALUES(?, 'TASK_RELEASED', ?, ?, ?, ?, ?, ?, '{}')
	`, eventID, projectID, request.TaskID, request.AgentID, request.SessionID,
		revision+1, now); err != nil {
		return fmt.Errorf("record task release event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit task release: %w", err)
	}
	return nil
}

func currentProjectID(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT project_id FROM projects ORDER BY project_id")
	if err != nil {
		return "", fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("scan project: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate projects: %w", err)
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("%w: expected one local project, found %d", model.ErrConflict, len(ids))
	}
	return ids[0], nil
}

func loadTaskTx(ctx context.Context, tx *sql.Tx, projectID, taskID string) (model.Task, bool, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT task_id, title, status, risk, owner_agent_id, branch, worktree,
		       base_commit, head_commit, revision
		FROM tasks WHERE project_id = ? AND task_id = ?
	`, projectID, taskID)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Task{}, false, nil
	}
	if err != nil {
		return model.Task{}, false, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT depends_on_task_id FROM task_dependencies WHERE task_id = ? ORDER BY depends_on_task_id", taskID)
	if err != nil {
		return model.Task{}, false, fmt.Errorf("query task dependencies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var dependency string
		if err := rows.Scan(&dependency); err != nil {
			return model.Task{}, false, fmt.Errorf("scan task dependency: %w", err)
		}
		task.Dependencies = append(task.Dependencies, dependency)
	}
	return task, true, rows.Err()
}

type taskScanner interface {
	Scan(...any) error
}

func scanTask(scanner taskScanner) (model.Task, error) {
	var task model.Task
	var status, risk string
	var owner, branch, worktree, base, head sql.NullString
	if err := scanner.Scan(&task.ID, &task.Title, &status, &risk, &owner, &branch,
		&worktree, &base, &head, &task.Revision); err != nil {
		return model.Task{}, err
	}
	task.Status = model.TaskStatus(status)
	task.Risk = model.Risk(risk)
	task.OwnerAgentID = nullStringPointer(owner)
	task.Branch = nullStringPointer(branch)
	task.Worktree = nullStringPointer(worktree)
	task.BaseCommit = nullStringPointer(base)
	task.HeadCommit = nullStringPointer(head)
	return task, nil
}

func (s *Store) taskDependencies(ctx context.Context, taskID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT depends_on_task_id FROM task_dependencies WHERE task_id = ? ORDER BY depends_on_task_id", taskID)
	if err != nil {
		return nil, fmt.Errorf("query task dependencies: %w", err)
	}
	defer rows.Close()
	var dependencies []string
	for rows.Next() {
		var dependency string
		if err := rows.Scan(&dependency); err != nil {
			return nil, fmt.Errorf("scan task dependency: %w", err)
		}
		dependencies = append(dependencies, dependency)
	}
	return dependencies, rows.Err()
}

func tasksEqual(left, right model.Task) bool {
	left.Dependencies = append([]string(nil), left.Dependencies...)
	right.Dependencies = append([]string(nil), right.Dependencies...)
	sort.Strings(left.Dependencies)
	sort.Strings(right.Dependencies)
	return reflect.DeepEqual(left, right)
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func claimConflict(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", model.ErrConflict, message)
	}
	return fmt.Errorf("%w: %s: %v", model.ErrConflict, message, cause)
}
