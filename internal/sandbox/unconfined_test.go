package sandbox

import "testing"

func TestUnconfinedFallbackPolicyMatchesEdition(t *testing.T) {
	if got, want := UnconfinedFallbackAllowed(), editionIsExtreme; got != want {
		t.Fatalf("UnconfinedFallbackAllowed() = %v, want %v", got, want)
	}
}
