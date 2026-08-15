package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policytest"
)

func (s *Store) appendPolicyTestEvent(ctx context.Context, tx *sql.Tx, fact policytest.EventFact) error {
	if err := fact.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(fact.Data())
	if err != nil {
		return fmt.Errorf("%w: encode policy test event", model.ErrInvalid)
	}
	sum := sha256.Sum256(append([]byte(string(fact.Type)+"\x00"), payload...))
	eventID := "POLICYTEST-EVENT-" + hex.EncodeToString(sum[:])
	var eventType, dataJSON string
	err = tx.QueryRowContext(ctx, "SELECT event_type, data_json FROM audit_events WHERE event_id = ?", eventID).Scan(&eventType, &dataJSON)
	if err == nil {
		if eventType == string(fact.Type) && dataJSON == string(payload) {
			return nil
		}
		return fmt.Errorf("%w: policy test event identity conflict", model.ErrConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read policy test event: %w", err)
	}
	return s.AppendEvent(ctx, tx, model.Event{ID: eventID, Type: string(fact.Type), Timestamp: time.Now().UTC(), AggregateRevision: 0, Data: fact.Data()})
}
