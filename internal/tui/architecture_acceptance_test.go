package tui

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
)

func TestArchitectureAcceptanceProposalEditionGrammar(t *testing.T) {
	valid := `MARSHAL_PROPOSAL {"action":"accept-architecture","id":"architecture-1","revision":"2","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","instruction_ref":"operator-1"}`
	_, err := parseMarshalProposal(valid)
	if (err == nil) != architecturecodec.ModelArchitectureApprovalAllowed() {
		t.Fatalf("edition grammar: %v", err)
	}
	for _, invalid := range []string{
		`MARSHAL_PROPOSAL {"action":"accept-architecture","id":"architecture-1","revision":"2","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		`MARSHAL_PROPOSAL {"action":"accept-architecture","id":"architecture-1","revision":"0","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","instruction_ref":"operator-1"}`,
		`MARSHAL_PROPOSAL {"action":"accept-architecture","id":"architecture-1","revision":"2","digest":"forged","instruction_ref":"operator-1"}`,
		`MARSHAL_PROPOSAL {"action":"accept-architecture","id":"architecture-1","revision":"2","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","instruction_ref":""}`,
	} {
		if _, err := parseMarshalProposal(invalid); err == nil {
			t.Fatalf("malformed acceptance admitted: %s", invalid)
		}
	}
}
