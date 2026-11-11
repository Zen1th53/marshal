//go:build governed

package tui

import "testing"

func TestCommunityNavigationReleaseAndTaskGapsUnchanged(t *testing.T) {
	if len(buildTaskControlOperations) != 0 {
		t.Fatal("private navigation configuration leaked into Community")
	}
	ia, err := FrozenIA()
	if err != nil || len(ia.Gaps()) != 2 {
		t.Fatal("Community frozen manifest gaps changed", err)
	}
	source, _ := testControl(t)
	// Community binds task pause, resume and retry only through the canonical
	// CommandTask authority; without it the controls stay unavailable.
	for _, id := range []ActionID{"CTUI-0043", "CTUI-0044", "CTUI-0045"} {
		binding := source.Bindings()[id]
		if binding.Bound() && binding.Gap != "" {
			t.Fatal("task control is both bound and a gap")
		}
	}
	view, _ := testControlView(t)
	missing, _ := ia.Node("CTUI-0203")
	if view.buildActionAvailability(missing) != Availability(missing) {
		t.Fatal("Marshal rendering policy leaked into Community")
	}
}
