//go:build !governed

package tui

import "testing"

func TestPlanReadyActivitySaysMarshalApprovedAndTasksStart(t *testing.T) {
	for n, want := range map[int]string{
		1: "A plan with 1 task is ready; the Marshal approved it and the tasks start now.",
		2: "A plan with 2 tasks is ready; the Marshal approved it and the tasks start now.",
	} {
		if got := planReadyActivity(n); got != want {
			t.Errorf("planReadyActivity(%d) = %q, want %q", n, got, want)
		}
	}
}
