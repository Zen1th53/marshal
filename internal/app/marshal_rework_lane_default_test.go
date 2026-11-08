//go:build governed

package app

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestCommunityReturnedTaskKeepsItsReworkPath(t *testing.T) {
	run := marshal.Run{State: marshal.Reviewing, Process05Bound: true, Process05Parallel: true}
	returned := marshal.Task{PlanTaskID: "a", Mode: marshal.Governed, State: marshal.Returned, ReturnsByAgent: map[string]int{"claude": 1}}
	pauseReturnedProcess05Task(&run, returned)
	if run.State != marshal.Reviewing || run.Pause != nil || reworkInTaskLane(run, returned) {
		t.Fatal("Community return behaviour changed")
	}
}
