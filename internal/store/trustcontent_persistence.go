package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/trustcontent"
)

func (s *Store) PutTrustedContentSegment(ctx context.Context, record trustcontent.Record) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("%w: invalid trusted content segment", model.ErrInvalid)
	}
	for attempt := 0; ; attempt++ {
		err := s.putTrustedContentSegmentOnce(ctx, record)
		if err == nil || !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return fmt.Errorf("%w: retry trusted content segment persistence", model.ErrUnavailable)
		}
	}
}

func (s *Store) putTrustedContentSegmentOnce(ctx context.Context, record trustcontent.Record) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: begin trusted content persistence", model.ErrUnavailable)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO trusted_content_segments(
			segment_id, idempotency_key, source_id, zone, digest, content_ref, state, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.IdempotencyKey, record.SourceID, record.Zone, record.Digest, record.ContentRef, record.State, record.CreatedAt.Format(time.RFC3339Nano))
	if err == nil {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%w: commit trusted content persistence", model.ErrUnavailable)
		}
		return nil
	}
	if isSQLiteBusy(err) {
		return err
	}
	existing, getErr := getTrustedContentSegment(ctx, tx, record.ID)
	if getErr == nil && sameTrustedContentRecord(existing, record) {
		return nil
	}
	if errors.Is(getErr, sql.ErrNoRows) {
		return fmt.Errorf("%w: trusted content idempotency conflict", model.ErrConflict)
	}
	if getErr != nil {
		return fmt.Errorf("%w: read trusted content segment", model.ErrUnavailable)
	}
	return fmt.Errorf("%w: trusted content segment is immutable", model.ErrConflict)
}

func (s *Store) GetTrustedContentSegment(ctx context.Context, id string) (trustcontent.Record, error) {
	if id == "" {
		return trustcontent.Record{}, fmt.Errorf("%w: trusted content segment id is required", model.ErrInvalid)
	}
	record, err := getTrustedContentSegment(ctx, s.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return trustcontent.Record{}, model.ErrNotFound
	}
	if err != nil {
		return trustcontent.Record{}, fmt.Errorf("%w: read trusted content segment", model.ErrUnavailable)
	}
	if err := record.Validate(); err != nil {
		return trustcontent.Record{}, fmt.Errorf("%w: invalid trusted content segment", model.ErrInvalid)
	}
	return record, nil
}

func (s *Store) TransitionTrustedContentSegment(ctx context.Context, id string, from, to trustcontent.State) error {
	if id == "" || !from.Valid() || !to.Valid() || from == to ||
		!((from == trustcontent.StateIngested && to == trustcontent.StateZoned) || (from == trustcontent.StateZoned && to == trustcontent.StateRendered)) {
		return fmt.Errorf("%w: invalid trusted content state transition", model.ErrInvalid)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE trusted_content_segments SET state = ? WHERE segment_id = ? AND state = ?`, to, id, from)
	if err != nil {
		return fmt.Errorf("%w: transition trusted content segment", model.ErrUnavailable)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("%w: trusted content state transition conflict", model.ErrConflict)
	}
	return nil
}

type trustedContentScanner interface{ Scan(...any) error }

func getTrustedContentSegment(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (trustcontent.Record, error) {
	var record trustcontent.Record
	var createdAt string
	err := query.QueryRowContext(ctx, `
		SELECT segment_id, idempotency_key, source_id, zone, digest, content_ref, state, created_at
		FROM trusted_content_segments WHERE segment_id = ?
	`, id).Scan(&record.ID, &record.IdempotencyKey, &record.SourceID, &record.Zone, &record.Digest, &record.ContentRef, &record.State, &createdAt)
	if err != nil {
		return trustcontent.Record{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return trustcontent.Record{}, err
	}
	record.CreatedAt = parsed.UTC()
	return record, nil
}

func sameTrustedContentRecord(left, right trustcontent.Record) bool {
	return left.ID == right.ID && left.IdempotencyKey == right.IdempotencyKey && left.SourceID == right.SourceID &&
		left.Zone == right.Zone && left.Digest == right.Digest && left.ContentRef == right.ContentRef
}
