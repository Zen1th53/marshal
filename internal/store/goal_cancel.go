package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// CancelGoalContract commits the head, cancelled revision, consumed budget,
// termination, command receipt and audit using the ordinary goal serializer.
// The active CANCELLED head is also the durable dispatch-prevention intent.
func (s *Store) CancelGoalContract(ctx context.Context, session, id string, expected int64, reason string) (model.GoalContract, error) {
	if expected < 1 || strings.TrimSpace(reason) == "" || len(reason) > 4096 {
		return model.GoalContract{}, model.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.GoalContract{}, err
	}
	defer tx.Rollback()
	g, err := readGoalContract(ctx, tx, id, expected)
	if err != nil {
		return model.GoalContract{}, err
	}
	if g.SessionID != session || g.Confirmation == model.ConfirmationCancelled {
		return model.GoalContract{}, model.ErrGoalConflict
	}
	g.Revision = expected + 1
	g.Confirmation = model.ConfirmationCancelled
	g.RevisionReason = reason
	g.UpdatedAt = time.Now().UTC()
	if err := s.saveGoalContractTx(ctx, tx, g, expected); err != nil {
		return model.GoalContract{}, err
	}
	c := model.ConsumedBudget{}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT consumed_json FROM budget_trackers WHERE session_id=? AND goal_id=? AND revision=?`, session, id, g.Revision).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.GoalContract{}, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return model.GoalContract{}, err
		}
	}
	term := model.GoalTermination{SessionID: session, GoalID: id, GoalRevision: g.Revision, State: model.StateCancelled, ReasonCode: model.ReasonUserCancelled, ReasonDetail: reason, ConsumedBudget: c, CompletedAt: g.UpdatedAt}
	payload, err := json.Marshal(c)
	if err != nil {
		return model.GoalContract{}, err
	}
	if err := s.writeGoalTermination(ctx, tx, term, string(payload)); err != nil {
		return model.GoalContract{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.GoalContract{}, fmt.Errorf("commit goal cancellation: %w", err)
	}
	return s.GetGoalContract(ctx, id, g.Revision)
}
