//go:build !governed

package execution

import "context"

type requiredContextFenceStore interface {
	WithRequiredContextFence(context.Context, map[string]int64, func(map[string]ExecutionRun) error) error
}

// WithRequiredContextFence holds this engine's admission/lane locks and the
// owned run store snapshot through one short local context admission effect.
// The callback receives detached snapshots. It MUST NOT reenter this engine
// or run store, invoke a model/network, or wait for a user. SQLite admission
// and opaque invocation-token creation are suitable local effects.
// This is not an atomic transaction with SQLite or other store instances.
func (e *Engine) WithRequiredContextFence(ctx context.Context, versions map[string]int64, effect func(map[string]ExecutionRun) error) error {
	if e == nil || effect == nil {
		return ErrRunBlocked
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fenced, ok := e.store.(requiredContextFenceStore)
	if !ok {
		return ErrRunBlocked
	}
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.laneCommitMu.Lock()
	defer e.laneCommitMu.Unlock()
	return fenced.WithRequiredContextFence(ctx, versions, effect)
}

func (s *MemoryRunStore) WithRequiredContextFence(ctx context.Context, versions map[string]int64, effect func(map[string]ExecutionRun) error) error {
	if s == nil || effect == nil || len(versions) == 0 || len(versions) > 256 {
		return ErrRunBlocked
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshots := make(map[string]ExecutionRun, len(versions))
	for id, version := range versions {
		row, ok := s.runs[id]
		if !ok || id == "" || version < 1 || row.Version != version {
			return ErrRunConflict
		}
		if row.ParentRunID != "" {
			if _, found := versions[row.ParentRunID]; !found {
				return ErrRunConflict
			}
		}
		owned, err := cloneExecutionRun(row)
		if err != nil {
			return err
		}
		snapshots[id] = owned
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return effect(snapshots)
}
func (s *FileRunStore) WithRequiredContextFence(ctx context.Context, versions map[string]int64, effect func(map[string]ExecutionRun) error) error {
	if s == nil || s.mem == nil {
		return ErrRunBlocked
	}
	return s.mem.WithRequiredContextFence(ctx, versions, effect)
}
