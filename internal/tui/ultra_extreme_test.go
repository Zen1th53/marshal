//go:build !governed

package tui

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// The shipped build starts with ULTRA execution on, and no /ultra form can
// switch it off or weaken what the Marshal dispatches.
func TestExtremeSessionStartsUltraAndCannotStop(t *testing.T) {
	fakeProviderCLIs(t, "codex", "claude", "opencode", "agy")
	w := NewWorkspace(nil, "proj", "session")
	w.AttachULTRASession(cloud.Authorize(t.Context(), cloud.Config{}, t.TempDir(), "test"))
	assertUltraDispatch := func(when string) {
		t.Helper()
		gate, execution := w.ultraGate()
		if !execution || !gate.Entitled() || !ultraActive(w.GetUIState()) {
			t.Fatalf("%s: ULTRA execution is not active", when)
		}
		policy := marshal.TierPolicy(workspaceUltraGate{gate: gate, execution: execution}, marshal.DefaultSettings())
		if policy.Tier != marshal.Ultra || policy.Concurrency < 2 || !policy.CrossReviewRequired || !policy.VerifierRequired {
			t.Fatalf("%s: dispatch policy %+v", when, policy)
		}
	}
	assertUltraDispatch("startup")
	for _, cmd := range []string{"/ultra", "/ultra status", "/ultra request", "/ultra stop", "/ultra stop confirm", "/ultra start"} {
		out, err := w.ExecuteCommand(t.Context(), cmd)
		if err != nil || out != ultraFixedAnswer {
			t.Fatalf("%s: %q %v", cmd, out, err)
		}
		assertUltraDispatch("after " + cmd)
	}
}
