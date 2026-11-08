//go:build !governed

package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// ProjectContractTaskScopes returns canonical task scope IDs from this exact
// storage project. It is data for authenticated owner inspection, never a task
// execution grant. ListTasks cannot serve this purpose: it omits project IDs.
func (s *Store) ProjectContractTaskScopes(ctx context.Context, project string) ([]string, error) {
	if project == "" {
		return nil, projectcontext.ErrInvalid
	}
	// Discover only accepted memory task scopes, not the project's entire
	// historical task inventory or optional candidate memory population.
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT m.scope_id FROM memory_records_v2 m
	WHERE m.project_id=? AND m.lifecycle IN ('durable','verified') AND m.authority IN ('operator','policy')
	AND m.scope='task' AND EXISTS(SELECT 1 FROM tasks t WHERE t.task_id=m.scope_id AND t.project_id=m.project_id)
	ORDER BY m.scope_id LIMIT ?`, project, maxProjectContextRefs+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if len(ids) > maxProjectContextRefs {
		return nil, projectcontext.ErrBudget
	}
	return ids, rows.Err()
}

// ReadProjectContractOwnerMemory authorizes the exact row in the same short
// transaction as body retrieval. It does not rank or scan all memory bodies.
func (s *Store) ReadProjectContractOwnerMemory(ctx context.Context, project, actor, id string) (model.MemoryRecordV2, error) {
	if project == "" || actor == "" || id == "" {
		return model.MemoryRecordV2{}, authz.ErrUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	defer tx.Rollback()
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_records_v2 m WHERE m.project_id=? AND m.memory_id=? AND (m.acl_scope='' OR m.acl_scope=?)
	AND (m.scope_id=? OR m.scope_id=? OR (m.scope='task' AND EXISTS(SELECT 1 FROM tasks t WHERE t.project_id=m.project_id AND t.task_id=m.scope_id))))`, project, id, actor, project, actor).Scan(&allowed)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if !allowed {
		return model.MemoryRecordV2{}, authz.ErrUnauthorized
	}
	return getMemoryV2(ctx, tx, project, id)
}

// ReadProjectContextTask checks the canonical storage-project boundary rather
// than trusting a globally shaped task ID or a caller worktree path.
func (s *Store) ReadProjectContextTask(ctx context.Context, project projectcontext.ProjectBinding, id string) (model.Task, error) {
	if project.CanonicalID == "" || project.StorageID == "" || id == "" {
		return model.Task{}, projectcontext.ErrInvalid
	}
	row := s.db.QueryRowContext(ctx, `SELECT task_id, title, status, risk, owner_agent_id, branch, worktree,
	base_commit, head_commit, revision FROM tasks WHERE project_id=? AND task_id=?`, project.StorageID, id)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Task{}, model.ErrNotFound
	}
	if err != nil {
		return model.Task{}, err
	}
	if err = s.taskControl(ctx, &task); err != nil {
		return model.Task{}, err
	}
	task.Dependencies, err = s.taskDependencies(ctx, id)
	return task, err
}
