//go:build !governed

package execution

import "context"

type currentRequiredTaskFenceStore interface {
	withCurrentRequiredTaskFence(context.Context, string, func(map[string]ExecutionRun) error) error
}

// WithCurrentRequiredTaskFence selects the owned task run and its actual
// parent under the existing admission/store fence. Callers cannot relax a
// version check or nominate a different parent. The effect must still compare
// the admitted task exactly and validate current semantic/authorization pins.
// Detached snapshots are DATA only and grant no model/work authority.
// No Engine/store reentry, network/model, or user wait is permitted in effect.
func (e *Engine) WithCurrentRequiredTaskFence(ctx context.Context, runID string, effect func(map[string]ExecutionRun) error) error {
	if e == nil || runID == "" || effect == nil {
		return ErrRunBlocked
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	store, ok := e.store.(currentRequiredTaskFenceStore)
	if !ok {
		return ErrRunBlocked
	}
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.laneCommitMu.Lock()
	defer e.laneCommitMu.Unlock()
	return store.withCurrentRequiredTaskFence(ctx, runID, effect)
}

func (s *MemoryRunStore) withCurrentRequiredTaskFence(ctx context.Context, runID string, effect func(map[string]ExecutionRun) error) error {
	if s == nil || runID == "" || effect == nil {
		return ErrRunBlocked
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	current, exists := s.runs[runID]
	if !exists || current.RunID != runID || current.Version < 1 {
		return ErrRunConflict
	}
	owned, err := cloneExecutionRun(current)
	if err != nil {
		return err
	}
	snapshots := map[string]ExecutionRun{runID: owned}
	if current.ParentRunID != "" {
		parent, exists := s.runs[current.ParentRunID]
		if !exists || parent.RunID != current.ParentRunID || parent.Version < 1 || parent.RunID == runID || parent.ParentRunID != "" {
			return ErrRunConflict
		}
		ownedParent, err := cloneExecutionRun(parent)
		if err != nil {
			return err
		}
		snapshots[parent.RunID] = ownedParent
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return effect(snapshots)
}
func (s *FileRunStore) withCurrentRequiredTaskFence(ctx context.Context, runID string, effect func(map[string]ExecutionRun) error) error {
	if s == nil || s.mem == nil {
		return ErrRunBlocked
	}
	return s.mem.withCurrentRequiredTaskFence(ctx, runID, effect)
}
