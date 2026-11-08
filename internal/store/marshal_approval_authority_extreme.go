//go:build !governed

package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// The plan and run approval share a write transaction with the current Goal
// and accepted architecture binding. A prior read cannot authorize a commit
// after either source of authority has moved.
func (s *Store) validateMarshalApprovalAuthorityTx(ctx context.Context, tx *sql.Tx, storageProjectID string, p plan.ExecutionPlan, run marshal.Run, expectedPlan int64) error {
	if expectedPlan == 0 || !run.Process05Bound || p.State != plan.StateApproved {
		return nil
	}
	if p.Goal.GoalID == "" || p.Goal.Revision < 1 || p.RequiredProjectContract == nil {
		return projectcontext.Changed("marshal_approval_authority_extreme.go:23")
	}
	goal, err := readGoalContract(ctx, tx, p.Goal.GoalID, p.Goal.Revision)
	if err != nil || !goal.Confirmation.Settled() || !p.Goal.Matches(goal) {
		return projectcontext.Changed("marshal_approval_authority_extreme.go:27")
	}
	var activeID string
	var activeRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, goal.SessionID).Scan(&activeID, &activeRevision); err != nil || activeID != goal.ID || activeRevision != goal.Revision {
		return projectcontext.Changed("marshal_approval_authority_extreme.go:32")
	}
	binding := projectcontext.ProjectBinding{CanonicalID: string(p.ProjectID), StorageID: storageProjectID}
	contract, _, digest, err := readProjectContract(ctx, tx, binding)
	if err != nil {
		return err
	}
	current := projectcontext.ContractRef{ID: contract.ID, Revision: contract.Revision, Digest: digest}
	if current != *p.RequiredProjectContract {
		return errors.Join(projectcontext.Changed("marshal_approval_authority_extreme.go:41"), errors.New("accepted project contract changed before plan approval"))
	}
	return nil
}
