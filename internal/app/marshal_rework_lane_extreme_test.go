//go:build !governed

package app

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestExtremeReturnedLaneTaskReworksInsideItsRun(t *testing.T) {
	parallel := marshal.Run{State: marshal.Reviewing, Process05Bound: true, Process05Parallel: true}
	returned := marshal.Task{PlanTaskID: "a", Mode: marshal.Governed, State: marshal.Returned, ReturnsByAgent: map[string]int{"claude": 1}}
	run := parallel
	pauseReturnedProcess05Task(&run, returned)
	if run.State != marshal.Reviewing || run.Pause != nil {
		t.Fatalf("returned lane task stopped the run: %+v", run.Pause)
	}
	if !reworkInTaskLane(parallel, returned) || taskLaneRework(returned) != 1 {
		t.Fatal("returned lane task is not reworked in its next lane")
	}
	// The worker's rework limit still stops the run for the person.
	reassigned := returned
	reassigned.State = marshal.Reassigned
	reassigned.ReturnsByAgent = map[string]int{"claude": 2}
	run = parallel
	pauseReturnedProcess05Task(&run, reassigned)
	if run.State != marshal.AwaitingUser || run.Pause == nil || !strings.Contains(run.Pause.Reason, "rework limit") || reworkInTaskLane(parallel, reassigned) {
		t.Fatalf("rework limit did not escalate: %+v", run.Pause)
	}
	// A serial Process05 run keeps the fresh-plan requirement.
	serial := marshal.Run{State: marshal.Reviewing, Process05Bound: true}
	pauseReturnedProcess05Task(&serial, returned)
	if serial.Pause == nil || !strings.Contains(serial.Pause.Reason, "requires a new approved Process 04 plan") || reworkInTaskLane(serial, returned) {
		t.Fatalf("serial Process05 return changed: %+v", serial.Pause)
	}
	native := returned
	native.Mode = marshal.Native
	if reworkInTaskLane(parallel, native) {
		t.Fatal("a native task used a governed task lane rework")
	}
}
