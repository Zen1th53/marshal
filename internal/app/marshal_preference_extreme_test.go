//go:build !governed

package app

import (
	"github.com/Zen1th53/marshal/internal/marshal"
	"testing"
)

func TestOffDuringDefaultBatchKeepsAdmittedCollector(t *testing.T) {
	testAdmittedUltraCollector(t, true)
}

func TestUltraOffPreventsNewParallelDispatch(t *testing.T) {
	gate := &collectorPreferenceGate{}
	gate.enabled.Store(true)
	admitted, release := reserveExecution(gate)
	release()
	gate.mu.Lock()
	gate.enabled.Store(false)
	gate.mu.Unlock()
	run := marshal.Run{Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{
		{PlanTaskID: "a", State: marshal.Dispatched},
		{PlanTaskID: "b", State: marshal.Queued},
	}}
	if marshal.TierPolicy(admitted, run.Settings).Tier != marshal.Ultra {
		t.Fatal("reserved mode changed")
	}
	if can, _ := canLaunchCandidate(gate, run, run.Tasks[1], nil); can {
		t.Fatal("off admitted work beside active worker")
	}
	if err := checkDispatchAdmission(gate, run, run.Tasks[1], 1); err == nil {
		t.Fatal("direct dispatch bypassed off")
	}
	run.Tasks[0].State = marshal.Merged
	if err := checkDispatchAdmission(gate, run, run.Tasks[1], 1); err != nil {
		t.Fatal(err)
	}
}

func TestApprovedUltraDrainsToSerialWithoutRebinding(t *testing.T) {
	gate := &collectorPreferenceGate{}
	run := marshal.Run{Tier: marshal.Ultra, Process05Bound: true, Process05Parallel: true, PlanID: "approved", PlanVersion: 3, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{
		{PlanTaskID: "a", Mode: marshal.Governed, State: marshal.Queued},
		{PlanTaskID: "b", Mode: marshal.Governed, State: marshal.Dispatched},
	}}
	if err := checkDispatchAdmission(gate, run, run.Tasks[0], 0); err == nil {
		t.Fatal("active worker did not drain")
	}
	run.Tasks[1].State = marshal.Merged
	if err := checkDispatchAdmission(gate, run, run.Tasks[0], 0); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterLaunchGate(gate, run, run.Tasks[0]) {
		t.Fatal("serial admission did not stop")
	}
	if marshal.TierPolicy(gate, run.Settings).Tier != marshal.Standard || !run.Process05Parallel || run.Tier != marshal.Ultra || run.PlanID != "approved" || run.PlanVersion != 3 {
		t.Fatalf("binding changed: %+v", run)
	}
}

func TestOffAfterBatchReservationKeepsAdmittedMode(t *testing.T) {
	testAdmittedUltraCollector(t, true, true)
}
