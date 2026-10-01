package store

import (
	"context"
	"database/sql"
	"fmt"
)

// GateDecisionSummary counts recorded gate decisions; it is observation, not
// configuration: each row is a decision the runtime actually made.
type GateDecisionSummary struct {
	Allowed, Denied int
	LastAt          string
	LastPoint       string
	LastDigest      string
}

func (s *Store) SummarizeGateDecisions(ctx context.Context) (GateDecisionSummary, error) {
	var summary GateDecisionSummary
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(allowed),0), COALESCE(SUM(1-allowed),0) FROM gate_decisions`).Scan(&summary.Allowed, &summary.Denied); err != nil {
		return GateDecisionSummary{}, fmt.Errorf("summarize gate decisions: %w", err)
	}
	err := s.db.QueryRowContext(ctx, `SELECT created_at, gate_point, policy_digest FROM gate_decisions ORDER BY created_at DESC LIMIT 1`).Scan(&summary.LastAt, &summary.LastPoint, &summary.LastDigest)
	if err != nil && err != sql.ErrNoRows {
		return GateDecisionSummary{}, fmt.Errorf("read latest gate decision: %w", err)
	}
	return summary, nil
}
