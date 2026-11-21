//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if isProviderAuthFailure(cause) {
		return s.pauseWorkerAuth(ctx, runID, run.Tasks[i].PlanTaskID, cause)
	}
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
	// The failover streak counts only failures that can be the provider's.
	if infraKindCoolsProvider(kind) {
		if t.InfraStreakWorker == t.Worker {
			t.InfraStreak++
		} else {
			t.InfraStreakWorker, t.InfraStreak = t.Worker, 1
		}
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
		"summary":               fmt.Sprintf("%s: %s. Retrying (%d of %d); this doesn't count against the task.", firstNonEmpty(strings.TrimSpace(t.Title), t.PlanTaskID), infraFailurePhrase(ProviderDisplayName(t.Worker), kind), t.InfraRetries, infraRetryLimit) + " " + reason,
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
		machine := "failed attempt recovery: " + recoveryErr.Error()
		t.EscalationReason = machine
		t.EscalationCode = escalationCode(marshal.CodeFailedAttemptRecovery, t.EscalationReason)
		plain := "A failed attempt left work behind, and Marshal kept it."
		pauseMarshal(&run, marshal.Pause{Reason: plain, Resolutions: []string{"Press Enter to retry the task; the kept work stays in its own branch."}, Detail: machine, Code: marshal.CodeFailedAttemptRecovery, Action: marshal.PauseRetryTask, TaskID: t.PlanTaskID})
		data["reason"], data["summary"] = machine, plain
		kindEvent = events.EventTypeMarshalEscalated
	} else if kind == platformUnsupported {
		// Every provider fails the same way here, so neither a retry nor a
		// provider cooldown can help: stop at once and say why.
		t.State = marshal.Escalated
		machine := fmt.Sprintf("%s: %s", infraRetryEscalation, reason)
		t.EscalationReason = machine
		t.EscalationCode = escalationCode(marshal.CodeInfraRetry, t.EscalationReason)
		plain, resolution := platformRefusal(platformName(runtime.GOOS, runtime.GOARCH), string(t.Mode), reason)
		pauseMarshal(&run, marshal.Pause{Reason: plain, Resolutions: []string{resolution}, Detail: machine, Code: marshal.CodeInfraRetry, Action: marshal.PauseRetryTask, TaskID: t.PlanTaskID})
		data["reason"], data["summary"] = machine, plain
		kindEvent = events.EventTypeMarshalEscalated
	} else if t.InfraRetries > infraRetryLimit {
		t.State = marshal.Escalated
		machine := fmt.Sprintf("%s: %d attempts ended without a hand-in; last on %s (%s): %s", infraRetryEscalation, t.InfraRetries, t.Worker, kind, reason)
		t.EscalationReason = machine
		t.EscalationCode = escalationCode(marshal.CodeInfraRetry, t.EscalationReason)
		provider := ProviderDisplayName(t.Worker)
		plain := fmt.Sprintf("%s failed %d times in a row (%s).", provider, t.InfraRetries, plainInfraKind(kind)) + " " + reason
		pauseMarshal(&run, marshal.Pause{Reason: plain, Resolutions: []string{fmt.Sprintf("Check %s on the Providers view (press 4), then press Enter to retry.", provider)}, Detail: machine, Code: marshal.CodeInfraRetry, Action: marshal.PauseRetryTask, TaskID: t.PlanTaskID})
		data["reason"], data["summary"] = machine, plain
		kindEvent = events.EventTypeMarshalEscalated
	} else {
		t.State = marshal.Returned
		// Only a failure that says something about the provider cools it;
		// a crash, an expired lease or a lost connection to Marshal's tools
		// would otherwise park the run as waiting on a quota it still has.
		if t.InfraStreak >= infraFailoverStreak && infraKindCoolsProvider(kind) && s.otherWorker(t.Worker, t.Mode) != "" {
			until := s.clock().Add(infraFailureCooldown)
			providerHealth.Mark(t.Worker, "infrastructure failure ("+kind+"): "+reason, until)
			data["provider_cooldown_until"] = until.UTC().Format(time.RFC3339)
			t.InfraStreakWorker, t.InfraStreak = "", 0
		}
	}
	if err := s.record(ctx, runID, t.PlanTaskID, kindEvent, data); err != nil {
		return err
	}
	if s.RecordActivity != nil {
		s.RecordActivity(data["summary"].(string))
	}
	if err := s.save(ctx, runID, run, rev); err != nil {
		return err
	}
	_, err := s.Charge(ctx, runID, t.PlanTaskID, "infra_retry", zeroMarshalCharge())
	return err
}

// platformRefusal words a pause for a worker this computer cannot run. It
// claims a Linux (or Linux amd64) requirement only when the refusal itself
// names one, such as governed socket confinement; any other refusal is
// reported as what it is, a limit of Marshal on this computer.
func platformRefusal(host, mode, reason string) (plain, resolution string) {
	lower := strings.ToLower(reason)
	needs := ""
	switch {
	case strings.Contains(lower, "linux amd64"):
		needs = "Linux amd64"
	case strings.Contains(lower, "linux"):
		needs = "Linux"
	}
	if needs != "" {
		return fmt.Sprintf("This computer (%s) cannot run %s workers (%s). They need %s; run Marshal there to execute this plan.", host, mode, reason, needs),
			fmt.Sprintf("Run this project with Marshal on a %s machine, then press Enter to retry.", needs)
	}
	return fmt.Sprintf("This computer (%s) cannot run %s workers: Marshal could not start them here (%s). The plan and the providers are not at fault.", host, mode, reason),
		"Update Marshal, then press Enter to retry; meanwhile Marshal on Linux can run this plan."
}

// resetInfraRetries gives a task a fresh infrastructure retry budget when the
// operator retries it.
func resetInfraRetries(t *marshal.Task) {
	t.InfraRetries, t.InfraStreakWorker, t.InfraStreak = 0, "", 0
}

// The failed auth lane remains evidence; retry after sign-in opens a new one.
func retireWorkerAuthLane(run marshal.Run, t *marshal.Task) {
	if reworkFailedLaneWorker(run, *t) {
		t.LaneReworkBase++
	}
}
