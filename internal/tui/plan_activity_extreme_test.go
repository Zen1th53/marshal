//go:build !governed

package tui

import "testing"

func TestPlanReadyActivitySaysMarshalApprovedAndTasksStart(t *testing.T) {
	for n, want := range map[int]string{
		1: "A plan with 1 task is ready; the Marshal approved it and the tasks start now.",
		2: "A plan with 2 tasks is ready; the Marshal approved it and the tasks start now.",
	} {
		if got := planReadyActivity(n, true); got != want {
			t.Errorf("planReadyActivity(%d, true) = %q, want %q", n, got, want)
		}
	}
	// Without an approval the line never says the tasks start.
	if got, want := planReadyActivity(4, false), "A plan with 4 tasks is in; the Marshal was asked to approve it, and the tasks start when it does."; got != want {
		t.Errorf("planReadyActivity(4, false) = %q, want %q", got, want)
	}
}
