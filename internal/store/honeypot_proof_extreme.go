//go:build !governed

package store

import (
	"context"
	"errors"
)

// HoneypotProof is a selected durable disposition, never scanner contents.
type HoneypotProof struct{ EventID, ProjectID, TaskID, ScanID, ResultCommit, Outcome, Timestamp string }

func (s *Store) HoneypotScanProofs(ctx context.Context, scan, commit string) ([]HoneypotProof, error) {
	if scan == "" || commit == "" {
		return nil, errors.New("missing scan proof binding")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event_id,COALESCE(project_id,''),CASE WHEN length(data_json)<=8192 THEN json_extract(data_json,'$.task_id') END,CASE WHEN length(data_json)<=8192 THEN json_extract(data_json,'$.scan_id') END,CASE WHEN length(data_json)<=8192 THEN json_extract(data_json,'$.result_commit') END,CASE WHEN length(data_json)<=8192 THEN json_extract(data_json,'$.outcome') END,timestamp FROM audit_events WHERE event_type='HONEYPOT_SCAN' AND json_valid(data_json) AND json_extract(data_json,'$.scan_id')=? AND json_extract(data_json,'$.result_commit')=? ORDER BY timestamp,event_id LIMIT 257`, scan, commit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []HoneypotProof
	for rows.Next() {
		var p HoneypotProof
		if err = rows.Scan(&p.EventID, &p.ProjectID, &p.TaskID, &p.ScanID, &p.ResultCommit, &p.Outcome, &p.Timestamp); err != nil {
			return nil, err
		}
		if len(p.EventID)+len(p.ProjectID)+len(p.TaskID)+len(p.ScanID)+len(p.ResultCommit)+len(p.Outcome)+len(p.Timestamp) > 8192 {
			return nil, errors.New("oversized scan proof")
		}
		result = append(result, p)
		if len(result) > 256 {
			return nil, errors.New("too many scan proofs")
		}
	}
	return result, rows.Err()
}
