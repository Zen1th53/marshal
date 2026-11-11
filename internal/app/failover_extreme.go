//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
)

var defaultFailoverOrder = providerFallbackOrder

func markProviderUnavailable(provider string, until time.Time) {
	providerHealth.Mark(provider, "provider limit", until)
}

func isProviderUnavailable(provider string, now time.Time) bool {
	_, cooling := providerHealth.Cooling(provider, now)
	return cooling
}

func getProviderCooldownUntil(provider string) (time.Time, bool) {
	until, _, ok := providerHealth.CooldownUntil(provider)
	return until, ok
}

// ResetProviderCooldowns clears all provider cooldowns (for test isolation).
func ResetProviderCooldowns() { providerHealth.Reset() }

// GetEffectiveFailoverOrder returns the active failover order for settings/run.
func GetEffectiveFailoverOrder(s marshal.Settings) []string {
	if order := s.EffectiveFailoverOrder(); len(order) > 0 {
		return order
	}
	return append([]string(nil), defaultFailoverOrder...)
}

// maxQuotaWait is the longest provider cooldown failover waits out in place.
const maxQuotaWait = 5 * time.Minute

// errWaitingOnQuota marks a task paused until a provider quota resets.
var errWaitingOnQuota = errors.New("waiting_on_quota")

// quotaWaitError is errWaitingOnQuota with the mode and the reset time the
// pause records. Its message is unchanged; errors.Is(err, errWaitingOnQuota)
// still holds, and handleWorkerLimit reads until with errors.As.
type quotaWaitError struct {
	mode  marshal.WorkerMode
	until time.Time
}

func (e *quotaWaitError) Error() string {
	return fmt.Sprintf("%v: every provider for %s mode is cooling down until %s", errWaitingOnQuota, e.mode, e.until.UTC().Format(time.RFC3339))
}

func (e *quotaWaitError) Is(target error) bool { return target == errWaitingOnQuota }

// markKnownEmpty treats a provider the limits reader reports as known-empty
// (0% left) as cooling: it marks it in providerHealth with its reset time
// (the limits reader's when known, else defaultCooldown), so the existing
// cooling, waiting and pausing logic applies to it unchanged.
func (s *MarshalService) markKnownEmpty(provider string, now time.Time) {
	if s.QuotaLeft == nil {
		return
	}
	left, known := s.QuotaLeft(provider)
	if !known || left > 0 {
		return
	}
	if _, cooling := providerHealth.Cooling(provider, now); cooling {
		return
	}
	markProviderUnavailable(provider, now.Add(defaultCooldown))
}

// selectFailoverWorker picks the next available provider from the failover order.
// If all matching providers are in cooldown it waits out a short cooldown and
// reports errWaitingOnQuota for a long one.
func (s *MarshalService) selectFailoverWorker(ctx context.Context, currentWorker string, mode marshal.WorkerMode, drivers map[string]driver.Driver, order []string) (string, error) {
	if len(order) == 0 {
		return "", fmt.Errorf("empty failover order")
	}
	currIdx := -1
	for i, p := range order {
		if marshalHarnessName(p) == marshalHarnessName(currentWorker) {
			currIdx = i
			break
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		now := s.clock()
		var earliest time.Time
		// Collect every eligible candidate in failover order, then return the
		// one with the most limit left: the highest known percent first,
		// unknown candidates after, ties by failover order.
		var cands []failoverCandidate
		for offset := 1; offset <= len(order); offset++ {
			cand := order[(currIdx+offset)%len(order)]
			d := drivers[cand]
			if d == nil {
				for name, candidate := range drivers {
					if marshalHarnessName(name) == marshalHarnessName(cand) {
						cand = name
						d = candidate
						break
					}
				}
			}
			if d == nil || d.Mode() != mode {
				continue
			}
			if s.InstalledVersion != nil && s.InstalledVersion(ctx, cand) == "" {
				continue
			}
			// A provider the limits reader reports known-empty is cooling.
			s.markKnownEmpty(cand, now)
			until, limited := getProviderCooldownUntil(cand)
			if limited && now.Before(until) {
				if earliest.IsZero() || until.Before(earliest) {
					earliest = until
				}
				continue
			}
			if mode == marshal.Governed && s.InstalledVersion != nil && !s.workerGovernance(ctx, cand).Governed() {
				if s.ProbeWorker == nil || s.ProbeWorker(ctx, cand) != nil || !s.workerGovernance(ctx, cand).Governed() {
					continue
				}
			}
			c := failoverCandidate{name: cand}
			if s.QuotaLeft != nil {
				c.left, c.known = s.QuotaLeft(cand)
			}
			cands = append(cands, c)
		}
		if len(cands) > 0 {
			best := 0
			for i := 1; i < len(cands); i++ {
				if cands[i].betterThan(cands[best]) {
					best = i
				}
			}
			return cands[best].name, nil
		}
		if earliest.IsZero() {
			return "", fmt.Errorf("no provider supports %s mode for failover", mode)
		}
		// A short cooldown is waited out; a long one (a daily or multi-day
		// quota) pauses the task as waiting_on_quota with its reset time
		// instead of holding the run silently for hours.
		if wait := earliest.Sub(now); wait > maxQuotaWait {
			return "", &quotaWaitError{mode: mode, until: earliest}
		}
		timer := time.NewTimer(earliest.Sub(now))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		}
	}

}

