package store

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

// BeginGoalDispatch provides a cross-process admission barrier using SQLite's
// writer lock. The returned release must be called promptly after launching
// (not after waiting for) the provider. No writes or receipt are needed here.
func (s *Store) BeginGoalDispatch(ctx context.Context, session, id string, revision int64) (func(), error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Obtain the writer lock before reading canonical state. A no-op UPDATE
	// does not change the active head, but serializes against edits/cancellation.
	if _, err = tx.ExecContext(ctx, `UPDATE goal_active SET active_revision=active_revision WHERE session_id=?`, session); err != nil {
		tx.Rollback()
		return nil, err
	}
	var activeID string
	var activeRev int64
	if err = tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, session).Scan(&activeID, &activeRev); err != nil {
		tx.Rollback()
		return nil, err
	}
	if activeID != id || activeRev != revision {
		tx.Rollback()
		return nil, model.ErrGoalConflict
	}
	g, err := readGoalContract(ctx, tx, id, revision)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	if !g.Confirmation.Settled() {
		tx.Rollback()
		return nil, fmt.Errorf("%w: goal is %s", model.ErrGoalConflict, g.Confirmation)
	}
	return func() { _ = tx.Rollback() }, nil
}
