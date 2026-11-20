//go:build !governed

package tui

import (
	"encoding/json"
	"testing"
)

// Every broker call the Marshal makes is one step on the Board, noted
// before the call is served, whatever it answers.
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
	if len(items) != 2 || items[0].Kind != "check" || items[0].Text != "architecture conformance" || items[1].Kind != "start" {
		t.Fatalf("steps = %+v, want start then the conformance check", items)
	}
}
