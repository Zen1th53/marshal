package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// ErrWorkerSlotsFull refuses a launch when every worker slot is reserved.
var ErrWorkerSlotsFull = errors.New("worker slots full")

// errWorkerExitHeld reports a worker result held for inspection; its slot
// keeps its capacity.
var errWorkerExitHeld = errors.New("worker exit held for inspection")

type authorizedResumeKey struct{}

// withAuthorizedResume marks recovery started by the person's resume, the
// only recovery that may apply a staged exit while the run is paused.
func withAuthorizedResume(ctx context.Context) context.Context {
	return context.WithValue(ctx, authorizedResumeKey{}, true)
}

func authorizedResume(ctx context.Context) bool {
	ok, _ := ctx.Value(authorizedResumeKey{}).(bool)
	return ok
}

// workerSlotCap is the fixed reservation limit for one run.
func (s *MarshalService) workerSlotCap(run marshal.Run) int {
	if s.WorkerSlotCap > 0 {
		return s.WorkerSlotCap
	}
	_, limit := marshal.EffectiveWorkerLimits(run.Settings)
	return limit
}

func (s *MarshalService) slotFault(point string) error {
	if s.workerSlotFault != nil {
		return s.workerSlotFault(point)
	}
	return nil
}

func slotAttempt(t marshal.Task) int {
	return 1 + t.EvidenceAttemptBase + taskReturns(t)
}

// reserveWorkerSlot adds the reservation for t's launch to next, which the
// caller commits together with the launch intent. Released and acknowledged
// slots are dropped so the run stays bounded.
func (s *MarshalService) reserveWorkerSlot(next *marshal.Run, t marshal.Task, rev int64) error {
	if s.LaunchSupervisor == nil || t.LaunchKey == "" {
		return nil
	}
	kept := next.Slots[:0:0]
	for _, slot := range next.Slots {
		if slot.State == marshal.SlotReleased && slot.Acked {
			continue
		}
		if slot.LaunchKey == t.LaunchKey {
			if slot.Active() {
				return fmt.Errorf("%w: launch %s already holds a worker slot", errWorkerExitHeld, t.LaunchKey)
			}
			continue
		}
		kept = append(kept, slot)
	}
	next.Slots = kept
	if next.ActiveSlots() >= s.workerSlotCap(*next) {
		return fmt.Errorf("%w: %d of %d reserved", ErrWorkerSlotsFull, next.ActiveSlots(), s.workerSlotCap(*next))
	}
	next.Slots = append(next.Slots, marshal.WorkerSlot{
		LaunchKey: t.LaunchKey, TaskID: t.PlanTaskID, Attempt: slotAttempt(t),
		PlanID: next.PlanID, PlanVersion: next.PlanVersion, Lease: next.ExecutionID,
		Revision: rev, State: marshal.SlotReserved,
	})
	return nil
}

// markSlotRunning records that the launch receipt proved the worker started.
func markSlotRunning(run *marshal.Run, key string) {
	if j := run.SlotIndex(key); j >= 0 && run.Slots[j].State == marshal.SlotReserved {
		run.Slots[j].State = marshal.SlotRunning
	}
}

func digest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p))))
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// proveWorkerExit returns the exit record for the slot's worker, or an error
// when the exit is not proved by the owner: a receipt for this launch key
// and a reaped process handle.
func (s *MarshalService) proveWorkerExit(slot marshal.WorkerSlot, h *driver.Handle) (marshal.StagedExit, error) {
	if s.ProveWorkerExit != nil {
		return s.ProveWorkerExit(slot, h)
	}
	receipt, ok := h.LaunchReceipt()
	if !ok || receipt.Key != slot.LaunchKey {
		return marshal.StagedExit{}, fmt.Errorf("%w: worker handle carries no receipt for launch %s", errWorkerExitHeld, slot.LaunchKey)
	}
	if _, _, done := h.Outcome(); !done {
		return marshal.StagedExit{}, fmt.Errorf("%w: worker of launch %s has not exited", errWorkerExitHeld, slot.LaunchKey)
	}
	p := receipt.Process
	return marshal.StagedExit{Proof: marshal.ExitProofExited, ExitDigest: digest(slot.LaunchKey, p.BootID, strconv.Itoa(p.PID), p.Start, marshal.ExitProofExited)}, nil
}

