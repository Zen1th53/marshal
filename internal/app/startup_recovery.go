package app

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/store"
)

// RecoveryReport describes what a previous run left behind.
//
// It exists because startup reconciliation was already happening but its
// result was discarded, which meant a session that had been interrupted, and
// tasks that had been reclaimed from a dead worker, were tidied up silently. A
// user returning to a project could not tell that anything had happened. The
// counts were being computed all along; they simply were not reported.
type RecoveryReport struct {
	// ReclaimedTasks were held by a worker that is no longer running and have
	// been returned to the pool.
	ReclaimedTasks int `json:"reclaimed_tasks"`
	// EndedSessions were left open by a previous run and have been closed.
	EndedSessions int `json:"ended_sessions"`
	// FailedRuns were in flight when the previous run stopped.
	FailedRuns int `json:"failed_runs"`
}

// Interrupted reports whether the previous run left anything behind.
func (r RecoveryReport) Interrupted() bool {
	return r.ReclaimedTasks > 0 || r.EndedSessions > 0 || r.FailedRuns > 0
}

// Summary describes the recovery in the user's terms, or reports that there
// was nothing to recover.
func (r RecoveryReport) Summary() string {
	if !r.Interrupted() {
		return "No interrupted work was found."
	}
	return fmt.Sprintf(
		"A previous run ended unexpectedly: %d task(s) were returned to the queue, %d session(s) were closed, and %d run(s) were marked failed.",
		r.ReclaimedTasks, r.EndedSessions, r.FailedRuns)
}

// ReconcileStartupWithReport performs startup reconciliation and reports what
// it found.
//
// Reconciliation itself is unchanged and remains idempotent: reclaiming an
// already-reclaimed task is a no-op, so a repeated or crashed startup does not
// double-count or double-act. What changes is that the outcome is returned
// instead of discarded, so the control center can tell the user that their
// previous session was interrupted rather than quietly cleaning up after it.
//
// Nothing here resumes work. Recovery is offered; taking it is the user's
// decision.
func (r *Runtime) ReconcileStartupWithReport(ctx context.Context) (RecoveryReport, error) {
	if r == nil || r.store == nil {
		return RecoveryReport{}, fmt.Errorf("runtime is unavailable")
	}
	result, err := r.store.ReconcileStartupOrphans(ctx)
	if err != nil {
		return RecoveryReport{}, err
	}
	report := reportFrom(result)

	// Stale leases are released as a second pass, exactly as before.
	reclaimed, err := r.releaseStaleLeases(ctx)
	if err != nil {
		return report, err
	}
	report.ReclaimedTasks += reclaimed
	return report, nil
}

func reportFrom(result store.ReconcileResult) RecoveryReport {
	return RecoveryReport{
		ReclaimedTasks: result.ReconciledTasks,
		EndedSessions:  result.TerminatedSessions,
		FailedRuns:     result.FailedWorkerRuns,
	}
}
