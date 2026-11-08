//go:build !governed

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

// securityUnconfinedScan bounds how many of the newest policy.allowed
// events are looked at for an unconfined lane.
const securityUnconfinedScan = 50

// securityUnconfinedDecision is what an Extreme lane that ran without
// enforceable confinement records.
const securityUnconfinedDecision = "ran unconfined on the host"

// securityUnconfined reads the newest lane that ran unconfined on the host
// through the (event_type, sequence) index.
func (s *Store) securityUnconfined(ctx context.Context) (*SecurityUnconfined, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT subject, task_id, at, data_json FROM structured_events INDEXED BY structured_events_by_type_sequence
		WHERE event_type = ? ORDER BY sequence DESC LIMIT ?`, string(events.EventTypePolicyAllowed), securityUnconfinedScan)
	if err != nil {
		return nil, err
	}
	var found *SecurityUnconfined
	for rows.Next() && found == nil {
		var worker, task, at, raw string
		if err := rows.Scan(&worker, &task, &at, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var data struct {
			Decision string `json:"decision"`
			Cause    string `json:"confinement_cause"`
		}
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode policy event: %w", err)
		}
		if data.Decision != securityUnconfinedDecision {
			continue
		}
		found = &SecurityUnconfined{Worker: worker, Task: task, Cause: data.Cause}
		found.At, _ = time.Parse(time.RFC3339Nano, at)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return found, nil
}