// validateSlotResult checks that a result belongs to the exact reservation:
// same task, attempt, plan and launch, and not superseded by another result.
func validateSlotResult(run marshal.Run, slot marshal.WorkerSlot, t marshal.Task, exit marshal.StagedExit) error {
	if slot.TaskID != t.PlanTaskID || slot.Attempt != slotAttempt(t) || slot.PlanID != run.PlanID || slot.PlanVersion != run.PlanVersion || slot.LaunchKey != t.LaunchKey {
		return fmt.Errorf("%w: result does not match the reservation of launch %s", errWorkerExitHeld, slot.LaunchKey)
	}
	if slot.Quarantined || slot.State == marshal.SlotHeld {
		return fmt.Errorf("%w: launch %s is held: %s", errWorkerExitHeld, slot.LaunchKey, slot.Reason)
	}
	if slot.State == marshal.SlotReleased {
		return fmt.Errorf("%w: launch %s already closed its slot", errWorkerExitHeld, slot.LaunchKey)
	}
	if slot.Exit != nil && (slot.Exit.EvidenceDigest != exit.EvidenceDigest || slot.Exit.ExitDigest != exit.ExitDigest) {
		return fmt.Errorf("%w: launch %s staged different evidence", errWorkerExitHeld, slot.LaunchKey)
	}
	return nil
}

// stageWorkerResult proves the worker's exit and returns the staged entry
// (before apply) and the released entry (after apply, naming receipt). A
// nil handin stages an exit that delivered no result.
func (s *MarshalService) stageWorkerResult(run marshal.Run, slot marshal.WorkerSlot, t marshal.Task, h *driver.Handle, handin *marshal.HandIn, valid bool, receipt string) (staged, released marshal.WorkerSlot, err error) {
	exit, err := s.proveWorkerExit(slot, h)
	if err != nil {
		return slot, slot, err
	}
	if handin != nil {
		data, err := json.Marshal(handin)
		if err != nil {
			return slot, slot, err
		}
		exit.EvidenceDigest = digest(string(data))
		exit.HandInRef = handin.ResultCommit
		exit.HandInValid = valid
	}
	if err := validateSlotResult(run, slot, t, exit); err != nil {
		return slot, slot, err
	}
	staged = slot
	staged.State = marshal.SlotStaged
	staged.Exit = &exit
	released = staged
	applied := exit
	applied.Receipt = receipt
	released.State = marshal.SlotReleased
	released.Exit = &applied
	return staged, released, nil
}

// holdWorkerSlot keeps a slot's capacity and records why its result waits
// for inspection. The collecting operation opID ends with nothing adopted.
func (s *MarshalService) holdWorkerSlot(ctx context.Context, runID, key, opID string, cause error) error {
	ctx = context.WithoutCancel(ctx)
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return errors.Join(cause, err)
	}
	j := run.SlotIndex(key)
	if j < 0 {
		return cause
	}
	run.Slots[j].State = marshal.SlotHeld
	run.Slots[j].Reason = cause.Error()
	if run.Operation != nil && run.Operation.ID == opID {
		run.Operation = nil
	}
	return errors.Join(cause, s.save(ctx, runID, run, rev))
}

// ackWorkerSlot tells the launch supervisor (for an adopted result) and the
// scheduler that a slot is free, then records the acknowledgement. Recovery
// repeats it until it succeeds; the release itself is already committed. A
// release without a result keeps the launch receipt, so the worker that ran
// is never launched again.
func (s *MarshalService) ackWorkerSlot(ctx context.Context, runID, key string) error {
	if err := s.slotFault("ack"); err != nil {
		return err
	}
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	j := run.SlotIndex(key)
	if j < 0 || run.Slots[j].State != marshal.SlotReleased || run.Slots[j].Acked {
		return nil
	}
	if exit := run.Slots[j].Exit; exit != nil && exit.EvidenceDigest != "" {
		if err := s.LaunchSupervisor.Finish(key); err != nil {
			return err
		}
	}
	if s.WorkerSlotReleased != nil {
		if err := s.WorkerSlotReleased(runID, key); err != nil {
			return err
		}
	}
	if err := s.slotFault("acked"); err != nil {
		return err
	}
	run.Slots[j].Acked = true
	return s.save(ctx, runID, run, rev)
}

