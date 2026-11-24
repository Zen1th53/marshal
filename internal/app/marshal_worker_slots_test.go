package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
)

type slotFixture struct {
	launchFixture
	notified *int
}

func newSlotFixture(t *testing.T) slotFixture {
	t.Helper()
	f := newLaunchFixture(t)
	f.s.WorkerSlotCap = 1
	notified := 0
	f.s.WorkerSlotReleased = func(runID, key string) error {
		notified++
		return nil
	}
	return slotFixture{launchFixture: f, notified: &notified}
}

func (f slotFixture) run(t *testing.T) (marshal.Run, int64) {
	t.Helper()
	run, rev, err := f.s.load(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	return run, rev
}

func (f slotFixture) slot(t *testing.T, key string) marshal.WorkerSlot {
	t.Helper()
	run, _ := f.run(t)
	j := run.SlotIndex(key)
	if j < 0 {
		t.Fatalf("no slot for launch %s: %+v", key, run.Slots)
	}
	return run.Slots[j]
}

// otherLaunchFits asks the fixed-cap evaluator whether another launch could
// reserve a slot now.
func (f slotFixture) otherLaunchFits(t *testing.T) bool {
	t.Helper()
	run, rev := f.run(t)
	other := marshal.Task{PlanTaskID: "z", LaunchKey: "marshal-launch-other"}
	err := f.s.reserveWorkerSlot(&run, other, rev)
	if err != nil && !errors.Is(err, ErrWorkerSlotsFull) {
		t.Fatal(err)
	}
	return err == nil
}

func (f slotFixture) handIns(t *testing.T) int {
	t.Helper()
	_, err := f.s.Store.GetMarshalHandIn(context.Background(), "run", "a", 1)
	if errors.Is(err, model.ErrNotFound) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return 1
}

func (f slotFixture) dispatch(t *testing.T) (MarshalDispatch, string) {
	t.Helper()
	d, err := f.s.Dispatch(t.Context(), "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := d.Handle.LaunchReceipt()
	if !ok {
		t.Fatal("supervised dispatch carries no receipt")
	}
	if slot := f.slot(t, rec.Key); slot.State != marshal.SlotRunning {
		t.Fatalf("started worker's slot is %s", slot.State)
	}
	return d, rec.Key
}

func (f slotFixture) expectApplied(t *testing.T, key string, notices int) {
	t.Helper()
	slot := f.slot(t, key)
	if slot.State != marshal.SlotReleased || !slot.Acked || slot.Exit == nil || slot.Exit.Receipt == "" || slot.Exit.Proof != marshal.ExitProofExited {
		t.Fatalf("staged exit not applied and acknowledged: %+v exit=%+v", slot, slot.Exit)
	}
	run, _ := f.run(t)
	if run.Operation != nil || run.Tasks[0].State != marshal.HandedIn {
		t.Fatalf("result not committed: op=%+v task=%s", run.Operation, run.Tasks[0].State)
	}
	if f.handIns(t) != 1 || *f.notified != notices {
		t.Fatalf("hand-ins=%d notices=%d, want 1 and %d", f.handIns(t), *f.notified, notices)
	}
	if !f.otherLaunchFits(t) {
		t.Fatal("released slot still counts against capacity")
	}
}

func (f slotFixture) expectStagedOnly(t *testing.T, key string) marshal.Run {
	t.Helper()
	run, _ := f.run(t)
	slot := f.slot(t, key)
	if run.Operation == nil || run.Operation.HandIn == nil || slot.State != marshal.SlotStaged || slot.Exit == nil || slot.Exit.EvidenceDigest == "" {
		t.Fatalf("staged exit not retained: op=%+v slot=%+v", run.Operation, slot)
	}
	if run.Tasks[0].State != marshal.Dispatched || f.handIns(t) != 0 || *f.notified != 0 {
		t.Fatalf("staged exit applied early: task=%s hand-ins=%d notices=%d", run.Tasks[0].State, f.handIns(t), *f.notified)
	}
	if f.otherLaunchFits(t) {
		t.Fatal("staged exit released its reservation before apply")
	}
	return run
}

func TestStagedHandInReplaysOnceAcrossPauseLeaseLossAndShutdown(t *testing.T) {
	for _, point := range []string{"reserve", "reserved"} {
		t.Run("fault at "+point, func(t *testing.T) {
			f := newSlotFixture(t)
			f.s.workerSlotFault = func(p string) error {
				if p == point {
					return fmt.Errorf("injected %s fault", p)
				}
				return nil
			}
			if _, err := f.s.Dispatch(t.Context(), "run", "a", "write"); err == nil {
				t.Fatal("fault not observed")
			}
			run, _ := f.run(t)
			if run.Operation != nil || len(run.Slots) != 0 || run.Tasks[0].State != marshal.Queued {
				t.Fatalf("refused launch kept a reservation: op=%+v slots=%+v", run.Operation, run.Slots)
			}
			f.expectWorks(t, 0)
			f.s.workerSlotFault = nil
			d, key := f.dispatch(t)
			if _, err := f.s.CollectHandIn(t.Context(), "run", d); err != nil {
				t.Fatal(err)
			}
			f.expectApplied(t, key, 1)
			f.expectWorks(t, 1)
		})
	}

	t.Run("staged exit across lease loss, shutdown, pause and resume", func(t *testing.T) {
		f := newSlotFixture(t)
		d, key := f.dispatch(t)
		f.s.AfterLifecycleEffect = func(kind, _ string) error {
			if kind == "hand-in" {
				return context.Canceled
			}
			return nil
		}
		if _, err := f.s.CollectHandIn(t.Context(), "run", d); !errors.Is(err, context.Canceled) {
			t.Fatalf("crash after staging not observed: %v", err)
		}
		f.s.AfterLifecycleEffect = nil
		staged := f.expectStagedOnly(t, key)

		lease := errors.New("lifecycle lease lost")
		f.s.requireOwner = func() error { return lease }
		if err := f.s.RecoverPendingOperations(t.Context()); !errors.Is(err, lease) {
			t.Fatalf("recovery ran without the lease: %v", err)
		}
		f.s.requireOwner = nil
		f.expectStagedOnly(t, key)

		stopped, cancel := context.WithCancel(t.Context())
		cancel()
		_ = f.s.RecoverPendingOperations(stopped)
		f.expectStagedOnly(t, key)

		_, rev := f.run(t)
		pauseMarshal(&staged, marshal.Pause{Reason: "owner paused", Resolutions: []string{"resume"}, Code: marshal.CodeOperatorIntervention, Action: marshal.PauseResume})
		if err := f.s.save(t.Context(), "run", staged, rev); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := f.s.RecoverPendingOperations(t.Context()); err != nil {
				t.Fatal(err)
			}
			f.expectStagedOnly(t, key)
			if !f.slot(t, key).Quarantined {
				t.Fatal("paused run did not quarantine its staged exit")
			}
		}

		if _, err := f.s.Resume(t.Context(), "run"); err != nil {
			t.Fatal(err)
		}
		f.expectApplied(t, key, 1)
		if f.slot(t, key).Quarantined {
			t.Fatal("applied exit still quarantined")
		}

		// Replays after apply change nothing and return the stored receipt.
		receipt := f.slot(t, key).Exit.Receipt
		for range 2 {
			f.restart(t)
			f.expectApplied(t, key, 1)
			if got := f.slot(t, key).Exit.Receipt; got != receipt {
				t.Fatalf("replay rewrote the receipt: %s, want %s", got, receipt)
			}
		}
		if disp, err := f.s.LaunchSupervisor.Inspect(key); err != nil || disp != driver.LaunchAbsent {
			t.Fatalf("adopted result kept its launch record: %s %v", disp, err)
		}
		f.expectWorks(t, 1)
	})

	t.Run("fault at apply replays the staged entry", func(t *testing.T) {
		f := newSlotFixture(t)
		d, key := f.dispatch(t)
		f.s.workerSlotFault = func(p string) error {
			if p == "apply" {
				return errors.New("injected apply fault")
			}
			return nil
		}
		if _, err := f.s.CollectHandIn(t.Context(), "run", d); err == nil {
			t.Fatal("apply fault not observed")
		}
		f.s.workerSlotFault = nil
		f.expectStagedOnly(t, key)
		f.restart(t)
		f.expectApplied(t, key, 1)
		f.restart(t)
		f.expectApplied(t, key, 1)
	})

	t.Run("fault at ack repeats only the acknowledgement", func(t *testing.T) {
		f := newSlotFixture(t)
		d, key := f.dispatch(t)
		f.s.workerSlotFault = func(p string) error {
			if p == "ack" {
				return errors.New("injected ack fault")
			}
			return nil
		}
		if _, err := f.s.CollectHandIn(t.Context(), "run", d); err != nil {
			t.Fatalf("committed result reported as failed: %v", err)
		}
		slot := f.slot(t, key)
		if slot.State != marshal.SlotReleased || slot.Acked || f.handIns(t) != 1 || *f.notified != 0 {
			t.Fatalf("ack fault: slot=%+v hand-ins=%d notices=%d", slot, f.handIns(t), *f.notified)
		}
		f.s.workerSlotFault = nil
		f.restart(t)
		f.expectApplied(t, key, 1)
		f.restart(t)
		f.expectApplied(t, key, 1)
	})

	t.Run("fault after notice repeats the notice once more", func(t *testing.T) {
		f := newSlotFixture(t)
		d, key := f.dispatch(t)
		f.s.workerSlotFault = func(p string) error {
			if p == "acked" {
				return errors.New("injected fault after notice")
			}
			return nil
		}
		if _, err := f.s.CollectHandIn(t.Context(), "run", d); err != nil {
			t.Fatalf("committed result reported as failed: %v", err)
		}
		if slot := f.slot(t, key); slot.State != marshal.SlotReleased || slot.Acked || *f.notified != 1 {
			t.Fatalf("fault after notice: slot=%+v notices=%d", slot, *f.notified)
		}
		f.s.workerSlotFault = nil
		f.restart(t)
		f.expectApplied(t, key, 2)
		f.restart(t)
		f.expectApplied(t, key, 2)
	})

	t.Run("fault at stage holds nothing new", func(t *testing.T) {
		f := newSlotFixture(t)
		d, key := f.dispatch(t)
		f.s.workerSlotFault = func(p string) error {
			if p == "stage" {
				return errors.New("injected stage fault")
			}
			return nil
		}
		if _, err := f.s.CollectHandIn(t.Context(), "run", d); err == nil {
			t.Fatal("stage fault not observed")
		}
		f.s.workerSlotFault = nil
		if slot := f.slot(t, key); slot.State != marshal.SlotRunning || slot.Exit != nil || f.handIns(t) != 0 {
			t.Fatalf("unstaged exit changed the slot: %+v", slot)
		}
		if f.otherLaunchFits(t) {
			t.Fatal("unstaged exit released capacity")
		}
	})
}

func TestApprovalAndReviewReleaseOnlyExitedWorkerSlots(t *testing.T) {
	t.Run("live process awaiting approval keeps its slot", func(t *testing.T) {
		f := newSlotFixture(t)
		d, key := f.dispatch(t)
		if f.otherLaunchFits(t) {
			t.Fatal("running worker's slot did not count against capacity")
		}
		f.s.ProveWorkerExit = func(slot marshal.WorkerSlot, _ *driver.Handle) (marshal.StagedExit, error) {
			return marshal.StagedExit{}, fmt.Errorf("%w: worker of %s is live and awaiting approval", errWorkerExitHeld, slot.LaunchKey)
		}
		if _, err := f.s.CollectHandIn(t.Context(), "run", d); !errors.Is(err, errWorkerExitHeld) {
			t.Fatalf("unproved exit adopted: %v", err)
		}
		slot := f.slot(t, key)
		run, _ := f.run(t)
		if slot.State != marshal.SlotHeld || slot.Exit != nil || run.Tasks[0].State != marshal.Dispatched || f.handIns(t) != 0 || *f.notified != 0 {
			t.Fatalf("live worker released: slot=%+v task=%s", slot, run.Tasks[0].State)
		}
		if f.otherLaunchFits(t) {
			t.Fatal("live worker's slot freed capacity")
		}
		if disp, err := f.s.LaunchSupervisor.Inspect(key); err != nil || disp != driver.LaunchRunning {
			t.Fatalf("live worker's receipt lost: %s %v", disp, err)
		}
		// A restart still finds the live worker and keeps the slot held.
		f.restart(t)
		if f.otherLaunchFits(t) || f.slot(t, key).State != marshal.SlotHeld {
			t.Fatal("restart released a live worker's slot")
		}
		// Once the process is gone, reconcile frees capacity without adopting.
		f.host.reboot()
		run, rev := f.run(t)
		if _, _, err := f.s.reconcileWorkerSlots(t.Context(), "run", run, rev, false); err != nil {
			t.Fatal(err)
		}
		if slot := f.slot(t, key); slot.State != marshal.SlotReleased || !slot.Acked || slot.Exit.EvidenceDigest != "" || f.handIns(t) != 0 {
			t.Fatalf("confirmed exit not reconciled: %+v", slot)
		}
		if run, _ := f.run(t); run.Tasks[0].State != marshal.Dispatched {
			t.Fatalf("reconcile advanced review: %s", run.Tasks[0].State)
		}
		if !f.otherLaunchFits(t) {
			t.Fatal("confirmed exit kept its slot")
		}
	})

	t.Run("hand-in pending review frees an exited slot", func(t *testing.T) {
		f := newSlotFixture(t)
		d, key := f.dispatch(t)
		if _, err := f.s.CollectHandIn(t.Context(), "run", d); err != nil {
			t.Fatal(err)
		}
		f.expectApplied(t, key, 1)
		if !f.slot(t, key).Exit.HandInValid {
			t.Fatal("valid hand-in staged as invalid")
		}
	})

	t.Run("invalid hand-in frees an exited slot", func(t *testing.T) {
		f := newSlotFixture(t)
		f.worker.inner.Args = func(driver.Request) []string { return []string{"-c", "true"} }
		d, key := f.dispatch(t)
		if _, err := f.s.CollectHandIn(t.Context(), "run", d); err != nil {
			t.Fatal(err)
		}
		if run, _ := f.run(t); run.Operation != nil || run.Tasks[0].State == marshal.Dispatched || run.Tasks[0].State == marshal.HandedIn || f.handIns(t) != 1 {
			t.Fatalf("invalid hand-in not recorded and returned: op=%+v task=%s hand-ins=%d", run.Operation, run.Tasks[0].State, f.handIns(t))
		}
		slot := f.slot(t, key)
		if slot.State != marshal.SlotReleased || slot.Exit == nil || slot.Exit.HandInValid || !slot.Acked {
			t.Fatalf("invalid hand-in slot: %+v exit=%+v", slot, slot.Exit)
		}
		if !f.otherLaunchFits(t) {
			t.Fatal("invalid hand-in kept its slot")
		}
	})

	t.Run("mismatched result is held", func(t *testing.T) {
		f := newSlotFixture(t)
		d, key := f.dispatch(t)
		run, rev := f.run(t)
		run.Slots[run.SlotIndex(key)].Attempt = 7
		if err := f.s.save(t.Context(), "run", run, rev); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.CollectHandIn(t.Context(), "run", d); !errors.Is(err, errWorkerExitHeld) {
			t.Fatalf("mismatched result adopted: %v", err)
		}
		if slot := f.slot(t, key); slot.State != marshal.SlotHeld || f.handIns(t) != 0 {
			t.Fatalf("mismatched result changed the slot: %+v", slot)
		}
	})

	t.Run("store refuses a release without proof", func(t *testing.T) {
		f := newSlotFixture(t)
		_, key := f.dispatch(t)
		run, rev := f.run(t)
		run.Slots[run.SlotIndex(key)].State = marshal.SlotReleased
		if err := f.s.save(t.Context(), "run", run, rev); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("self-attested release stored: %v", err)
		}
	})
}
