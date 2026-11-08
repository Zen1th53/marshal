package tui

import (
	"strings"
	"testing"
)

// Extreme runs ULTRA locally, so the Cloud entitlement request is not offered;
// the Community edition still offers it.
func TestEntitlementRequestOfferedOnlyOutsideExtreme(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatal(err)
	}
	nav, err := NewNavState(ia)
	if err != nil {
		t.Fatal(err)
	}
	if err := nav.DeepLink("CTUI-0030"); err != nil {
		t.Fatal(err)
	}
	offered := false
	for _, child := range nav.Children() {
		if child.SpecID == "CTUI-0035" {
			offered = true
		}
	}
	if offered == editionExtreme {
		t.Fatalf("Request ULTRA entitlement offered=%v in extreme=%v", offered, editionExtreme)
	}
	if got := len(ia.Search("request ultra entitlement")) > 0; editionExtreme && got && searchHasSpec(ia, "CTUI-0035") {
		t.Fatal("palette search must not reach the hidden entitlement request in Marshal")
	}
}

func searchHasSpec(ia *IA, id string) bool {
	for _, n := range ia.Search("request ultra entitlement") {
		if n.SpecID == id {
			return true
		}
	}
	return false
}

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