// quarantineStagedExit keeps a staged hand-in and its reservation untouched
// while the run is paused, unless the person resumed it. It reports whether
// the exit was quarantined.
func (s *MarshalService) quarantineStagedExit(ctx context.Context, runID string, run *marshal.Run, rev int64, op *marshal.LifecycleOperation) (bool, int64, error) {
	if s.LaunchSupervisor == nil || run.Pause == nil || authorizedResume(ctx) {
		return false, rev, nil
	}
	changed := false
	for j := range run.Slots {
		if run.Slots[j].TaskID == op.TaskID && run.Slots[j].State == marshal.SlotStaged && !run.Slots[j].Quarantined {
			run.Slots[j].Quarantined = true
			run.Slots[j].Reason = "run paused; staged result waits for resume"
			changed = true
		}
	}
	staged := false
	for _, slot := range run.Slots {
		staged = staged || (slot.TaskID == op.TaskID && slot.State == marshal.SlotStaged)
	}
	if !staged {
		return false, rev, nil
	}
	if !changed {
		return true, rev, nil
	}
	if err := s.save(ctx, runID, *run, rev); err != nil {
		return true, rev, err
	}
	return true, rev + 1, nil
}

// reconcileWorkerSlots runs once no lifecycle operation is pending. It
// re-acknowledges released slots and, for slots of workers no longer owned
// by this process, releases capacity only on proved exit or absence. It
// never starts effects or advances review.
func (s *MarshalService) reconcileWorkerSlots(ctx context.Context, runID string, run marshal.Run, rev int64, owned bool) (marshal.Run, int64, error) {
	if s.LaunchSupervisor == nil || run.Operation != nil || len(run.Slots) == 0 {
		return run, rev, nil
	}
	changed := false
	var acks []string
	for j := range run.Slots {
		slot := &run.Slots[j]
		switch {
		case slot.State == marshal.SlotReleased:
			if !slot.Acked {
				acks = append(acks, slot.LaunchKey)
			}
		case owned || slot.State == marshal.SlotStaged:
			// A live owner's worker or a staged result: its writer decides.
		default:
			disp, err := s.LaunchSupervisor.Inspect(slot.LaunchKey)
			if err != nil {
				disp = driver.LaunchAmbiguous
			}
			switch disp {
			case driver.LaunchExited, driver.LaunchAbsent:
				proof := marshal.ExitProofExited
				if disp == driver.LaunchAbsent {
					proof = marshal.ExitProofAbsent
				}
				slot.State = marshal.SlotReleased
				slot.Exit = &marshal.StagedExit{Proof: proof, ExitDigest: digest(slot.LaunchKey, string(disp))}
				acks = append(acks, slot.LaunchKey)
				changed = true
			case driver.LaunchAmbiguous:
				if slot.State != marshal.SlotHeld {
					slot.State = marshal.SlotHeld
					slot.Reason = "worker launch is ambiguous"
					changed = true
				}
			}
		}
	}
	if changed {
		if err := s.save(ctx, runID, run, rev); err != nil {
			return run, rev, err
		}
		rev++
	}
	for _, key := range acks {
		// The release is committed; a failed notice stays unacknowledged and
		// the next recovery repeats it.
		_ = s.ackWorkerSlot(ctx, runID, key)
	}
	if len(acks) > 0 {
		fresh, freshRev, err := s.load(ctx, runID)
		if err != nil {
			return run, rev, err
		}
		run, rev = fresh, freshRev
	}
	return run, rev, nil
}
