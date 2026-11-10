package tui

import (
	"strings"
	"testing"
)

func TestEditionLabelsMatchEdition(t *testing.T) {
	got := communityLabel("MARSHAL — COMMUNITY TUI / Status / Community Cloud & ULTRA")
	if editionExtreme {
		if strings.Contains(got, "COMMUNITY") || strings.Contains(got, "Community Cloud") {
			t.Fatalf("Marshal shows Community wording: %q", got)
		}
	} else if !strings.Contains(got, "Community Cloud & ULTRA") {
		t.Fatalf("Community lost its wording: %q", got)
	}
}
