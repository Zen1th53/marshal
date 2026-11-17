package app

import "context"

// recordArchitectureCommit binds a commit (HEAD of dir when commit is empty)
// to the project's accepted architecture, for the given event: accepted,
// merged or delivered. It is a record, never a gate: a failure changes
// nothing about the run.
func (r *Runtime) recordArchitectureCommit(ctx context.Context, dir, commit, event, task string) {
	if r == nil || r.store == nil || dir == "" {
		return
	}
	r.recordArchitectureCommitEdition(ctx, dir, commit, event, task)
}

func (s *MarshalService) recordArchitectureCommit(ctx context.Context, dir, commit, event, task string) {
	if s != nil {
		s.runtime.recordArchitectureCommit(ctx, dir, commit, event, task)
	}
}
