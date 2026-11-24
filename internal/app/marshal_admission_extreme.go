//go:build !governed

package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Gradual admission after a limit reset. When a provider's cooldown ends,
// every task that waited on it is ready at once; launching them all in one
// burst can hit the limit again at once. While a provider (or the one model
// of it a task runs on) recovers, admission allows one start immediately,
// then at most one new start per recoveryInterval until the provider's
// normal cap, counting every lane already in use. A recovery opens only from
// a cooldown that ended; when the limits reader is wired and cannot read the
// limit after the reset, the task is held instead.

// recoveryFamilies are the provider health keys whose recovery paces t: its
// provider and, when known, the model it runs on.
func (s *MarshalService) recoveryFamilies(ctx context.Context, run marshal.Run, t marshal.Task, now time.Time) ([]string, string) {
	keys := []string{providerHealthKey(t.Worker)}
	taskModel := s.taskModel(ctx, run, t, now)
	if taskModel != "" {
		keys = append(keys, providerHealthKey(modelHealthKey(t.Worker, taskModel)))
	}
	return keys, taskModel
}

// heldLanes counts the lanes in use for candidate t: tasks running, tasks
// whose exit is not proved, approvals that keep a live worker, and launches
// of this round not yet visible in run.
func heldLanes(run marshal.Run, t marshal.Task, launched []MarshalDispatch) (int, []string) {
	var ids []string
	for _, other := range run.Tasks {
		if holdsWorkerSlot(other, t) {
			ids = append(ids, other.PlanTaskID)
		}
	}
	for _, d := range launched {
		if i := taskIndex(run, d.TaskID); (i < 0 || !holdsWorkerSlot(run.Tasks[i], t)) && d.TaskID != t.PlanTaskID {
			ids = append(ids, d.TaskID)
		}
	}
	return len(ids), ids
}

// recoveryAdmission is the last admission check before t launches at now:
// the lane cap new admissions use (with a temporary host lane cap), the
// recovery pace of its provider and model, and a readable limit after a
// reset. It reads only; the caller takes the start with takeRecoveryStarts.
// It returns the families to take and their cap.
func (s *MarshalService) recoveryAdmission(ctx context.Context, gate marshal.CapabilityGate, run marshal.Run, t marshal.Task, launched []MarshalDispatch, now time.Time) (DispatchAdmission, []string, int) {
	adm := DispatchAdmission{Allowed: true, TaskID: safeAdmissionTaskID(t.PlanTaskID), Provider: providerHealthKey(t.Worker)}
	if marshal.TierPolicy(gate, run.Settings).Tier != marshal.Ultra {
		return adm, nil, 0
	}
	limits, laneCap := extremeProviderLimits(run.Settings)
	if s.LaneCap != nil {
		if c := s.LaneCap(laneCap); c > 0 && c < laneCap {
			laneCap = c
		}
	}
	adm.EffectiveCap = laneCap
	held, ids := heldLanes(run, t, launched)
	if held >= laneCap {
		adm.Allowed = false
		adm.Code = AdmissionCapacity
		adm.Reason = fmt.Sprintf("every lane new work may use is taken (%d of %d)", held, laneCap)
		adm.BlockingTaskIDs = safeAdmissionTaskIDs(ids)
		return adm, nil, 0
	}
	rampCap := min(extremeProviderLimit(run, limits, normalizeProvider(t.Worker)), laneCap)
	keys, taskModel := s.recoveryFamilies(ctx, run, t, now)
	for i, key := range keys {
		if !providerHealth.Recovering(key, now, rampCap) {
			continue
		}
		if !s.limitKnown(t.Worker, taskModel, i > 0) {
			adm.Allowed = false
			adm.Code = AdmissionQuotaUnknown
			adm.Reason = fmt.Sprintf("%s's usage limit cannot be read after its reset; refresh its usage or approve a plan change", adm.Provider)
			return adm, nil, 0
		}
		if retry, ok := providerHealth.RampAdmit(key, now, rampCap); !ok {
			adm.Allowed = false
			adm.Code = AdmissionRecoveryRamp
			adm.Reason = fmt.Sprintf("%s is back from its usage limit: new tasks start one every %s", adm.Provider, recoveryInterval)
			adm.RetryAt = &retry
			return adm, nil, 0
		}
	}
	return adm, keys, rampCap
}

// limitKnown reports whether the limits reader can read worker's limit
// (its model's when model is set). Without a reader, the cooldown's end is
// the only sample there is.
func (s *MarshalService) limitKnown(worker, taskModel string, forModel bool) bool {
	if forModel && s.ModelQuotaLeft != nil {
		_, known := s.ModelQuotaLeft(worker, taskModel)
		return known
	}
	if s.QuotaLeft == nil {
		return true
	}
	_, known := s.QuotaLeft(worker)
	return known
}

// takeRecoveryStarts records a start at now on every recovering family and
// returns the undo for a start that did not begin.
func (s *MarshalService) takeRecoveryStarts(keys []string, now time.Time, rampCap int) func() {
	undos := make([]func(), 0, len(keys))
	for _, key := range keys {
		undos = append(undos, providerHealth.RampTake(key, now, rampCap))
	}
	return func() {
		for _, undo := range undos {
			undo()
		}
	}
}

// reserveRecoveryStart checks the final provider and atomically takes all
// recovering families, so other projects cannot admit the same interval.
func (s *MarshalService) reserveRecoveryStart(ctx context.Context, run marshal.Run, t marshal.Task) (func(), error) {
	now := s.clock()
	adm, keys, cap := s.recoveryAdmission(ctx, s.admissionGate(ctx), run, t, nil, now)
	if !adm.Allowed {
		return nil, &DispatchAdmissionError{Admission: adm}
	}
	undo, retry, ok := providerHealth.RampReserve(keys, now, cap)
	if !ok {
		adm.Allowed = false
		adm.Code = AdmissionRecoveryRamp
		adm.Reason = fmt.Sprintf("%s is back from its usage limit: new tasks start one every %s", adm.Provider, recoveryInterval)
		adm.RetryAt = &retry
		return nil, &DispatchAdmissionError{Admission: adm}
	}
	return undo, nil
}
