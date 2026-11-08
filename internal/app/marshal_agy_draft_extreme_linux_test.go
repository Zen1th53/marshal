//go:build !governed && linux

package app

import (
	"github.com/Zen1th53/marshal/internal/marshal"
	"testing"
)

func TestPrivateMarshalDraftAdmitsFourIndependentGovernedProviders(t *testing.T) {
	data := fourProviderMarshalDraft(t)
	service := &MarshalService{ProjectID: "PROJECT-11111111111111111111111111111111"}
	draft, err := service.DraftFromProposal(data, "codex")
	if err != nil {
		t.Fatalf("actual four-provider handoff refused: %v", err)
	}
	if len(draft.Tasks) != 4 {
		t.Fatalf("tasks=%d", len(draft.Tasks))
	}
	for i, worker := range []string{"codex", "claude", "opencode", "agy"} {
		task := draft.Tasks[i]
		if task.Worker != worker || task.Mode != marshal.Governed || len(task.DependsOn) != 0 || len(task.Files) != 1 || task.Files[0] != worker+"-marker.txt" {
			t.Fatalf("assignment changed: %+v", task)
		}
	}
	if err := validateDraft(draft); err != nil {
		t.Fatal(err)
	}
}