// failoverCandidate is one eligible failover provider and how much of its
// limit is left, as the limits reader reports it.
type failoverCandidate struct {
	name  string
	left  int
	known bool
}

// betterThan ranks candidates for "most limit left": a known percent beats
// an unknown one, a higher known percent beats a lower, and equal or both
// unknown keep failover order (the caller holds them in it and takes the
// first, so a later equal is never better).
func (c failoverCandidate) betterThan(other failoverCandidate) bool {
	if c.known != other.known {
		return c.known
	}
	if c.known {
		return c.left > other.left
	}
	return false
}

// handleWorkerLimit performs automatic provider failover under the Marshal build tag.
func (s *MarshalService) handleWorkerLimit(ctx context.Context, runID string, dispatch MarshalDispatch, handin marshal.HandIn, reason string, cooldown time.Duration) (out marshal.HandIn, retErr error) {
	defer func() {
		if retErr == nil {
			return
		}
		r, v, e := s.load(context.WithoutCancel(ctx), runID)
		if e != nil {
			return
		}
		idx := taskIndex(r, dispatch.TaskID)
		if idx < 0 || r.Tasks[idx].State != marshal.Dispatched {
			return
		}
		r.Tasks[idx].State = marshal.Escalated
		r.Tasks[idx].EscalationReason = "provider failover interrupted: " + retErr.Error()
		r.Tasks[idx].EscalationCode = escalationCode(marshal.CodeFailoverInterrupted, r.Tasks[idx].EscalationReason)
		r.Operation = nil
		pauseMarshal(&r, marshal.Pause{Reason: r.Tasks[idx].EscalationReason, Resolutions: []string{"inspect retained limited attempt and retry"}, Code: marshal.CodeFailoverInterrupted, Action: marshal.PauseResume})
		retErr = errors.Join(retErr, s.saveFailoverDecision(context.WithoutCancel(ctx), runID, r, v, dispatch.TaskID, events.EventTypeMarshalEscalated, map[string]any{"reason": r.Tasks[idx].EscalationReason}))
	}()
	run, rev, loadErr := s.load(context.WithoutCancel(ctx), runID)
	if loadErr != nil {
		return handin, loadErr
	}
	i := taskIndex(run, dispatch.TaskID)
	if i < 0 {
		return handin, model.ErrNotFound
	}
	t := &run.Tasks[i]
	currentWorker := t.Worker

	now := s.clock()
	until := now.Add(cooldown)
	// A limited model fails over to another verified model of the same
	// provider first; the provider cools down only when none is left.
	limitedModel := handin.Model
	if dispatch.Handle != nil && dispatch.Handle.Request().Model != "" {
		limitedModel = dispatch.Handle.Request().Model
	}
	sameProvider := false
	if limitedModel != "" {
		markModelLimited(currentWorker, limitedModel, reason, until)
		if other, usable := flexibleWorkerModel(ctx, s.Store, currentWorker, "", now); usable && other != limitedModel {
			sameProvider = true
		}
	}
	if !sameProvider {
		// Mark limited provider unavailable until cooldown
		markProviderUnavailable(currentWorker, until)
	}

	drivers := s.Drivers
	if t.Mode == marshal.Governed && s.GovernedDrivers != nil {
		drivers = s.GovernedDrivers
	}
	order := GetEffectiveFailoverOrder(run.Settings)

	nextWorker, err := currentWorker, error(nil)
	if !sameProvider {
		nextWorker, err = s.selectFailoverWorker(ctx, currentWorker, t.Mode, drivers, order)
	}
	if err != nil {
		t.State = marshal.Escalated
		t.EscalationReason = "provider failover: " + err.Error()
		baseCode := marshal.CodeProviderFailover
		if errors.Is(err, errWaitingOnQuota) {
			baseCode = marshal.CodeProviderQuota
		}
		t.EscalationCode = escalationCode(baseCode, t.EscalationReason)
		run.Operation = nil
		resolution := "retry after resolving provider availability"
		pause := marshal.Pause{Reason: t.EscalationReason, Resolutions: []string{resolution}, Code: baseCode, Action: marshal.PauseResume}
		if errors.Is(err, errWaitingOnQuota) {
			pause.Resolutions = []string{"resume after the provider quota resets"}
			pause.Kind = "quota"
			var qerr *quotaWaitError
			if errors.As(err, &qerr) {
				pause.Until = qerr.until
			}
		}
		pauseMarshal(&run, pause)
		return handin, errors.Join(err, s.saveFailoverDecision(context.WithoutCancel(ctx), runID, run, rev, dispatch.TaskID, events.EventTypeMarshalEscalated, map[string]any{"reason": t.EscalationReason}))
	}

	// Archive the limited attempt, including dirty files, while retaining the
	// canonical task branch and worktree identity for Process 05.
	head, err := gitMarshal(context.WithoutCancel(ctx), dispatch.Handle.Worktree(), "rev-parse", "HEAD")
	if err != nil {
		return handin, err
	}
	ref := fmt.Sprintf("refs/"+statepath.RefName+"/%s/limited/%s/attempt-%d", runID, dispatch.TaskID, t.ProviderFailovers+1)
	if _, err = gitMarshal(context.WithoutCancel(ctx), s.Repository, "update-ref", ref, strings.TrimSpace(head)); err != nil {
		return handin, err
	}

	archivePath := fmt.Sprintf("%s-limited-%d", dispatch.Handle.Worktree(), t.ProviderFailovers+1)
	archiveMoved, archiveRenamed, committed := false, false, false
	archiveBranch := fmt.Sprintf(statepath.RefName+"/%s/limited-%s-%d", runID, t.PlanTaskID, t.ProviderFailovers+1)
	defer func() {
		if committed {
			return
		}
		if archiveRenamed {
			_, rollbackErr := gitMarshal(context.WithoutCancel(ctx), archivePath, "branch", "-m", t.Branch)
			retErr = errors.Join(retErr, rollbackErr)
		}
		if archiveMoved {
			_, rollbackErr := gitMarshal(context.WithoutCancel(ctx), s.Repository, "worktree", "move", archivePath, dispatch.Handle.Worktree())
			retErr = errors.Join(retErr, rollbackErr)
		}
	}()

	if _, err = gitMarshal(context.WithoutCancel(ctx), s.Repository, "worktree", "move", dispatch.Handle.Worktree(), archivePath); err != nil {
		return handin, err
	}
	archiveMoved = true
	if _, err = gitMarshal(context.WithoutCancel(ctx), archivePath, "branch", "-m", archiveBranch); err != nil {
		return handin, err
	}

	archiveRenamed = true
	if s.AfterLifecycleEffect != nil {
		if err = s.AfterLifecycleEffect("provider_failover", ref); err != nil {
			return handin, err
		}
	}

	// Reassign task with fresh session, same brief and scope
	t.Worker = nextWorker
	t.State = marshal.Reassigned
	t.ResultCommit = ""
	t.EscalationReason = ""
	t.EscalationCode = ""
	if t.ReturnsByAgent == nil {
		t.ReturnsByAgent = map[string]int{}
	}
	// A provider failover is not a worker rework: the next worker starts with
	// a fresh return count and earlier workers' returns are retired (kept for
	// lane numbering), so its first return goes back to it instead of
	// escalating as if a second worker had failed the task too. An operator
	// reassignment keeps its own rule.
	retireFailoverReturns(t, nextWorker)
	// A failed lane is retained; the replacement opens the next attempt.
	if run.Process05Parallel && run.Process05Bound && t.Mode == marshal.Governed {
		t.LaneReworkBase++
	}
	t.ProviderFailovers++
	t.ProviderFailoverPending = true
	t.FailoverBrief = dispatch.Handle.Request().Brief
	run.Operation = nil
	if run.CloseAuthorization != nil {
		run.CloseAuthorization.Voided = true
	}

	// Audit every failover
	auditData := map[string]any{
		"failover":          true,
		"previous_worker":   currentWorker,
		"worker":            nextWorker,
		"reason":            reason,
		"cooldown":          cooldown.String(),
		"retained_worktree": archivePath,
		"retained_ref":      ref,
	}
	if err = s.saveFailoverDecision(context.WithoutCancel(ctx), runID, run, rev, dispatch.TaskID, events.EventTypeMarshalTaskReassigned, auditData); err != nil {
		return handin, err
	}

	committed = true

	// Show failover in activity, in one plain line.
	if s.RecordActivity != nil {
		s.RecordActivity(rerouteActivity(currentWorker, 1, nextWorker))
	}

	elapsed := s.clock().Sub(dispatch.Started)
	if elapsed < 0 {
		elapsed = 0
	}
	_, chargeErr := s.Charge(context.WithoutCancel(ctx), runID, dispatch.TaskID, "failover_reassign", marshal.Charge{Tokens: marshal.Amount{Known: false}, Money: marshal.Amount{Known: false}, WallTime: elapsed})

	return marshal.HandIn{}, chargeErr
}

