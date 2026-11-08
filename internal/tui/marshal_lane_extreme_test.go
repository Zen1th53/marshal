//go:build !governed

package tui

import "testing"

func TestExtremeModelTaskApprovalProposalIsStrict(t *testing.T) {
	p, err := parseMarshalProposal(`MARSHAL_PROPOSAL {"action":"approve-task","id":"approval-123"}`)
	if err != nil || p.command() != "/marshal approve-task approval-123" {
		t.Fatalf("proposal: %+v %v", p, err)
	}
	for _, text := range []string{`{"action":"approve-task","id":""}`, `{"action":"approve-task","id":"../a"}`, `{"action":"approve-task","id":"a","provider":"codex"}`, `{"action":"approve-task","id":"a","allow":true}`} {
		if _, err := parseMarshalProposal(marshalProposalPrefix + text); err == nil {
			t.Fatal("forged authority field accepted")
		}
	}
}
