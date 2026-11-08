package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
)

// CredentialDecisionEvents reads committed decisions for one project and
// provider. Decode after the SQL event/project filter: SQLite's JSON lookup
// of duplicate keys can disagree with Go's last-key decoding. Results keep
// the stored timestamp/event-ID order; malformed relevant rows are rejected
// rather than treated as absent, which would weaken revocation.
//
// The egress watcher asks every 100 ms per open egress, and the store has a
// single connection, so each project/provider keeps a cursor: a call decodes
// only the decision rows committed since the previous one. Every check below
// still applies to each row; a malformed relevant row is never passed, so it
// fails every later call too.
func (s *Store) CredentialDecisionEvents(ctx context.Context, projectID, provider string, started time.Time) ([]model.Event, error) {
	s.credentialDecisions.mu.Lock()
	defer s.credentialDecisions.mu.Unlock()
	if s.credentialDecisions.seen == nil {
		s.credentialDecisions.seen = map[string]*credentialDecisionCursor{}
	}
	key := projectID + "\x00" + provider
	cursor := s.credentialDecisions.seen[key]
	if cursor == nil {
		cursor = &credentialDecisionCursor{}
		s.credentialDecisions.seen[key] = cursor
	}
	rows, err := s.db.QueryContext(ctx, `SELECT rowid, event_id, timestamp, data_json
 FROM audit_events WHERE event_type = 'PERMISSION_DECIDED' AND project_id = ? AND rowid > ?
 ORDER BY rowid`, projectID, cursor.last)
	if err != nil {
		return nil, fmt.Errorf("credential decisions: %w", err)
	}
	defer rows.Close()
	last := cursor.last
	var found []storedDecision
	for rows.Next() {
		var event model.Event
		var rowID int64
		var at, data string
		if err := rows.Scan(&rowID, &event.ID, &at, &data); err != nil {
			return nil, fmt.Errorf("scan credential decision: %w", err)
		}
		last = rowID
		if event.Timestamp, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("parse credential decision %s: %w", event.ID, err)
		}
		if err := json.Unmarshal([]byte(data), &event.Data); err != nil || event.Data == nil {
			return nil, fmt.Errorf("decode credential decision %s: %v", event.ID, err)
		}
		kind, ok := event.Data["kind"].(string)
		if !ok {
			return nil, fmt.Errorf("invalid credential decision kind %s", event.ID)
		}
		if kind != "credential" {
			continue
		}
		object, ok := event.Data["object"].(string)
		if !ok {
			return nil, fmt.Errorf("invalid credential decision object %s", event.ID)
		}
		if object != provider {
			continue
		}
		if _, ok := event.Data["allow"].(bool); !ok {
			return nil, fmt.Errorf("invalid credential decision allow %s", event.ID)
		}
		event.Type = "PERMISSION_DECIDED"
		event.ProjectID = projectID
		found = append(found, storedDecision{at: at, event: event})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read credential decisions: %w", err)
	}
	cursor.last = last
	cursor.events = append(cursor.events, found...)
	var selected []storedDecision
	for _, decision := range cursor.events {
		if !decision.event.Timestamp.Before(started) {
			selected = append(selected, decision)
		}
	}
	// The stored timestamp text and event ID order, as ORDER BY gave before.
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].at != selected[j].at {
			return selected[i].at < selected[j].at
		}
		return selected[i].event.ID < selected[j].event.ID
	})
	result := make([]model.Event, len(selected))
	for i, decision := range selected {
		result[i] = decision.event
	}
	return result, nil
}

// credentialDecisionCursor is how far a project/provider's credential
// decisions were read, with the relevant ones found so far.
type credentialDecisionCursor struct {
	last   int64
	events []storedDecision
}

type storedDecision struct {
	at    string
	event model.Event
}

type credentialDecisionCache struct {
	mu   sync.Mutex
	seen map[string]*credentialDecisionCursor
}

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
