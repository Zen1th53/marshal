//go:build governed

package tui

import "testing"

func TestPlanReadyActivityRetainsGovernedApprovalWording(t *testing.T) {
	if got, want := planReadyActivity(2), "A plan with 2 tasks is ready. Review it and approve it in the popup."; got != want {
		t.Fatalf("planReadyActivity(2) = %q, want %q", got, want)
	}
}