// rerouteActivity is the one plain line a reroute shows: canonical provider
// names, no task ids and no cooldown. The TUI collapses consecutive
// identical-pair lines within a minute into one, so its wording is fixed.
func rerouteActivity(from string, n int, to string) string {
	noun := "task"
	if n != 1 {
		noun = "tasks"
	}
	return fmt.Sprintf("%s reached its limit — I moved %d %s to %s.", providerHealthKey(from), n, noun, providerHealthKey(to))
}

func isProviderFailover(t marshal.Task) bool {
	return t.ProviderFailoverPending && t.State == marshal.Reassigned
}
func providerFailoverBrief(t marshal.Task, brief string) string {
	if isProviderFailover(t) {
		return t.FailoverBrief
	}
	return brief
}

func finishProviderFailoverDispatch(t *marshal.Task) {
	t.ProviderFailoverPending = false
	t.FailoverBrief = ""
}

func (s *MarshalService) saveFailoverDecision(ctx context.Context, runID string, run marshal.Run, rev int64, taskID string, kind events.EventType, data map[string]any) error {
	event, err := s.decisionEvent(runID, taskID, kind, data)
	if err != nil {
		return err
	}
	return s.Store.SaveMarshalState(ctx, s.ProjectID, runID, run, rev, &event, nil, 0)
}

