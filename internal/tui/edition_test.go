package tui

import (
	"context"
	"testing"
)

func TestEditionNavigationAndCapabilityBoundaries(t *testing.T) {
	if editionExtreme {
		if Edition() != "Marshal" {
			t.Fatalf("Edition() = %q, want extreme", Edition())
		}
	} else {
		if Edition() != "standard" {
			t.Fatalf("Edition() = %q, want standard", Edition())
		}
	}

	// ULTRA-only operations still consult their existing authority and
	// cannot be granted locally.
	source, auth := testControl(t)
	outcome, err := source.executeULTRAMode(context.Background(), ActionRequest{Action: "CTUI-0033"})
	if err != nil {
		t.Fatalf("ULTRA action returned error: %v", err)
	}
	if outcome.Verdict != VerdictBlocked || auth.ULTRAEntitled() {
		t.Fatalf("ULTRA action outcome=%s entitled=%t, want BLOCKED and unentitled", outcome.Verdict, auth.ULTRAEntitled())
	}
}
