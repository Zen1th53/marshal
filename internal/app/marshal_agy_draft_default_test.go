//go:build governed || !linux

package app

import (
	"strings"
	"testing"
)

func TestUnavailableMarshalDraftRefusesGovernedAGY(t *testing.T) {
	_, err := (&MarshalService{ProjectID: "PROJECT-11111111111111111111111111111111"}).DraftFromProposal(fourProviderMarshalDraft(t), "codex")
	if err == nil || !strings.Contains(err.Error(), "worker agy does not support governed mode") {
		t.Fatalf("unexpected admission: %v", err)
	}
}
