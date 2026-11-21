//go:build linux && !governed

package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshalsteps"
)

// Every broker call the Marshal makes is one step on the Board, whatever it
// answers, and lands in the step log, so a Board started later on the same
// project shows it too.
func TestProposalBrokerNotesMarshalSteps(t *testing.T) {
	w, lease, _ := liveMCPFixture(t)
	raw, _ := json.Marshal(map[string]string{"read_id": "none"})
	if _, err := w.handleProposalMCP(t.Context(), lease, "project_contract_context_abort", raw); err != nil {
		t.Fatal(err)
	}
	_, _ = w.handleProposalMCP(t.Context(), lease, "project_contract_conformance", json.RawMessage(`{}`))
	items, last, provider := w.marshalSteps.snapshot()
	if last.IsZero() || provider != lease.provider {
		t.Fatalf("last %v provider %q, want the lease's", last, provider)
	}
	if len(items) != 2 || items[0].Kind != "check" || !strings.HasPrefix(items[0].Text, "architecture conformance") || items[1].Kind != "start" {
		t.Fatalf("steps = %+v, want start then the conformance check", items)
	}
	var later marshalSteps
	later.bind(marshalsteps.Path(w.runtime.StateDirectory()))
	again, lastAgain, _ := later.snapshot()
	if len(again) != len(items) || again[0] != items[0] || !lastAgain.Equal(last) {
		t.Fatalf("step log: %+v at %v, want %+v at %v", again, lastAgain, items, last)
	}
}
