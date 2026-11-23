package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
)

// SettingChange is a committed change, identified independently of its delivery.
type SettingChange = settingscatalog.Change

// SetMarshalSettingsAs commits settings and their audit facts in the same CAS transaction.
// Actor must come from the authenticated caller, never from a proposed setting value.
func (s *Store) SetMarshalSettingsAs(ctx context.Context, projectID string, settings marshal.Settings, expected int64, actor string) (int64, error) {
	revision, _, err := s.CommitMarshalSettings(ctx, projectID, settings, expected, actor)
	return revision, err
}

// CommitMarshalSettings returns receipts only after their transaction commits.
func (s *Store) CommitMarshalSettings(ctx context.Context, projectID string, settings marshal.Settings, expected int64, actor string) (int64, []SettingChange, error) {
	if projectID == "" || actor == "" {
		return 0, nil, fmt.Errorf("%w: missing settings identity", model.ErrInvalid)
	}
	if err := settings.Validate(); err != nil {
		return 0, nil, fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	old := marshal.DefaultSettings()
	var raw string
	err = tx.QueryRowContext(ctx, "SELECT data_json FROM marshal_settings WHERE project_id=?", projectID).Scan(&raw)
	if err == nil {
		old = marshal.Settings{}
		if err = json.Unmarshal([]byte(raw), &old); err != nil {
			return 0, nil, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, err
	}
	next, err := marshalWriteTx(ctx, tx, "marshal_settings", []string{"project_id"}, []any{projectID}, settings, expected)
	if err != nil {
		return 0, nil, err
	}
	changes := []SettingChange{}
	for _, d := range settingscatalog.Catalogue() {
		before, _ := settingscatalog.Get(old, d.Key)
		after, _ := settingscatalog.Get(settings, d.Key)
		if before == after {
			continue
		}
		id := fmt.Sprintf("settings:%x:%d:%s", sha256.Sum256([]byte(projectID)), next, d.Key)
		change := SettingChange{ID: id, Revision: next, Key: d.Key, Old: before, New: after, Actor: actor, At: time.Now().UTC()}
		changes = append(changes, change)
		data := map[string]any{"id": change.ID, "revision": next, "key": d.Key, "old": before, "new": after, "actor": actor, "at": change.At}
		err = s.appendEventTx(ctx, tx, model.Event{ID: id, Type: "marshal.settings.changed", ProjectID: projectID, AggregateRevision: next, Timestamp: change.At, Data: data})
		if err != nil {
			return 0, nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, nil, err
	}
	return next, changes, nil
}

// MarshalSettingsChanges returns a bounded, newest-first history.
func (s *Store) MarshalSettingsChanges(ctx context.Context, projectID string) ([]SettingChange, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data_json FROM audit_events WHERE project_id=? AND event_type='marshal.settings.changed' ORDER BY timestamp DESC, event_id LIMIT 100`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SettingChange{}
	for rows.Next() {
		var raw string
		var c SettingChange
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
