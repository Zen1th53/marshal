package tui

import (
	"errors"
	"strings"
	"testing"
)

// A line already on the card, line a included, is updated by sending the
// whole card again with that line changed; a card the parser refuses says
// which line broke which rule instead of a bare "invalid" refusal.
func TestRequirementsLineAUpdateAndRefusalSaysWhy(t *testing.T) {
	w := &Workspace{}
	first, err := parseMarshalProposal(reqProposalLine(exampleReqCard))
	if err != nil {
		t.Fatal(err)
	}
	w.openRequirements(first)
	updated := strings.Replace(exampleReqCard, `"text":"Add login with email; out: OAuth","source":"you"`, `"text":"Add login with email and magic links","source":"you, edit a"`, 1)
	second, err := parseMarshalProposal(reqProposalLine(updated))
	if err != nil {
		t.Fatal(err)
	}
	w.openRequirements(second)
	card := w.currentReq()
	if card == nil || card.Lines[0].Text != "Add login with email and magic links" || card.Lines[0].Source != "you, edit a" {
		t.Fatalf("line a not updated: %+v", card)
	}

	long := strings.Replace(exampleReqCard, "Add login with email; out: OAuth", strings.Repeat("x", 243), 1)
	_, err = parseMarshalProposal(reqProposalLine(long))
	if err == nil || !errors.Is(err, errReqCard) || !strings.Contains(err.Error(), "line a text is 243 bytes, at most 200") || !strings.Contains(err.Error(), "invalid or non-allow-listed Marshal proposal") {
		t.Fatalf("refusal = %v, want it to name line a and the 200-byte limit", err)
	}
}
