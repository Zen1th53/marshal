//go:build governed

package tui

import "testing"

func TestPlanReadyActivityRetainsGovernedApprovalWording(t *testing.T) {
	if got, want := planReadyActivity(2, true), "A plan with 2 tasks is ready. Review it and approve it in the popup."; got != want {
		t.Fatalf("planReadyActivity(2, true) = %q, want %q", got, want)
	}
	if got := planReadyActivity(2, false); got == planReadyActivity(2, true) {
		t.Fatalf("planReadyActivity(2, false) promises a popup that is not queued: %q", got)
	}
}
