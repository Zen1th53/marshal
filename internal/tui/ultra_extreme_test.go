//go:build !governed

package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestExtremeSessionStartsUltraAndCanStop(t *testing.T) {
	fakeProviderCLIs(t, "codex", "claude", "opencode", "agy")
	w := NewWorkspace(nil, "proj", "session")
	w.AttachULTRASession(cloud.Authorize(t.Context(), cloud.Config{}, t.TempDir(), "test"))
	gate, execution := w.ultraGate()
	if !execution || !ultraActive(w.GetUIState()) {
		t.Fatal("ULTRA execution did not start active")
	}
	policy := marshal.TierPolicy(workspaceUltraGate{gate: gate, execution: execution}, marshal.DefaultSettings())
	if policy.Tier != marshal.Ultra || policy.Concurrency < 2 || !policy.CrossReviewRequired || !policy.VerifierRequired {
		t.Fatalf("startup dispatch: %+v", policy)
	}
	for _, cmd := range []string{"/ultra", "/ultra status", "/ultra request"} {
		out, err := w.ExecuteCommand(t.Context(), cmd)
		if err != nil || out != "ULTRA: ACTIVE (Marshal build, no cloud check)" {
			t.Fatalf("%s: %q %v", cmd, out, err)
		}
	}
	for _, cmd := range []string{"/ultra stop", "/ultra stop confirm"} {
		if _, err := w.ExecuteCommand(t.Context(), cmd); err != nil {
			t.Fatal(err)
		}
	}
	gate, execution = w.ultraGate()
	if execution || !gate.Entitled() || ultraActive(w.GetUIState()) {
		t.Fatal("stop did not disable execution while retaining entitlement")
	}
	out, err := w.ExecuteCommand(t.Context(), "/ultra status")
	if err != nil || !strings.Contains(out, "EXECUTION OFF") {
		t.Fatalf("stopped status: %q %v", out, err)
	}
	policy = marshal.TierPolicy(workspaceUltraGate{gate: gate, execution: execution}, marshal.DefaultSettings())
	if policy.Tier != marshal.Standard {
		t.Fatal("stopped session still dispatches ULTRA")
	}
	if _, err := w.ExecuteCommand(t.Context(), "/ultra start"); err != nil {
		t.Fatal(err)
	}
	if !ultraActive(w.GetUIState()) {
		t.Fatal("restart failed")
	}
}
