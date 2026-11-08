package sandbox

import "testing"

// Host execution exists only in the Marshal build, which also allows the
// unconfined fallback; the default build stays confined.
func TestHostExecutionImpliesUnconfinedFallback(t *testing.T) {
	if HostExecutionPreferred() && !UnconfinedFallbackAllowed() {
		t.Fatal("host execution without the unconfined fallback")
	}
}
