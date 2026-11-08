//go:build !governed

package app

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestExtremeProcess05ApprovalWaitPreservesDeliveryAndTaskBases(t *testing.T) {
	for _, scenario := range []struct {
		name                          string
		lane                          bool
		runBase, actualBase, taskBase string
		observedTaskBase              string
		want                          bool
	}{
		{"serial-dependency-head", false, "initial", "initial", "integrated-dependency", "integrated-dependency", true},
		{"serial-moved-delivery", false, "initial", "foreign", "integrated-dependency", "integrated-dependency", false},
		{"serial-moved-task", false, "initial", "initial", "integrated-dependency", "foreign", false},
		{"serial-old-task-base", false, "initial", "initial", "integrated-dependency", "initial", false},
		{"lane-task-head", true, "initial", "integrated-dependency", "integrated-dependency", "integrated-dependency", true},
		{"lane-parent-head", true, "initial", "initial", "integrated-dependency", "integrated-dependency", false},
		{"lane-moved-task", true, "initial", "integrated-dependency", "integrated-dependency", "foreign", false},
		{"missing-task-base", false, "initial", "initial", "", "", false},
		{"missing-delivery-base", false, "", "", "integrated-dependency", "integrated-dependency", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			actual := execution.ExecutionRun{BaseCommit: scenario.actualBase}
			if scenario.lane {
				actual.ParentRunID = "original-parent"
			}
			if got := process05ApprovalWaitBaseBound(actual, marshal.Run{BaseCommit: scenario.runBase},
				marshal.Task{BaseCommit: scenario.taskBase}, execution.TaskExecution{BaseCommit: scenario.observedTaskBase}); got != scenario.want {
				t.Fatalf("delivery/task base admitted=%v, want %v", got, scenario.want)
			}
		})
	}
}
