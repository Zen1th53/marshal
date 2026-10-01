package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// ImportedConversation contains identity metadata only, never transcript text.
type ImportedConversation struct {
	SourceID, Provider string
	UpdatedAt          time.Time
}

func (s *Store) ImportedConversations(ctx context.Context, projectID string) ([]ImportedConversation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, ext_meta_json, updated_at FROM memory_records_v2 WHERE project_id = ? AND session_id <> '' AND json_extract(ext_meta_json, '$.imported_retroactive') = 1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportedConversation
	for rows.Next() {
		var id, meta, updated string
		if err := rows.Scan(&id, &meta, &updated); err != nil {
			return nil, err
		}
		var fields struct {
			Provider string `json:"provider"`
		}
		if err := json.Unmarshal([]byte(meta), &fields); err != nil {
			return nil, err
		}
		stamp, err := time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, err
		}
		out = append(out, ImportedConversation{SourceID: id, Provider: fields.Provider, UpdatedAt: stamp})
	}
	return out, rows.Err()
}

func (s *Store) IsGovernedSessionID(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM worker_runs WHERE run_id = ? OR session_id = ? OR task_id = ?`, id, id, id).Scan(&count)
	return count > 0, err
}

// SessionInventoryRuns preserves project ownership and includes finished runs.
func (s *Store) SessionInventoryRuns(ctx context.Context, projectID, adapterName string) ([]model.WorkerRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.run_id, w.task_id, w.session_id, w.adapter, w.adapter_version, w.status, w.started_at, w.ended_at, w.exit_status
		FROM worker_runs w JOIN sessions s ON s.session_id = w.session_id
		WHERE s.project_id = ? AND (? = '' OR w.adapter = ?)
		ORDER BY w.started_at DESC
	`, projectID, adapterName, adapterName)
	if err != nil {
		return nil, fmt.Errorf("query worker runs: %w", err)
	}
	defer rows.Close()

	var runs []model.WorkerRun
	for rows.Next() {
		var r model.WorkerRun
		var startedAtStr string
		var endedAtStr sql.NullString
		if err := rows.Scan(&r.ID, &r.TaskID, &r.SessionID, &r.Adapter, &r.AdapterVersion, &r.Status, &startedAtStr, &endedAtStr, &r.ExitStatus); err != nil {
			return nil, fmt.Errorf("scan worker run: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, startedAtStr); err == nil {
			r.StartedAt = t
		}
		if endedAtStr.Valid {
			if t, err := time.Parse(time.RFC3339Nano, endedAtStr.String); err == nil {
				r.EndedAt = &t
			}
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}
