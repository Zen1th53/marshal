//go:build !governed

package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

// This local protocol fixture proves immutable native question composition, not
// a provider/model execution or release acceptance.
func TestProviderTransportLiveMCPQuestionRetainsExactReview(t *testing.T) {
	w, l, cfg := liveMCPFixture(t)
	approval, err := w.runtime.Execution().Engine().ApprovalManager().RequestApproval(execution.ApprovalRequest{RunID: "owned-transport-lane", TaskID: "fix", PlanID: "PLAN-exact", PlanVersion: 2, OperationType: "PROVIDER_TRANSPORT", TargetResource: "chatgpt.com:443", RiskLevel: model.R3, Scope: "exact task/provider/endpoint/plan/head/attempt binding; no LAN or arbitrary egress", CurrentState: "immutable package and source", Parameters: "exact fixed provider API", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(approval)
	req := permission.Request{Kind: "marshal-command", Object: "/marshal approve-task " + approval.ApprovalID, RunID: "marshal-run", TaskID: "3:7", Scope: "exact owner lane"}
	w.describePrivateTransportApproval(&req, approval.ApprovalID)
	if req.ApprovalBinding != string(before) || !strings.Contains(req.Reason, "Separate explicit review") || !strings.Contains(req.Scope, approval.Scope) {
		t.Fatalf("question lost exact transport scope: %+v", req)
	}
	w.permissions.build.pending = map[string]*buildPendingDecision{"transport-question": {request: req, provider: "codex", launchID: l.id, deadline: time.Now().Add(time.Minute)}}
	if _, err := proposalmcp.Call(t.Context(), cfg, "submit", json.RawMessage(`{"submission_id":"wrong-question","proposal":{"action":"decide","id":"other-question","allow":true,"reason":"exact provider transport only"}}`)); err == nil {
		t.Fatal("unmatched model question admitted")
	}
	receipt := submitMCP(t, cfg, `{"submission_id":"exact-transport","proposal":{"action":"decide","id":"transport-question","allow":true,"reason":"exact provider transport only"}}`)
	if receipt.State != "answer_accepted" {
		t.Fatal(receipt)
	}
	pending, err := w.store.PendingMarshalProposals(t.Context(), w.projectID)
	if err != nil {
		t.Fatal(err)
	}
	var accepted permission.Request
	var raw string
	for _, stored := range pending {
		if stored.ID == receipt.OccurrenceID {
			raw = stored.RequestJSON
		}
	}
	if err := json.Unmarshal([]byte(raw), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.ApprovalBinding != string(before) || accepted.Object != req.Object || accepted.SourceLaunch != l.id || accepted.SourceOccurrence != receipt.OccurrenceID {
		t.Fatalf("native acceptance changed reviewed snapshot: %+v", accepted)
	}
	if _, err := w.store.AppliedMCPTransportReview(t.Context(), w.projectID, approval.ApprovalID); err == nil {
		t.Fatal("accepted-only answer became applied authority")
	}
	w.revokeProposalLease(l)
	if _, err := proposalmcp.Call(t.Context(), cfg, "submit", json.RawMessage(`{"submission_id":"revoked","proposal":{"action":"decide","id":"transport-question","allow":true,"reason":"exact provider transport only"}}`)); err == nil {
		t.Fatal("revoked native generation accepted review")
	}
}
