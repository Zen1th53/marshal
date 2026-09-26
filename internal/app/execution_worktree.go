package app

import "context"

// SetPreserveBranch selects branch delivery for a ready Process 05 run.
func (s *ExecutionService) SetPreserveBranch(ctx context.Context, runID, baseCommit string) error {
	return s.engine.SetPreserveBranch(ctx, runID, baseCommit)
}
