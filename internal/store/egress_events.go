package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

// EgressControlEvents reads the durable run scopes, decisions and notifications
// in commit order. Attempts and unrelated history are not policy inputs.
func (s *Store) EgressControlEvents(ctx context.Context, runID string, after events.Sequence) ([]events.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT event_id, sequence, event_type, subject, task_id, run_id, at, data_json
 FROM structured_events WHERE sequence > ? AND (? = '' OR run_id = ?)
 AND event_type IN ('network.egress.requested','network.egress.granted','network.egress.revoked','network.egress.notification') ORDER BY sequence`, int64(after), runID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []events.Event
	for rows.Next() {
		var e events.Event
		var at, data string
		var sequence int64
		if err := rows.Scan(&e.ID, &sequence, &e.Type, &e.Subject, &e.TaskID, &e.RunID, &at, &data); err != nil {
			return nil, err
		}
		e.Sequence = events.Sequence(sequence)
		e.At, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &e.Data); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