// admitProviderDispatch prevents queued and returned tasks from launching a
// cooling provider, including calls made directly to Dispatch.
func (s *MarshalService) admitProviderDispatch(ctx context.Context, runID string, run *marshal.Run, rev int64, index int, brief string) (int64, error) {
	t := &run.Tasks[index]
	now := s.clock()
	// A worker the limits reader reports known-empty is cooling too.
	s.markKnownEmpty(t.Worker, now)
	if !isProviderUnavailable(t.Worker, now) {
		return rev, nil
	}
	drivers := s.Drivers
	if t.Mode == marshal.Governed && s.GovernedDrivers != nil {
		drivers = s.GovernedDrivers
	}
	next, err := s.selectFailoverWorker(ctx, t.Worker, t.Mode, drivers, GetEffectiveFailoverOrder(run.Settings))
	if err != nil {
		return rev, err
	}
	if next == t.Worker {
		return rev, nil
	}
	previous := t.Worker
	if run.CloseAuthorization != nil {
		run.CloseAuthorization.Voided = true
	}
	t.Worker = next
	t.State = marshal.Reassigned
	t.ProviderFailoverPending = true
	t.FailoverBrief = brief
	if t.ReturnsByAgent == nil {
		t.ReturnsByAgent = map[string]int{}
	}
	// A cooldown failover is not a reassignment after failed rework: like a
	// limit failover, earlier workers' returns are retired (kept for lane
	// numbering) and the next worker starts with the normal rework
	// allowance. Keeping the earlier worker's entry made NextAfterReturn
	// treat the task as failed by two workers, so the new worker's first
	// genuine return escalated at once.
	retireFailoverReturns(t, next)
	data := map[string]any{"failover": true, "admission": true, "previous_worker": previous, "worker": next, "reason": "provider cooldown"}
	if err = s.saveFailoverDecision(ctx, runID, *run, rev, t.PlanTaskID, events.EventTypeMarshalTaskReassigned, data); err != nil {
		return rev, err
	}
	if s.RecordActivity != nil {
		s.RecordActivity(rerouteActivity(previous, 1, next))
	}
	return rev + 1, nil
}

// providerFailoverPending reports a durable failover decision the dispatch
// rebinds; a manual reassignment never sets it.
func providerFailoverPending(t marshal.Task) bool { return t.ProviderFailoverPending }

func retireFailoverReturns(t *marshal.Task, next string) {
	for agent, returns := range t.ReturnsByAgent {
		t.LaneReworkBase += returns
		if agent != next {
			delete(t.ReturnsByAgent, agent)
		}
	}
	t.ReturnsByAgent[next] = 0
}
