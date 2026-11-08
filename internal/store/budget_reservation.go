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

	"github.com/Zen1th53/marshal/internal/budget"
	"github.com/Zen1th53/marshal/internal/model"
)

// ReserveGoalCall charges before dispatch. A matching replay returns false;
// that intent may have launched and must be reconciled, never relaunched.
// Unknown usage/bounds fail closed. Reserved bounds remain charged even when
// the call fails or a process crashes; they cannot undercount later dispatch.
func (s *Store) ReserveGoalCall(ctx context.Context, session, id string, revision int64, r budget.CallRequest) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE goal_active SET active_revision=active_revision WHERE session_id=?`, session); err != nil {
		return false, err
	}
	var activeID string
	var rev int64
	if err = tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, session).Scan(&activeID, &rev); err != nil {
		return false, err
	}
	if activeID != id || rev != revision {
		return false, model.ErrGoalConflict
	}
	g, err := readGoalContract(ctx, tx, id, revision)
	if err != nil {
		return false, err
	}
	if !g.Confirmation.Settled() {
		return false, fmt.Errorf("%w: goal is %s", model.ErrGoalConflict, g.Confirmation)
	}
	var c model.ConsumedBudget
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT consumed_json FROM budget_trackers WHERE session_id=? AND goal_id=? AND revision=?`, session, id, revision).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return false, err
		}
	} else {
		// No admitted calls is a known zero, distinct from an unreported call.
		z := int64(0)
		cost := float64(0)
		c.ReservedTotalTokens = &z
		c.ReservedCostUSD = &cost
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256(payload)
	key := sha256.Sum256([]byte(r.Key))
	keyText := hex.EncodeToString(key[:])
	digestText := hex.EncodeToString(digest[:])
	if old, found := c.CallReservations[keyText]; found {
		if old != digestText {
			return false, model.ErrConflict
		}
		return false, nil
	}
	l := model.BudgetLimit{}
	if g.BudgetLimits != nil {
		l = *g.BudgetLimits
	}
	if r.RunID != "" && r.Duration > 0 {
		previous := c.RunDurations[r.RunID]
		delta := r.Duration - previous
		if delta < 0 {
			return false, model.ErrInvalid
		}
		if c.Duration > time.Duration(1<<63-1)-delta {
			return false, model.ErrInvalid
		}
		c.Duration += delta
	}
	if err = budget.CheckDispatch(l, c, r); err != nil {
		return false, err
	}
	c.ModelCalls++
	if r.Process == budget.ProcessVerification {
		c.VerificationCalls++
	}
	if r.Retry {
		c.Retries++
	}
	c.Handoffs += r.Handoffs
	if r.RunID != "" && r.Duration > 0 {
		if c.RunDurations == nil {
			c.RunDurations = map[string]time.Duration{}
		}
		c.RunDurations[r.RunID] = r.Duration
	} else if r.Duration > c.Duration {
		c.Duration = r.Duration
	}
	if r.MaxTokens == nil {
		c.ReservedTotalTokens = nil
		c.UnreportedTokens = true
	} else if c.ReservedTotalTokens != nil {
		if *c.ReservedTotalTokens > int64(^uint64(0)>>1)-*r.MaxTokens {
			return false, model.ErrInvalid
		}
		*c.ReservedTotalTokens += *r.MaxTokens
	}
	if r.MaxCostUSD == nil {
		c.ReservedCostUSD = nil
		c.UnreportedCost = true
	} else if c.ReservedCostUSD != nil {
		*c.ReservedCostUSD += *r.MaxCostUSD
	}
	if c.CallReservations == nil {
		c.CallReservations = map[string]string{}
	}
	c.CallReservations[keyText] = digestText
	out, err := json.Marshal(c)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO budget_trackers(session_id,goal_id,revision,consumed_json,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(session_id,goal_id,revision) DO UPDATE SET consumed_json=excluded.consumed_json,updated_at=excluded.updated_at`, session, id, revision, string(out), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// GoalBudgetConsumed returns logical zero for no admitted work, and preserves
// nil measurements once a call with unknown usage has been admitted.
func (s *Store) GoalBudgetConsumed(ctx context.Context, session, id string, revision int64) (model.ConsumedBudget, error) {
	c, err := s.GetBudgetTracker(ctx, session, id, revision)
	if errors.Is(err, model.ErrNotFound) {
		return model.ConsumedBudget{}, nil
	}
	if err != nil {
		return model.ConsumedBudget{}, err
	}
	return *c, nil
}

// SettleGoalCall preserves a late turn's measured usage on the active head,
// including a new pending or cancelled revision. It cannot change lifecycle.
func (s *Store) SettleGoalCall(ctx context.Context, session, id string, revision int64, key string, result budget.CallSettlement) error {
	if result.Duration < 0 || len(result.RunID) > 256 || key == "" || len(key) > 256 || result.TotalTokens != nil && *result.TotalTokens < 0 {
		return model.ErrInvalid
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return model.ErrInvalid
	}
	if result.CostUSD != nil && *result.CostUSD < 0 {
		return model.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE goal_active SET active_revision=active_revision WHERE session_id=?`, session); err != nil {
		return err
	}
	var activeID string
	var current int64
	if err = tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, session).Scan(&activeID, &current); err != nil {
		return err
	}
	if activeID != id || current < revision {
		return model.ErrGoalConflict
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT consumed_json FROM budget_trackers WHERE session_id=? AND goal_id=? AND revision=?`, session, id, current).Scan(&raw); err != nil {
		return err
	}
	var c model.ConsumedBudget
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(key))
	keyText := hex.EncodeToString(sum[:])
	digest := sha256.Sum256(payload)
	digestText := hex.EncodeToString(digest[:])
	if _, ok := c.CallReservations[keyText]; !ok {
		return model.ErrConflict
	}
	if old, ok := c.CallSettlements[keyText]; ok {
		if old != digestText {
			return model.ErrConflict
		}
		return nil
	}
	if result.RunID != "" && result.Duration > 0 {
		previous := c.RunDurations[result.RunID]
		if result.Duration < previous {
			return model.ErrInvalid
		}
		delta := result.Duration - previous
		if c.Duration > time.Duration(1<<63-1)-delta {
			return model.ErrInvalid
		}
		c.Duration += delta
		if c.RunDurations == nil {
			c.RunDurations = map[string]time.Duration{}
		}
		c.RunDurations[result.RunID] = result.Duration
	}
	if result.TotalTokens == nil {
		c.UnreportedTokens = true
	} else {
		if c.TotalTokens == nil {
			z := int64(0)
			c.TotalTokens = &z
		}
		if *c.TotalTokens > int64(1<<63-1)-*result.TotalTokens {
			return model.ErrInvalid
		}
		*c.TotalTokens += *result.TotalTokens
		c.HasReportedTokens = true
	}
	if result.CostUSD == nil {
		c.UnreportedCost = true
	} else {
		if c.CostUSD == nil {
			z := float64(0)
			c.CostUSD = &z
		}
		*c.CostUSD += *result.CostUSD
		c.HasReportedCost = true
	}
	if c.CallSettlements == nil {
		c.CallSettlements = map[string]string{}
	}
	c.CallSettlements[keyText] = digestText
	encoded, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE budget_trackers SET consumed_json=?,updated_at=? WHERE session_id=? AND goal_id=? AND revision=?`, string(encoded), time.Now().UTC().Format(time.RFC3339Nano), session, id, current); err != nil {
		return err
	}
	// Cancellation remains final while retaining late consumption evidence.
	if _, err = tx.ExecContext(ctx, `UPDATE goal_terminations SET budget_consumed_json=? WHERE session_id=? AND goal_id=? AND revision=? AND state='CANCELLED'`, string(encoded), session, id, current); err != nil {
		return err
	}
	return tx.Commit()
}

