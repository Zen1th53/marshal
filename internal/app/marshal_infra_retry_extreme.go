//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/worktree"
)

// infraRetryLimit bounds, per task, the attempts that end without a hand-in
// for an infrastructure, provider, transport or tool failure. They do not
// spend the governed rework limit (bug #23), so they have this bound of
// their own: past it the task escalates with the last failure.
const infraRetryLimit = 6

// infraFailoverStreak is how many consecutive such failures one provider may
// have on a task before it cools down for infraFailureCooldown, so the next
// attempt fails over to another provider of the approved order.
const infraFailoverStreak = 2

const infraFailureCooldown = 10 * time.Minute

// infraRetryApplies reports whether a worker attempt that ended without a
// hand-in is retried outside the rework limit: governed tasks of a parallel
// Process05 plan, whose next attempt opens a fresh lane.
func infraRetryApplies(run marshal.Run, t marshal.Task) bool { return reworkFailedLaneWorker(run, t) }

// returnInfraFailure sends a task whose attempt ended without a hand-in back
// for a fresh attempt without counting it as rework. Only a review or
// verification that rejects delivered work spends the rework limit. The
// attempt keeps its reason as its review, the next lane is a new one, and a
// provider that failed the task twice in a row cools down so the next
// attempt fails over. Past infraRetryLimit the task escalates.
func (s *MarshalService) returnInfraFailure(ctx context.Context, runID string, run marshal.Run, rev int64, i int, cause string) error {
	t := &run.Tasks[i]
	// Capture the actual branch before clearing delivery identity. A Process05
	// import can advance it before a later hand-in step fails.
	var recoveryErr error
	dir := filepath.Join(s.Worktrees, taskArtifactID(runID, *t))
	if _, err := os.Lstat(dir); err == nil {
		state, err := worktree.New(s.Repository, s.Worktrees).Inspect(ctx, dir)
		if err != nil {
			recoveryErr = err
		} else if state.Branch != t.Branch {
			recoveryErr = errors.New("task branch changed; inspect retained worktree")
		} else if _, err := gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", t.BaseCommit, state.HEAD); err != nil {
			recoveryErr = errors.New("task HEAD is not based on its admitted base; inspect retained worktree")
		} else {
			t.FailedAttemptCommit = state.HEAD
			if state.Dirty {
				recoveryErr = errors.New("dirty failed-attempt worktree retained; preserve and resolve its changes before retry")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		recoveryErr = err
	}
	kind, reason := classifyInfraFailure(cause)
	attempt := 1 + t.EvidenceAttemptBase + taskReturns(*t)
	t.InfraRetries++
	if t.InfraStreakWorker == t.Worker {
		t.InfraStreak++
	} else {
		t.InfraStreakWorker, t.InfraStreak = t.Worker, 1
	}
	note := fmt.Sprintf("infrastructure failure (%s), not counted as rework: %s", kind, reason)
	review := marshal.Review{Reviewer: "marshal-runtime", Verdict: marshal.VerdictReturn, Reasons: []string{note}}
	if _, err := s.Store.GetMarshalReview(ctx, runID, t.PlanTaskID, attempt); errors.Is(err, model.ErrNotFound) {
		if _, err = s.Store.SetMarshalReview(ctx, runID, t.PlanTaskID, attempt, review); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	data := map[string]any{
		"reason":                note,
		"return_attempt":        fmt.Sprint(attempt),
		"return_reasons":        []string{note},
		"counted":               false,
		"infra_failure":         kind,
		"infra_retry":           fmt.Sprint(t.InfraRetries),
		"infra_retry_limit":     fmt.Sprint(infraRetryLimit),
		"worker":                t.Worker,
		"failed_attempt_commit": t.FailedAttemptCommit,
	}
	// The next attempt opens a fresh lane; the failed one stays as evidence.
	t.LaneReworkBase++
	t.ResultCommit = ""
	run.Operation = nil
	kindEvent := events.EventTypeMarshalTaskReturned
	if recoveryErr != nil {
		t.State = marshal.Escalated
		t.EscalationReason = "failed attempt recovery: " + recoveryErr.Error()
		pauseMarshal(&run, t.EscalationReason, "inspect and preserve the retained worktree; resolve its identity or dirty changes, then /marshal retry "+t.PlanTaskID+" and /marshal resume", "")
		data["reason"] = t.EscalationReason
		kindEvent = events.EventTypeMarshalEscalated
	} else if t.InfraRetries > infraRetryLimit {
		t.State = marshal.Escalated
		t.EscalationReason = fmt.Sprintf("%s: %d attempts ended without a hand-in; last on %s (%s): %s", infraRetryEscalation, t.InfraRetries, t.Worker, kind, reason)
		pauseMarshal(&run, t.EscalationReason, "resolve the provider or tool failure, then /marshal retry "+t.PlanTaskID+" and /marshal resume", "")
		data["reason"] = t.EscalationReason
		kindEvent = events.EventTypeMarshalEscalated
	} else {
		t.State = marshal.Returned
		if t.InfraStreak >= infraFailoverStreak && s.otherWorker(t.Worker, t.Mode) != "" {
			until := s.clock().Add(infraFailureCooldown)
			providerHealth.Mark(t.Worker, "infrastructure failure: "+kind, until)
			data["provider_cooldown_until"] = until.UTC().Format(time.RFC3339)
			t.InfraStreakWorker, t.InfraStreak = "", 0
		}
	}
	if err := s.record(ctx, runID, t.PlanTaskID, kindEvent, data); err != nil {
		return err
	}
	if s.RecordActivity != nil {
		s.RecordActivity(fmt.Sprintf("Task %s: %s (infra retry %d of %d)", t.PlanTaskID, note, t.InfraRetries, infraRetryLimit))
	}
	if err := s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	_, err := s.Charge(ctx, runID, t.PlanTaskID, "infra_retry", zeroMarshalCharge())
	return err
}

// resetInfraRetries gives a task a fresh infrastructure retry budget when the
// operator retries it.
func resetInfraRetries(t *marshal.Task) {
	t.InfraRetries, t.InfraStreakWorker, t.InfraStreak = 0, "", 0
}
