//go:build !governed

package tui

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/permission"
)

// Plan approval raised by an operator command and delivery confirmations raised
// by the runtime apply under Marshal authority: no popup, evidence recorded.
// A foreign proposal for the same action still goes to the Marshal.
func TestExtremeRunActionsApplyWithoutPopup(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-extreme-run-actions", false)
	for _, tc := range []struct {
		action, proposal string
		automatic        bool
	}{
		{"approve", "", true},
		{"deliver", "marshal-delivery-0001", true},
		{"approve", "worker-proposal", false},
	} {
		p := marshalProposal{action: tc.action, occurrence: tc.proposal}
		req := permission.Request{ProposalID: tc.proposal, Kind: "marshal-command", Object: p.command(), Scope: "run RUN-x", Who: p.requester(), Reason: "Apply this exact runtime action", RunID: "RUN-x", TaskID: "plan:1:" + tc.action + tc.proposal}
		w.permissions.mu.Lock()
		if w.permissions.runProposals == nil {
			w.permissions.runProposals = map[string]marshalProposal{}
		}
		w.permissions.runProposals[req.Key()] = p
		w.permissions.mu.Unlock()
		if got := w.routeAutomaticRunAction(t.Context(), req, "claude"); got != tc.automatic {
			t.Fatalf("%s %q: automatic %v, want %v", tc.action, tc.proposal, got, tc.automatic)
		}
		if !tc.automatic {
			continue
		}
		events, err := rt.Store().ListEvents(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range events {
			if e.Type == "PERMISSION_DECIDED" && e.Data["object"] == req.Object && e.Data["task_id"] == req.TaskID {
				found = e.Data["allow"] == true && e.Data["decided_by"] == "marshal-runtime" && e.Data["decision_reason"] == extremeRunActionReason && e.Data["source"] == "marshal-extreme"
			}
		}
		if !found {
			t.Fatalf("%s: run action evidence missing", tc.action)
		}
	}
}
