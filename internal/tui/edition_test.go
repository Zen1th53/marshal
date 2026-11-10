package tui

import "testing"

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
}
