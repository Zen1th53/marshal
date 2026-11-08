//go:build !governed

package store

import (
	"context"
	"github.com/Zen1th53/marshal/internal/authz"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// Metadata only. A retained agent note is not accepted project policy.
type MemoryReviewEntry struct {
	ID, Title, Lifecycle, Authority, Scope, ScopeID string
	Revision                                        int64
	BodyBytes                                       int
}

// Owner review alone may inspect retained historical session DATA from this
// exact project. Contract/worker/native essential readers remain unchanged.
func (s *Store) ReadOperatorReviewMemory(ctx context.Context, project, actor, id string) (model.MemoryRecordV2, error) {
	if project == "" || actor == "" || id == "" || len(id) > 256 || strings.ContainsRune(id, 0) {
		return model.MemoryRecordV2{}, authz.ErrUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	defer tx.Rollback()
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_records_v2 m WHERE m.project_id=? AND m.memory_id=? AND (m.acl_scope='' OR m.acl_scope=?)
	AND ((m.scope='project' AND m.scope_id=?) OR (m.scope IN ('agent','operator_private') AND m.scope_id=?) OR (m.scope='session' AND m.scope_id<>'') OR (m.scope='task' AND EXISTS(SELECT 1 FROM tasks t WHERE t.project_id=m.project_id AND t.task_id=m.scope_id))))`, project, id, actor, project, actor).Scan(&allowed)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if !allowed {
		return model.MemoryRecordV2{}, authz.ErrUnauthorized
	}
	return getMemoryV2(ctx, tx, project, id)
}

type MemoryReviewPage struct {
	Entries []MemoryReviewEntry
	NextID  string
}

// Current canonical task membership is metadata DATA, never a task grant.
func (s *Store) MemoryReviewTaskScope(ctx context.Context, project, task string) (bool, error) {
	if project == "" || task == "" || len(task) > 256 || strings.ContainsRune(task, 0) {
		return false, model.ErrInvalid
	}
	var present bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tasks WHERE project_id=? AND task_id=?)", project, task).Scan(&present)
	return present, err
}

// Exact canonical pagination is independent of retrieval ranking and indexes.
// Authenticate the local reader before calling; SQL applies the actual row ACL
// and project/canonical-task boundary before selecting even metadata.
func (s *Store) ReadMemoryReviewPage(ctx context.Context, project, actor, after string, limit int) (MemoryReviewPage, error) {
	zero := MemoryReviewPage{}
	if project == "" || actor == "" || len(after) > 256 || strings.ContainsRune(after, 0) || limit < 1 || limit > 32 {
		return zero, model.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT memory_id,title,lifecycle,authority,scope,scope_id,revision,length(CAST(body AS BLOB))
	FROM memory_records_v2 m WHERE m.project_id=? AND m.memory_id>?
	AND (m.lifecycle='candidate' OR (m.authority='agent' AND m.lifecycle IN ('durable','verified')))
	AND (m.acl_scope='' OR m.acl_scope=?)
	AND ((m.scope='project' AND m.scope_id=?) OR (m.scope IN ('agent','operator_private') AND m.scope_id=?) OR (m.scope='session' AND m.scope_id<>'') OR (m.scope='task' AND EXISTS(SELECT 1 FROM tasks t WHERE t.project_id=m.project_id AND t.task_id=m.scope_id)))
	ORDER BY memory_id LIMIT ?`, project, after, actor, project, actor, limit+1)
	if err != nil {
		return zero, err
	}
	defer rows.Close()
	page := MemoryReviewPage{Entries: []MemoryReviewEntry{}}
	for rows.Next() {
		var e MemoryReviewEntry
		if err = rows.Scan(&e.ID, &e.Title, &e.Lifecycle, &e.Authority, &e.Scope, &e.ScopeID, &e.Revision, &e.BodyBytes); err != nil {
			return zero, err
		}
		if len(page.Entries) == limit {
			page.NextID = page.Entries[len(page.Entries)-1].ID
			break
		}
		page.Entries = append(page.Entries, e)
	}
	return page, rows.Err()
}
