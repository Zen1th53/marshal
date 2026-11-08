package tui

import (
	"context"
	"strings"
	"testing"
)

func TestEditionNavigationAndCapabilityBoundaries(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "edition-test")
	ws.navReleased = false
	if editionExtreme {
		if Edition() != "Marshal" {
			t.Fatalf("Edition() = %q, want extreme", Edition())
		}
		if got := ws.navigationRefusal(); got != "" {
			t.Fatalf("Standard navigation refusal = %q, want empty", got)
		}
	} else {
		if Edition() != "standard" {
			t.Fatalf("Edition() = %q, want standard", Edition())
		}
		if got := ws.navigationRefusal(); !strings.Contains(got, "not available yet") {
			t.Fatalf("default navigation refusal = %q, want unreleased message", got)
		}
	}

	// The Marshal exposes the menu while ULTRA-only operations still
	// consult their existing authority and cannot be granted locally.
	source, auth := testControl(t)
	outcome, err := source.executeULTRAMode(context.Background(), ActionRequest{Action: "CTUI-0033"})
	if err != nil {
		t.Fatalf("ULTRA action returned error: %v", err)
	}
	if outcome.Verdict != VerdictBlocked || auth.ULTRAEntitled() {
		t.Fatalf("ULTRA action outcome=%s entitled=%t, want BLOCKED and unentitled", outcome.Verdict, auth.ULTRAEntitled())
	}

	v := testView(t)
	for _, gap := range v.nav.ia.Gaps() {
		if availability := Availability(gap); availability.Enabled || !strings.Contains(availability.Reason, "IMPLEMENTATION GAP") {
			t.Errorf("gap %s availability = %+v", gap.SpecID, availability)
		}
		if _, ok := GapNotice(gap); !ok {
			t.Errorf("gap %s has no implementation-gap notice", gap.SpecID)
		}
	}
}