// ReleaseGoalCall refunds one admitted call that provably never reached a
// model (an exact zero-call transport review pause). Only an unsettled
// reservation can be released, and only once.
func (s *Store) ReleaseGoalCall(ctx context.Context, session, id string, revision int64, key string, retry bool) error {
	if key == "" || len(key) > 256 {
		return model.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE goal_active SET active_revision=active_revision WHERE session_id=?`, session); err != nil {
		return err
	}
	var activeID string
	var current int64
	if err = tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, session).Scan(&activeID, &current); err != nil {
		return err
	}
	if activeID != id || current != revision {
		return model.ErrGoalConflict
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT consumed_json FROM budget_trackers WHERE session_id=? AND goal_id=? AND revision=?`, session, id, revision).Scan(&raw); err != nil {
		return err
	}
	var c model.ConsumedBudget
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(key))
	keyText := hex.EncodeToString(sum[:])
	if _, ok := c.CallReservations[keyText]; !ok {
		return model.ErrConflict
	}
	if _, settled := c.CallSettlements[keyText]; settled || c.ModelCalls < 1 || retry && c.Retries < 1 {
		return model.ErrConflict
	}
	delete(c.CallReservations, keyText)
	c.ModelCalls--
	if retry {
		c.Retries--
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE budget_trackers SET consumed_json=?,updated_at=? WHERE session_id=? AND goal_id=? AND revision=?`, string(encoded), time.Now().UTC().Format(time.RFC3339Nano), session, id, revision); err != nil {
		return err
	}
	return tx.Commit()
}
